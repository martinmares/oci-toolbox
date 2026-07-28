package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/spf13/cobra"

	"github.com/martinmares/oci-toolbox/internal/bundle"
)

var reconstructOptions struct {
	file      string
	output    string
	releaseID string
	creds     string
	insecure  bool
	extraTags []string
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
    --output release.yml

  oci-toolbox release reconstruct --file release-images.yml --output release.yml`,
}

func init() {
	releaseReconstructCmd.Flags().StringVarP(&reconstructOptions.file, "file", "f", "", "strict YAML file containing image references and deployment selectors")
	releaseReconstructCmd.Flags().StringVarP(&reconstructOptions.output, "output", "o", "", "release manifest output path")
	releaseReconstructCmd.Flags().StringVar(&reconstructOptions.releaseID, "release-id", "", "release identifier; inferred when every input uses the same tag")
	releaseReconstructCmd.Flags().StringVar(&reconstructOptions.creds, "creds", "", "registry credentials user:password")
	releaseReconstructCmd.Flags().BoolVar(&reconstructOptions.insecure, "insecure", false, "skip registry TLS verification")
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
		digest   string
		platform string
	}
	resolvedItems := make([]resolved, 0, len(items))
	commonTag := ""
	canInferTag := true
	ids := map[string]struct{}{}
	selectors := map[string]struct{}{}
	for i := range items {
		item := items[i]
		refValue := stripScheme(item.Ref)
		if !hasExplicitImageIdentifier(refValue) {
			return fmt.Errorf("images[%d].ref must contain an explicit tag or digest", i)
		}
		ref, err := name.ParseReference(refValue)
		if err != nil {
			return fmt.Errorf("images[%d].ref: %w", i, err)
		}
		desc, err := fetchImageManifest(ref.String(), opts)
		if err != nil {
			return fmt.Errorf("inspecting %s: %w", item.Ref, err)
		}
		platform, err := verifiedPlatform(desc)
		if err != nil {
			return fmt.Errorf("inspecting %s: %w", item.Ref, err)
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
			extraDesc, err := fetchImageManifest(ref.Context().Name()+":"+extraTag, opts)
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

		resolvedItems = append(resolvedItems, resolved{
			item:     item,
			image:    ref.Context().Name(),
			tag:      tag,
			digest:   desc.Digest.String(),
			platform: platform,
		})
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
		Platform:  "linux/amd64",
		ExtraTags: extraTags,
		Images:    make([]bundle.ReleaseImage, 0, len(resolvedItems)),
	}
	for _, item := range resolvedItems {
		manifest.Images = append(manifest.Images, bundle.ReleaseImage{
			ID:            item.item.ID,
			AppName:       item.item.AppName,
			ContainerName: item.item.ContainerName,
			Image:         item.image,
			Tag:           item.tag,
			Digest:        item.digest,
			ExtraTags:     append([]string(nil), extraTags...),
			Platform:      item.platform,
		})
	}
	if err := writeReleaseManifest(cmd, reconstructOptions.output, manifest); err != nil {
		return err
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "release manifest written to %s\n", reconstructOptions.output)
	return nil
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
