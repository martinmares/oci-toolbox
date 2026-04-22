package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/spf13/cobra"

	"github.com/datalite/oci-patch/internal/auth"
	"github.com/datalite/oci-patch/internal/image"
)

var (
	inspectCreds    string
	inspectOS       string
	inspectArch     string
	inspectVariant  string
	inspectNoTags   bool
	inspectInsecure bool
)

var inspectCmd = &cobra.Command{
	Use:   "inspect IMAGE",
	Short: "Display metadata of an OCI image (JSON, skopeo-compatible)",
	Args:  cobra.ExactArgs(1),
	Example: `  oci-patch inspect docker://registry.example.com/app:latest
  oci-patch inspect --creds user:pass docker://registry.example.com/app:latest
  oci-patch inspect --override-os linux --override-arch arm64 docker://docker.io/library/alpine:3.21`,
	RunE: runInspect,
}

func init() {
	inspectCmd.Flags().StringVar(&inspectCreds, "creds", "", "registry credentials user:password")
	inspectCmd.Flags().StringVar(&inspectOS, "override-os", "", "override OS (e.g. linux)")
	inspectCmd.Flags().StringVar(&inspectArch, "override-arch", "", "override architecture (e.g. arm64, amd64)")
	inspectCmd.Flags().StringVar(&inspectVariant, "override-variant", "", "override variant (e.g. v8)")
	inspectCmd.Flags().BoolVar(&inspectNoTags, "no-tags", false, "skip fetching repository tags")
	inspectCmd.Flags().BoolVar(&inspectInsecure, "insecure", false, "skip TLS certificate verification")
	rootCmd.AddCommand(inspectCmd)
}

func runInspect(c *cobra.Command, args []string) error {
	baseOpts := auth.RemoteOptions(parseCreds(inspectCreds))
	if inspectInsecure {
		baseOpts = append(baseOpts, auth.InsecureTransport())
	}

	rawRef := stripScheme(args[0])
	ref, err := name.ParseReference(rawRef)
	if err != nil {
		return fmt.Errorf("invalid image reference: %w", err)
	}

	opts := baseOpts
	if inspectOS != "" || inspectArch != "" || inspectVariant != "" {
		opts = append(opts, platformOption(inspectOS, inspectArch, inspectVariant))
	}

	img, err := remote.Image(ref, opts...)
	if err != nil {
		return fmt.Errorf("fetching image: %w", err)
	}

	var repoTags []string
	if !inspectNoTags {
		repoTags, err = fetchTags(ref, baseOpts)
		if err != nil {
			repoTags = []string{}
		}
	}

	refName := ref.Context().String()
	out, err := image.Inspect(img, refName, repoTags)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(c.OutOrStdout())
	enc.SetIndent("", "    ")
	return enc.Encode(out)
}

func platformOption(os, arch, variant string) remote.Option {
	p := v1.Platform{
		OS:           os,
		Architecture: arch,
		Variant:      variant,
	}
	return remote.WithPlatform(p)
}

func fetchTags(ref name.Reference, opts []remote.Option) ([]string, error) {
	return remote.List(ref.Context(), opts...)
}
