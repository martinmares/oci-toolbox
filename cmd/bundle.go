package cmd

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/spf13/cobra"

	"github.com/martinmares/oci-toolbox/internal/auth"
	"github.com/martinmares/oci-toolbox/internal/bundle"
	"github.com/martinmares/oci-toolbox/internal/logging"
)

var bundleOptions struct {
	releaseID      string
	sourceRegistry string
	targetRegistry string
	sourceProject  string
	targetProject  string
	extraTags      []string
}

var bundlePublishOptions struct {
	output         string
	bundleRevision string
	srcCreds       string
	destCreds      string
	srcInsecure    bool
	destInsecure   bool
	progress       bool
	debug          bool
}

var bundleCmd = &cobra.Command{
	Use:   "bundle",
	Short: "Validate, plan, and publish image bundles",
}

var bundleValidateCmd = &cobra.Command{
	Use:   "validate FILE",
	Short: "Validate a bundle YAML file without accessing registries",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		b, err := bundle.Load(args[0])
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "bundle %q is valid (%d images)\n", b.Name, len(b.Images))
		return nil
	},
}

var bundlePlanCmd = &cobra.Command{
	Use:   "plan FILE",
	Short: "Resolve source and destination references without copying images",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		plan, err := loadBundlePlan(args[0])
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "bundle: %s\nrelease: %s\n", plan.Name, plan.ReleaseID)
		for i, item := range plan.Images {
			fmt.Fprintf(cmd.OutOrStdout(), "%d. %s\n   %s\n   -> %s\n", i+1, item.ID, item.SourceRef, item.TargetRef)
			if len(item.ExtraTags) > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "   extra tags: %s\n", strings.Join(item.ExtraTags, ", "))
			}
		}
		return nil
	},
}

var bundlePublishCmd = &cobra.Command{
	Use:   "publish FILE",
	Short: "Publish all bundle images and create an immutable release manifest",
	Args:  cobra.ExactArgs(1),
	RunE:  runBundlePublish,
}

func init() {
	bundleCmd.PersistentFlags().StringVar(&bundleOptions.releaseID, "release-id", "", "release identifier and primary destination tag")
	bundleCmd.PersistentFlags().StringVar(&bundleOptions.sourceRegistry, "src-registry", "", "override source registry")
	bundleCmd.PersistentFlags().StringVar(&bundleOptions.targetRegistry, "dest-registry", "", "override destination registry")
	bundleCmd.PersistentFlags().StringVar(&bundleOptions.sourceProject, "src-project", "", "override source project path")
	bundleCmd.PersistentFlags().StringVar(&bundleOptions.targetProject, "dest-project", "", "override destination project path")
	bundleCmd.PersistentFlags().StringArrayVar(&bundleOptions.extraTags, "extra-tag", nil, "additional destination tag for every image (repeatable)")

	bundlePublishCmd.Flags().StringVarP(&bundlePublishOptions.output, "output", "o", "", "release manifest output path")
	bundlePublishCmd.Flags().StringVar(&bundlePublishOptions.bundleRevision, "bundle-revision", "", "Git revision of the input bundle")
	bundlePublishCmd.Flags().StringVar(&bundlePublishOptions.srcCreds, "src-creds", "", "source registry credentials user:password")
	bundlePublishCmd.Flags().StringVar(&bundlePublishOptions.destCreds, "dest-creds", "", "destination registry credentials user:password")
	bundlePublishCmd.Flags().BoolVar(&bundlePublishOptions.srcInsecure, "src-insecure", false, "skip source registry TLS verification")
	bundlePublishCmd.Flags().BoolVar(&bundlePublishOptions.destInsecure, "dest-insecure", false, "skip destination registry TLS verification")
	bundlePublishCmd.Flags().BoolVar(&bundlePublishOptions.progress, "progress", false, "show copy progress")
	bundlePublishCmd.Flags().BoolVar(&bundlePublishOptions.debug, "debug", false, "enable verbose output")

	bundleCmd.AddCommand(bundleValidateCmd, bundlePlanCmd, bundlePublishCmd)
	rootCmd.AddCommand(bundleCmd)
}

func loadBundlePlan(path string) (*bundle.Plan, error) {
	b, err := bundle.Load(path)
	if err != nil {
		return nil, err
	}
	return bundle.Resolve(b, bundle.ResolveOptions{
		ReleaseID:      bundleOptions.releaseID,
		SourceRegistry: bundleOptions.sourceRegistry,
		TargetRegistry: bundleOptions.targetRegistry,
		SourceProject:  bundleOptions.sourceProject,
		TargetProject:  bundleOptions.targetProject,
		ExtraTags:      bundleOptions.extraTags,
	})
}

