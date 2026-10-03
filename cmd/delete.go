package cmd

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/martinmares/oci-toolbox/internal/auth"
	"github.com/spf13/cobra"
)

type deleteOptions struct {
	apply, insecure                                      bool
	creds, expectDigest, artifactoryURL, artifactoryPath string
	timeout                                              time.Duration
}

func newDeleteCommand(manifest bool) *cobra.Command {
	o := deleteOptions{}
	use, short := "delete-tag IMAGE:TAG", "Preview or delete one registry tag without deleting its manifest by digest"
	if manifest {
		use, short = "delete-manifest IMAGE@sha256:DIGEST", "Preview or delete a manifest and all tags pointing to its digest in this repository"
	}
	c := &cobra.Command{Use: use, Short: short, Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		return runDelete(c.Context(), c.OutOrStdout(), args[0], manifest, o)
	}}
	c.Flags().BoolVar(&o.apply, "apply", false, "perform deletion; without this flag only inspect and print the target")
	c.Flags().BoolVar(&o.insecure, "insecure", false, "skip registry TLS certificate verification")
	c.Flags().StringVar(&o.creds, "creds", "", "registry credentials user:password; otherwise use Docker credential helpers")
	c.Flags().DurationVar(&o.timeout, "timeout", 60*time.Second, "timeout for the complete operation")
	if !manifest {
		c.Flags().StringVar(&o.expectDigest, "expect-digest", "", "refuse deletion if the tag does not point to this digest")
		c.Flags().StringVar(&o.artifactoryURL, "artifactory-url", "", "explicit Artifactory base URL, e.g. https://HOST/artifactory")
		c.Flags().StringVar(&o.artifactoryPath, "artifactory-path", "", "explicit storage path REPO_KEY/IMAGE_PATH, without the tag")
	}
	return c
}

func init() {
	rootCmd.AddCommand(newDeleteCommand(false), newDeleteCommand(true))
}

func missingManifest(err error) bool {
	var e *transport.Error
	return errors.As(err, &e) && e.StatusCode == http.StatusNotFound
}

func runDelete(parent context.Context, out io.Writer, input string, manifest bool, o deleteOptions) error {
	if o.timeout <= 0 {
		return fmt.Errorf("--timeout must be positive")
	}
	ctx, cancel := context.WithTimeout(parent, o.timeout)
	defer cancel()
	input = stripScheme(input)
	var ref name.Reference
	var err error
	if manifest {
		if !strings.Contains(input, "@sha256:") {
			return fmt.Errorf("delete-manifest requires an explicit @sha256:DIGEST, not a tag or layer reference")
		}
		ref, err = name.NewDigest(input, name.StrictValidation)
	} else {
		if strings.Contains(input, "@") || strings.LastIndex(input, ":") <= strings.LastIndex(input, "/") {
			return fmt.Errorf("delete-tag requires an explicit IMAGE:TAG; no implicit latest tag is allowed")
		}
		ref, err = name.NewTag(input, name.StrictValidation)
	}
	if err != nil {
		return err
	}
	if o.creds != "" && (parseCreds(o.creds) == nil || !strings.Contains(o.creds, ":")) {
		return fmt.Errorf("--creds must be user:password")
	}
	if (o.artifactoryURL == "") != (o.artifactoryPath == "") {
		return fmt.Errorf("--artifactory-url and --artifactory-path must be supplied together")
	}
	var artifactoryTarget *url.URL
	if o.artifactoryURL != "" {
		if manifest {
			return fmt.Errorf("Artifactory storage deletion is only supported for delete-tag")
		}
		artifactoryTarget, err = deletionStorageURL(ref, o)
		if err != nil {
			return err
		}
	}
	opts := append(auth.RemoteOptions(parseCreds(o.creds)), remote.WithContext(ctx))
	if o.insecure {
		opts = append(opts, auth.InsecureTransport())
	}
	desc, err := remote.Head(ref, opts...)
	if missingManifest(err) {
		_, err = fmt.Fprintf(out, "Already absent: %s\n", ref.Name())
		return err
	}
	if err != nil {
		return fmt.Errorf("inspect deletion target: %w", err)
	}
	digest := desc.Digest.String()
	if manifest && ref.Identifier() != digest {
		return fmt.Errorf("registry returned digest %s instead of %s", digest, ref.Identifier())
	}
	if o.expectDigest != "" && o.expectDigest != digest {
		return fmt.Errorf("tag points to %s, expected %s; nothing deleted", digest, o.expectDigest)
	}
	if _, err := fmt.Fprintf(out, "Target: %s\nManifest digest: %s\n", ref.Name(), digest); err != nil {
		return err
	}
	if manifest {
		tags, err := remote.List(ref.Context(), opts...)
		if err != nil {
			return fmt.Errorf("list tags before manifest deletion: %w", err)
		}
		sort.Strings(tags)
		var affected []string
		for _, tag := range tags {
			info, err := remote.Head(ref.Context().Tag(tag), opts...)
			if missingManifest(err) {
				continue
			}
			if err != nil {
				return fmt.Errorf("inspect tag %s before manifest deletion: %w", tag, err)
			}
			if info.Digest.String() == digest {
				affected = append(affected, tag)
			}
		}
		if _, err := fmt.Fprintf(out, "Tags currently pointing to this manifest: %s\nManifest deletion affects these tags; no blobs or child manifests are deleted by this command.\n", strings.Join(affected, ", ")); err != nil {
			return err
		}
	} else {
		if _, err := fmt.Fprintln(out, "Delete only this tag; never fall back to deleting its manifest digest."); err != nil {
			return err
		}
	}
	if artifactoryTarget != nil {
		if _, err := fmt.Fprintf(out, "Artifactory storage target: %s\n", artifactoryTarget); err != nil {
			return err
		}
	}
	if !o.apply {
		_, err := fmt.Fprintln(out, "Dry-run: nothing deleted. Repeat with --apply to execute.")
		return err
	}
	// Recheck the tag after preview: do not knowingly delete a retargeted tag.
	current, err := remote.Head(ref, opts...)
	if missingManifest(err) {
		_, err = fmt.Fprintln(out, "Already absent; nothing deleted.")
		return err
	}
	if err != nil {
		return err
	}
	if current.Digest.String() != digest {
		return fmt.Errorf("deletion target changed during inspection; nothing deleted")
	}
	if artifactoryTarget != nil {
		err = deleteArtifactoryTag(ctx, ref, artifactoryTarget, o)
	} else {
		err = remote.Delete(ref, opts...)
	}
	if err != nil {
		if !manifest {
			return fmt.Errorf("tag deletion failed (no digest fallback): %w; if this registry requires Artifactory storage deletion, specify --artifactory-url and --artifactory-path", err)
		}
		return fmt.Errorf("manifest deletion failed: %w", err)
	}
	_, err = remote.Head(ref, opts...)
	if err == nil {
		return fmt.Errorf("registry accepted deletion but target is still readable; verify registry state before retrying")
	}
	if !missingManifest(err) {
		return fmt.Errorf("deletion request accepted but verification failed: %w", err)
	}
	_, err = fmt.Fprintf(out, "Deleted: %s\n", ref.Name())
	return err
}

