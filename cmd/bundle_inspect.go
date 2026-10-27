package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/martinmares/oci-toolbox/internal/bundle"
	"github.com/spf13/cobra"
)

var bundleDigestPlanPath string

type inspectedImage struct {
	ID            string `json:"id"`
	AppName       string `json:"app_name"`
	ContainerName string `json:"container_name"`
	SourceRef     string `json:"source_ref"`
	TargetRef     string `json:"target_ref"`
	Digest        string `json:"digest"`
}
type inspectedBundle struct {
	ReleaseID string           `json:"release_id"`
	Images    []inspectedImage `json:"images"`
}

var bundleInspectCmd = &cobra.Command{
	Use:   "inspect FILE",
	Short: "Read source manifest digests without downloading image layers; emit a publishable JSON plan",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		plan, _, err := effectiveBundlePlan(args[0])
		if err != nil {
			return err
		}
		source, err := registryOptionsWithProxy(bundlePublishOptions.srcCreds, bundlePublishOptions.srcInsecure, bundlePublishOptions.srcProxy)
		if err != nil {
			return err
		}
		destination, err := registryOptionsWithProxy(bundlePublishOptions.destCreds, bundlePublishOptions.destInsecure, bundlePublishOptions.destProxy)
		if err != nil {
			return err
		}
		result := inspectedBundle{ReleaseID: plan.ReleaseID}
		for _, image := range plan.Images {
			opts := source
			if image.FromBaseRelease {
				opts = destination
			}
			opts = append(append([]remote.Option(nil), opts...), remote.WithContext(cmd.Context()))
			var descriptor *remote.Descriptor
			if image.FromBaseRelease {
				descriptor, err = fetchManifestDescriptor(image.SourceRef, opts)
			} else {
				descriptor, err = fetchBundleSourceManifest(image.SourceRef, opts)
			}
			if err != nil {
				return fmt.Errorf("image %q: %w", image.ID, err)
			}
			result.Images = append(result.Images, inspectedImage{image.ID, image.AppName, image.ContainerName, image.SourceRef, image.TargetRef, descriptor.Digest.String()})
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(result)
	},
}

func init() {
	f := bundleInspectCmd.Flags()
	f.StringVar(&bundlePublishOptions.srcCreds, "src-creds", "", "source credentials user:password")
	f.StringVar(&bundlePublishOptions.destCreds, "dest-creds", "", "destination credentials user:password")
	f.BoolVar(&bundlePublishOptions.srcInsecure, "src-insecure", false, "skip source TLS verification")
	f.BoolVar(&bundlePublishOptions.destInsecure, "dest-insecure", false, "skip destination TLS verification")
	f.StringVar(&bundlePublishOptions.srcProxy, "src-proxy", "", "source proxy URL, or direct")
	f.StringVar(&bundlePublishOptions.destProxy, "dest-proxy", "", "destination proxy URL, or direct")
	f.StringVar(&bundlePublishOptions.baseRelease, "base-release", "", "reuse unselected images from this release")
	f.StringArrayVar(&bundlePublishOptions.selectors, "select", nil, "selected APP/CONTAINER (repeatable)")
	f.StringVar(&bundlePublishOptions.selectFile, "select-file", "", "selection YAML file")
	f.BoolVar(&bundlePublishOptions.preserveBaseOnly, "preserve-base-only", false, "preserve only base and selected images")
	bundlePublishCmd.Flags().StringVar(&bundleDigestPlanPath, "digest-plan", "", "publish exactly the source digests reviewed with bundle inspect")
}

func effectiveBundlePlan(input string) (*bundle.Plan, *bundle.ReleaseManifest, error) {
	b, err := bundle.Load(input)
	if err != nil {
		return nil, nil, err
	}
	selectors := append([]string(nil), bundlePublishOptions.selectors...)
	if strings.TrimSpace(bundlePublishOptions.selectFile) != "" {
		fileSelectors, err := bundle.LoadSelectionFile(bundlePublishOptions.selectFile)
		if err != nil {
			return nil, nil, err
		}
		selectors = append(selectors, fileSelectors...)
	}
	baseReleasePath := strings.TrimSpace(bundlePublishOptions.baseRelease)
	var baseRelease *bundle.ReleaseManifest
	targetRegistry := bundleOptions.targetRegistry
	if baseReleasePath == "" {
		if len(selectors) > 0 {
			return nil, nil, errors.New("--select and --select-file require --base-release")
		}
		if bundlePublishOptions.preserveBaseOnly {
			return nil, nil, errors.New("--preserve-base-only requires --base-release")
		}
	} else {
		baseRelease, err = bundle.LoadRelease(baseReleasePath)
		if err != nil {
			return nil, nil, err
		}
		if strings.TrimSpace(targetRegistry) == "" && strings.TrimSpace(b.Registries.Target.URL) == "" {
			targetRegistry, err = bundle.InferTargetRegistry(b, baseRelease, bundleOptions.targetProject)
			if err != nil {
				return nil, nil, err
			}
		}
	}
	plan, err := resolveBundlePlan(b, targetRegistry)
	if err != nil {
		return nil, nil, err
	}
	if baseRelease != nil {
		plan, err = bundle.ApplyBaseReleaseWithOptions(plan, baseRelease, selectors, bundle.SelectiveOptions{
			PreserveBaseOnly: bundlePublishOptions.preserveBaseOnly,
		})
		if err != nil {
			return nil, nil, err
		}
	}
	return plan, baseRelease, nil
}

func lockBundlePlan(plan *bundle.Plan, filename string) error {
	content, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	var reviewed inspectedBundle
	if err := json.Unmarshal(content, &reviewed); err != nil {
		return fmt.Errorf("invalid digest plan: %w", err)
	}
	if reviewed.ReleaseID != plan.ReleaseID || len(reviewed.Images) != len(plan.Images) {
		return errors.New("digest plan does not match this release; inspect again")
	}
	for i := range plan.Images {
		image, locked := &plan.Images[i], reviewed.Images[i]
		if image.ID != locked.ID || image.AppName != locked.AppName || image.ContainerName != locked.ContainerName || image.SourceRef != locked.SourceRef || image.TargetRef != locked.TargetRef {
			return fmt.Errorf("digest plan does not match image %q; inspect again", image.ID)
		}
		ref := image.SourceImage + "@" + locked.Digest
		if _, err := name.NewDigest(ref); err != nil {
			return fmt.Errorf("invalid reviewed digest: %w", err)
		}
		image.SourceRef = ref
	}
	return nil
}
