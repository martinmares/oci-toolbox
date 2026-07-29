package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/martinmares/oci-toolbox/internal/bundle"
	"github.com/martinmares/oci-toolbox/internal/progress"
)

var reconstructOptions struct {
	file         string
	output       string
	releaseID    string
	creds        string
	insecure     bool
	progress     bool
	discoverTags bool
	extraTags    []string
}

var releaseCmd = &cobra.Command{
	Use:   "release",
	Short: "Create and inspect release manifests",
}

var releaseReconstructCmd = &cobra.Command{
	Use:   "reconstruct [IMAGE...]",
	Short: "Reconstruct a release manifest from existing registry images",
	Args:  cobra.ArbitraryArgs,
	RunE:  runReleaseReconstruct,
	Example: `  oci-toolbox release reconstruct \
    registry.example.com/team/api:RE_2026.07.07.01 \
    registry.example.com/team/ui:RE_2026.07.07.01 \
    --release-id RE_2026.07.07.01 \
    --output release.yml \
    --discover-tags \
    --progress

  oci-toolbox release reconstruct --file release-images.yml --output release.yml`,
}

func init() {
	releaseReconstructCmd.Flags().StringVarP(&reconstructOptions.file, "file", "f", "", "strict YAML file containing image references and deployment selectors")
	releaseReconstructCmd.Flags().StringVarP(&reconstructOptions.output, "output", "o", "", "release manifest output path")
	releaseReconstructCmd.Flags().StringVar(&reconstructOptions.releaseID, "release-id", "", "release identifier; inferred when every input uses the same tag")
	releaseReconstructCmd.Flags().StringVar(&reconstructOptions.creds, "creds", "", "registry credentials user:password")
	releaseReconstructCmd.Flags().BoolVar(&reconstructOptions.insecure, "insecure", false, "skip registry TLS verification")
	releaseReconstructCmd.Flags().BoolVar(&reconstructOptions.progress, "progress", false, "show image inspection progress and ETA")
	releaseReconstructCmd.Flags().BoolVar(&reconstructOptions.discoverTags, "discover-tags", false, "discover registry tags that point to each resolved digest")
	releaseReconstructCmd.Flags().StringArrayVar(&reconstructOptions.extraTags, "extra-tag", nil, "record an additional release tag (repeatable)")
	releaseCmd.AddCommand(releaseReconstructCmd)
	rootCmd.AddCommand(releaseCmd)
}