func deletionStorageURL(ref name.Reference, o deleteOptions) (*url.URL, error) {
	u, err := url.Parse(o.artifactoryURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("--artifactory-url must be an HTTP(S) base URL without credentials, query or fragment")
	}
	if u.Host != ref.Context().RegistryStr() {
		return nil, fmt.Errorf("Artifactory URL must use the same host as the image reference")
	}
	parts := strings.Split(o.artifactoryPath, "/")
	if len(parts) < 2 {
		return nil, fmt.Errorf("--artifactory-path must be REPO_KEY/IMAGE_PATH, without the tag")
	}
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\%?#:\r\n") {
			return nil, fmt.Errorf("invalid Artifactory storage path")
		}
	}
	// Both repository-path and subdomain Artifactory routing are supported, but
	// an unrelated image path may never be used for a deletion target.
	imagePath := strings.Join(parts[1:], "/")
	repository := ref.Context().RepositoryStr()
	if repository != o.artifactoryPath && repository != imagePath {
		return nil, fmt.Errorf("Artifactory storage path does not match the image reference")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/" + o.artifactoryPath + "/" + ref.Identifier()
	return u, nil
}

func deleteArtifactoryTag(ctx context.Context, ref name.Reference, target *url.URL, o deleteOptions) error {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if o.insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	client := &http.Client{Transport: tr, CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }}
	defer tr.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, target.String(), nil)
	if err != nil {
		return err
	}
	if creds := parseCreds(o.creds); creds != nil {
		req.SetBasicAuth(creds.Username, creds.Password)
	} else {
		a, err := authn.Resolve(ctx, authn.DefaultKeychain, ref.Context())
		if err != nil {
			return err
		}
		cfg, err := authn.Authorization(ctx, a)
		if err != nil {
			return err
		}
		if cfg.RegistryToken != "" {
			req.Header.Set("Authorization", "Bearer "+cfg.RegistryToken)
		} else if cfg.Username != "" {
			req.SetBasicAuth(cfg.Username, cfg.Password)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("Artifactory DELETE returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}