type publishPreflight struct {
	item         bundle.PlanImage
	sourceDigest string
	platform     string
	targetExists bool
}

func runBundlePublish(cmd *cobra.Command, args []string) error {
	if strings.TrimSpace(bundlePublishOptions.output) == "" {
		return errors.New("--output is required")
	}
	if err := ensureReleaseOutputAvailable(bundlePublishOptions.output); err != nil {
		return err
	}
	if bundlePublishOptions.debug && !bundlePublishOptions.progress {
		logging.Setup(slog.LevelDebug)
	} else {
		logging.Setup(slog.LevelInfo)
	}
	plan, err := loadBundlePlan(args[0])
	if err != nil {
		return err
	}
	sourceOpts := registryOptions(bundlePublishOptions.srcCreds, bundlePublishOptions.srcInsecure)
	targetOpts := registryOptions(bundlePublishOptions.destCreds, bundlePublishOptions.destInsecure)

	preflight := make([]publishPreflight, 0, len(plan.Images))
	for _, item := range plan.Images {
		sourceDesc, err := fetchImageManifest(item.SourceRef, sourceOpts)
		if err != nil {
			return fmt.Errorf("image %q source: %w", item.ID, err)
		}
		platform, err := verifiedPlatform(sourceDesc)
		if err != nil {
			return fmt.Errorf("image %q source: %w", item.ID, err)
		}
		targetDesc, exists, err := fetchOptionalManifest(item.TargetRef, targetOpts)
		if err != nil {
			return fmt.Errorf("image %q target preflight: %w", item.ID, err)
		}
		sourceDigest := sourceDesc.Digest.String()
		if exists && targetDesc.Digest.String() != sourceDigest {
			return fmt.Errorf(
				"image %q destination tag %s already points to %s, source is %s",
				item.ID, item.TargetRef, targetDesc.Digest, sourceDigest,
			)
		}
		preflight = append(preflight, publishPreflight{
			item:         item,
			sourceDigest: sourceDigest,
			platform:     platform,
			targetExists: exists,
		})
	}

	copyOpts := copyRunOptions{
		SrcCreds:      bundlePublishOptions.srcCreds,
		DestCreds:     bundlePublishOptions.destCreds,
		SrcInsecure:   bundlePublishOptions.srcInsecure,
		DestInsecure:  bundlePublishOptions.destInsecure,
		Progress:      bundlePublishOptions.progress,
		Debug:         bundlePublishOptions.debug,
		RecipePaths:   nil,
		DestNoReuse:   false,
		Reduce:        false,
		ReduceChanges: false,
	}

	manifest := &bundle.ReleaseManifest{
		ReleaseID:    plan.ReleaseID,
		CreatedAt:    time.Now().UTC(),
		Bundle:       &bundle.ReleaseBundle{Name: plan.Name, Revision: strings.TrimSpace(bundlePublishOptions.bundleRevision)},
		RegistryBase: registryBase(plan.TargetRegistry, plan.TargetProject),
		Platform:     "linux/amd64",
		ExtraTags:    append([]string(nil), plan.ExtraTags...),
		Images:       make([]bundle.ReleaseImage, 0, len(preflight)),
	}

	for i, entry := range preflight {
		item := entry.item
		sourceDigestRef := item.SourceImage + "@" + entry.sourceDigest
		if entry.targetExists {
			if err := tagRemoteManifest(item.TargetImage, entry.sourceDigest, item.ExtraTags, targetOpts); err != nil {
				return fmt.Errorf("image %q extra tags: %w", item.ID, err)
			}
			slog.Info("destination already matches source digest", "image", item.ID, "destination", item.TargetRef)
		} else {
			label := fmt.Sprintf("bundle %d/%d", i+1, len(preflight))
			if err := runCopyOne(cmd.Context(), "docker://"+sourceDigestRef, "docker://"+item.TargetRef, label, item.ExtraTags, copyOpts); err != nil {
				return fmt.Errorf("publishing image %q: %w", item.ID, err)
			}
		}

		targetDesc, err := fetchImageManifest(item.TargetRef, targetOpts)
		if err != nil {
			return fmt.Errorf("verifying image %q destination: %w", item.ID, err)
		}
		if targetDesc.Digest.String() != entry.sourceDigest {
			return fmt.Errorf(
				"image %q destination digest %s differs from source digest %s",
				item.ID, targetDesc.Digest, entry.sourceDigest,
			)
		}
		manifest.Images = append(manifest.Images, bundle.ReleaseImage{
			ID:            item.ID,
			AppName:       item.AppName,
			ContainerName: item.ContainerName,
			Source: &bundle.ReleaseSource{
				Image:  item.SourceImage,
				Tag:    item.RequestedTag,
				Digest: entry.sourceDigest,
			},
			Image:     item.TargetImage,
			Tag:       plan.ReleaseID,
			Digest:    targetDesc.Digest.String(),
			ExtraTags: append([]string(nil), item.ExtraTags...),
			Platform:  entry.platform,
		})
	}

	if err := writeReleaseManifest(cmd, bundlePublishOptions.output, manifest); err != nil {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "release manifest written to %s\n", bundlePublishOptions.output)
	return nil
}