func runReleaseReconstruct(cmd *cobra.Command, args []string) error {
	if strings.TrimSpace(reconstructOptions.output) == "" {
		return errors.New("--output is required")
	}
	if err := ensureReleaseOutputAvailable(reconstructOptions.output); err != nil {
		return err
	}
	items, err := reconstructItems(args, reconstructOptions.file)
	if err != nil {
		return err
	}
	opts := registryOptions(reconstructOptions.creds, reconstructOptions.insecure)
	extraTags := normalizeAdditionalTags(reconstructOptions.extraTags)
	for _, tag := range extraTags {
		if _, err := name.NewTag("example.invalid/repository:" + tag); err != nil {
			return fmt.Errorf("invalid extra tag %q: %w", tag, err)
		}
	}

	type resolved struct {
		item     bundle.ReconstructImage
		image    string
		tag      string
		tags     []string
		digest   string
		platform string
	}
	resolvedItems := make([]resolved, 0, len(items))
	commonTag := ""
	canInferTag := true
	commonPlatform := ""
	mixedPlatforms := false
	ids := map[string]struct{}{}
	selectors := map[string]struct{}{}
	var counter *progress.Counter
	if reconstructOptions.progress {
		counter = progress.NewCounter(len(items), "inspect images", cmd.ErrOrStderr())
		defer func() {
			_ = counter.Exit()
		}()
	}
	for i := range items {
		item := items[i]
		counter.Describe(fmt.Sprintf("inspect %d/%d: %s", i+1, len(items), shortRef(item.Ref)))
		refValue := stripScheme(item.Ref)
		if !hasExplicitImageIdentifier(refValue) {
			return fmt.Errorf("images[%d].ref must contain an explicit tag or digest", i)
		}
		ref, err := name.ParseReference(refValue)
		if err != nil {
			return fmt.Errorf("images[%d].ref: %w", i, err)
		}
		desc, err := fetchManifestDescriptor(ref.String(), opts)
		if err != nil {
			return fmt.Errorf("inspecting %s: %w", item.Ref, err)
		}
		platform, err := reconstructPlatform(desc)
		if err != nil {
			return fmt.Errorf("inspecting %s: %w", item.Ref, err)
		}
		if commonPlatform == "" {
			commonPlatform = platform
		} else if commonPlatform != platform {
			mixedPlatforms = true
		}
		tag := ""
		if tagged, ok := ref.(name.Tag); ok {
			tag = tagged.Identifier()
		} else {
			canInferTag = false
		}
		if commonTag == "" {
			commonTag = tag
		} else if commonTag != tag {
			canInferTag = false
		}

		inferred := bundle.InferredName(ref.Context().Name())
		if strings.TrimSpace(item.ID) == "" {
			item.ID = inferred
		}
		if strings.TrimSpace(item.AppName) == "" {
			item.AppName = inferred
		}
		if strings.TrimSpace(item.ContainerName) == "" {
			item.ContainerName = inferred
		}
		if _, exists := ids[item.ID]; exists {
			return fmt.Errorf("duplicate image id %q", item.ID)
		}
		ids[item.ID] = struct{}{}
		selector := item.AppName + "\x00" + item.ContainerName
		if _, exists := selectors[selector]; exists {
			return fmt.Errorf("duplicate deployment selector %q/%q", item.AppName, item.ContainerName)
		}
		selectors[selector] = struct{}{}

		for _, extraTag := range extraTags {
			extraDesc, err := fetchManifestDescriptor(ref.Context().Name()+":"+extraTag, opts)
			if err != nil {
				return fmt.Errorf("verifying extra tag %s:%s: %w", ref.Context().Name(), extraTag, err)
			}
			if extraDesc.Digest.String() != desc.Digest.String() {
				return fmt.Errorf(
					"extra tag %s:%s points to %s, expected %s",
					ref.Context().Name(), extraTag, extraDesc.Digest, desc.Digest,
				)
			}
		}

		var discoveredTags []string
		if reconstructOptions.discoverTags {
			counter.Describe(fmt.Sprintf("tags %d/%d: %s", i+1, len(items), shortRef(item.Ref)))
			discoveredTags, err = discoverMatchingTags(cmd.Context(), ref.Context(), desc.Digest.String(), opts)
			if err != nil {
				return fmt.Errorf("discovering tags for %s: %w", ref.Context().Name(), err)
			}
		}

		resolvedItems = append(resolvedItems, resolved{
			item:     item,
			image:    ref.Context().Name(),
			tag:      tag,
			tags:     discoveredTags,
			digest:   desc.Digest.String(),
			platform: platform,
		})
		if err := counter.Advance(); err != nil {
			return fmt.Errorf("updating progress: %w", err)
		}
	}
	if err := counter.Finish(); err != nil {
		return fmt.Errorf("finishing progress: %w", err)
	}
	counter = nil
	if mixedPlatforms {
		commonPlatform = ""
	}

	releaseID := strings.TrimSpace(reconstructOptions.releaseID)
	if releaseID == "" {
		if !canInferTag || commonTag == "" {
			return errors.New("--release-id is required when image references do not all use the same tag")
		}
		releaseID = commonTag
	}
	if _, err := name.NewTag("example.invalid/repository:" + releaseID); err != nil {
		return fmt.Errorf("invalid release ID %q: %w", releaseID, err)
	}
	manifest := &bundle.ReleaseManifest{
		ReleaseID: releaseID,
		CreatedAt: time.Now().UTC(),
		Platform:  commonPlatform,
		ExtraTags: extraTags,
		Images:    make([]bundle.ReleaseImage, 0, len(resolvedItems)),
	}
	for _, item := range resolvedItems {
		tag, imageExtraTags := reconstructedTags(
			item.tag,
			releaseID,
			item.tags,
			extraTags,
			reconstructOptions.discoverTags,
		)
		manifest.Images = append(manifest.Images, bundle.ReleaseImage{
			ID:            item.item.ID,
			AppName:       item.item.AppName,
			ContainerName: item.item.ContainerName,
			Image:         item.image,
			Tag:           tag,
			Digest:        item.digest,
			ExtraTags:     imageExtraTags,
			Platform:      item.platform,
		})
	}
	if err := writeReleaseManifest(cmd, reconstructOptions.output, manifest); err != nil {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "release manifest written to %s\n", reconstructOptions.output)
	return nil
}