func registryOptions(creds string, insecure bool) []remote.Option {
	opts := auth.RemoteOptions(parseCreds(creds))
	if insecure {
		opts = append(opts, auth.InsecureTransport())
	}
	return opts
}

func fetchImageManifest(refValue string, opts []remote.Option) (*remote.Descriptor, error) {
	desc, err := fetchManifestDescriptor(refValue, opts)
	if err != nil {
		return nil, err
	}
	if desc.MediaType.IsIndex() {
		return nil, fmt.Errorf(
			"multi-platform image index %s is not supported yet; expected a single linux/amd64 image manifest",
			desc.Digest,
		)
	}
	return desc, nil
}

func fetchManifestDescriptor(refValue string, opts []remote.Option) (*remote.Descriptor, error) {
	ref, err := name.ParseReference(stripScheme(refValue))
	if err != nil {
		return nil, fmt.Errorf("invalid image reference: %w", err)
	}
	desc, err := remote.Get(ref, opts...)
	if err != nil {
		return nil, err
	}
	if !desc.MediaType.IsImage() && !desc.MediaType.IsIndex() {
		return nil, fmt.Errorf("unsupported manifest media type %s", desc.MediaType)
	}
	return desc, nil
}

func fetchOptionalManifest(refValue string, opts []remote.Option) (*remote.Descriptor, bool, error) {
	desc, err := fetchImageManifest(refValue, opts)
	if err == nil {
		return desc, true, nil
	}
	var registryErr *transport.Error
	if errors.As(err, &registryErr) && registryErr.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	return nil, false, err
}

func verifiedPlatform(desc *remote.Descriptor) (string, error) {
	platform, err := detectedPlatform(desc)
	if err != nil {
		return "", err
	}
	if platform != "linux/amd64" {
		return "", fmt.Errorf("unsupported platform %q; only linux/amd64 is supported", platform)
	}
	return platform, nil
}

func detectedPlatform(desc *remote.Descriptor) (string, error) {
	img, err := desc.Image()
	if err != nil {
		return "", fmt.Errorf("reading image config: %w", err)
	}
	cfg, err := img.ConfigFile()
	if err != nil {
		return "", fmt.Errorf("reading image config: %w", err)
	}
	platform := strings.Trim(cfg.OS+"/"+cfg.Architecture, "/")
	if platform == "" {
		return "", fmt.Errorf("image config does not specify OS or architecture")
	}
	return platform, nil
}

func tagRemoteManifest(repository, digest string, tags []string, opts []remote.Option) error {
	if len(tags) == 0 {
		return nil
	}
	source, err := name.NewDigest(repository + "@" + digest)
	if err != nil {
		return err
	}
	desc, err := remote.Get(source, opts...)
	if err != nil {
		return err
	}
	for _, tag := range tags {
		target, err := name.NewTag(repository + ":" + tag)
		if err != nil {
			return err
		}
		if err := remote.Tag(target, desc, opts...); err != nil {
			return fmt.Errorf("tagging %s: %w", target, err)
		}
	}
	return nil
}

func registryBase(registry, project string) string {
	registry = strings.Trim(registry, "/")
	project = strings.Trim(project, "/")
	if project == "" {
		return registry
	}
	return registry + "/" + project
}

func writeReleaseManifest(cmd *cobra.Command, output string, manifest *bundle.ReleaseManifest) error {
	if output == "-" {
		return bundle.EncodeRelease(cmd.OutOrStdout(), manifest)
	}
	if err := ensureReleaseOutputAvailable(output); err != nil {
		return err
	}
	dir := filepath.Dir(output)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(output)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := bundle.EncodeRelease(tmp, manifest); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, output); err != nil {
		return err
	}
	return nil
}

func ensureReleaseOutputAvailable(output string) error {
	if output == "-" {
		return nil
	}
	if _, err := os.Stat(output); err == nil {
		return fmt.Errorf("output file already exists: %s", output)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