func discoverMatchingTags(
	ctx context.Context,
	repository name.Repository,
	digest string,
	opts []remote.Option,
) ([]string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	remoteOpts := append(append([]remote.Option(nil), opts...), remote.WithContext(ctx))
	tags, err := remote.List(repository, remoteOpts...)
	if err != nil {
		return nil, fmt.Errorf("listing repository tags: %w", err)
	}

	var (
		mu      sync.Mutex
		matches []string
	)
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(8)
	for _, tag := range tags {
		tag := tag
		group.Go(func() error {
			ref, err := name.NewTag(repository.Name() + ":" + tag)
			if err != nil {
				return fmt.Errorf("parsing tag %q: %w", tag, err)
			}
			tagOpts := append(append([]remote.Option(nil), opts...), remote.WithContext(groupCtx))
			desc, err := remote.Get(ref, tagOpts...)
			if err != nil {
				return fmt.Errorf("inspecting tag %q: %w", tag, err)
			}
			if desc.Digest.String() == digest {
				mu.Lock()
				matches = append(matches, tag)
				mu.Unlock()
			}
			return nil
		})
	}
	if err := group.Wait(); err != nil {
		return nil, err
	}
	sort.Strings(matches)
	return matches, nil
}

func reconstructedTags(
	explicitTag string,
	releaseID string,
	discovered []string,
	requiredExtra []string,
	discoveryEnabled bool,
) (string, []string) {
	if !discoveryEnabled {
		return explicitTag, append([]string(nil), requiredExtra...)
	}

	primary := explicitTag
	if primary == "" && containsString(discovered, releaseID) {
		primary = releaseID
	}
	if primary == "" && len(discovered) == 1 {
		primary = discovered[0]
	}

	all := normalizeAdditionalTags(append(append([]string(nil), requiredExtra...), discovered...))
	extra := make([]string, 0, len(all))
	for _, tag := range all {
		if tag != primary {
			extra = append(extra, tag)
		}
	}
	return primary, extra
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func reconstructPlatform(desc *remote.Descriptor) (string, error) {
	if desc.MediaType.IsIndex() {
		return "multi-platform", nil
	}
	if !desc.MediaType.IsImage() {
		return "", fmt.Errorf("unsupported manifest media type %s", desc.MediaType)
	}
	return detectedPlatform(desc)
}

func hasExplicitImageIdentifier(ref string) bool {
	if strings.Contains(ref, "@") {
		return true
	}
	lastSlash := strings.LastIndex(ref, "/")
	return strings.Contains(ref[lastSlash+1:], ":")
}

func reconstructItems(args []string, file string) ([]bundle.ReconstructImage, error) {
	file = strings.TrimSpace(file)
	if file != "" && len(args) > 0 {
		return nil, errors.New("--file cannot be combined with positional IMAGE arguments")
	}
	if file == "" {
		if len(args) == 0 {
			return nil, errors.New("provide IMAGE arguments or --file")
		}
		items := make([]bundle.ReconstructImage, 0, len(args))
		for _, ref := range args {
			items = append(items, bundle.ReconstructImage{Ref: ref})
		}
		return items, nil
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, fmt.Errorf("reading reconstruct file %q: %w", file, err)
	}
	defer f.Close()
	input, err := bundle.DecodeReconstruct(f)
	if err != nil {
		return nil, fmt.Errorf("parsing reconstruct file %q: %w", file, err)
	}
	return input.Images, nil
}
