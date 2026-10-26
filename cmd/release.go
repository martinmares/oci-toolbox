package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/martinmares/oci-toolbox/internal/bundle"
	"github.com/martinmares/oci-toolbox/internal/kubeimages"
	"github.com/martinmares/oci-toolbox/internal/progress"
)

type releaseReconstructOptions struct {
	file                string
	output              string
	releaseID           string
	creds               string
	insecure            bool
	progress            bool
	discoverTags        bool
	extraTags           []string
	namespace           string
	kubeconfig          string
	kubeContext         string
	kubeInsecure        bool
	imagePrefix         string
	excludeScaledToZero bool
}

var reconstructOptions releaseReconstructOptions

type releaseSelectorsOptions struct {
	format string
}

type releaseCompareOptions struct {
	mode         string
	format       string
	color        string
	changedOnly  bool
	failOnChange bool
}

type releaseToBundleOptions struct {
	name       string
	output     string
	sourceMode string
}

var selectorsOptions releaseSelectorsOptions
var compareOptions releaseCompareOptions
var toBundleOptions releaseToBundleOptions
var releaseConvertOutput string

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
    --namespace example \
    --image-prefix registry.example.com/team \
    --exclude-scaled-to-zero \
    --release-id RE_2026.07.07.01 \
    --output release.yml \
    --discover-tags \
    --progress

  oci-toolbox release reconstruct \
    registry.example.com/team/api:RE_2026.07.07.01 \
    registry.example.com/team/ui:RE_2026.07.07.01 \
    --release-id RE_2026.07.07.01 \
    --output release.yml \
    --discover-tags \
    --progress

  oci-toolbox release reconstruct --file release-images.yml --output release.yml`,
}

var releaseSelectorsCmd = &cobra.Command{
	Use:   "selectors RELEASE_MANIFEST",
	Short: "List deployment selectors from a release manifest",
	Args:  cobra.ExactArgs(1),
	RunE:  runReleaseSelectors,
	Example: `  oci-toolbox release selectors release.yml
  oci-toolbox release selectors release.yml --format plain
  oci-toolbox release selectors release.yml --format json`,
}

var releaseCompareCmd = &cobra.Command{
	Use:   "compare RELEASE_A RELEASE_B",
	Short: "Compare two release manifests by deployment selector",
	Args:  cobra.ExactArgs(2),
	RunE:  runReleaseCompare,
	Example: `  oci-toolbox release compare previous.yml current.yml
  oci-toolbox release compare previous.yml current.yml --mode digest --format summary
  oci-toolbox release compare previous.yml current.yml --mode image --format json
  oci-toolbox release compare previous.yml current.yml --mode full --changed-only --fail-on-change`,
}

var releaseToBundleCmd = &cobra.Command{
	Use:   "to-bundle RELEASE_MANIFEST",
	Short: "Convert a release manifest into an image bundle",
	Args:  cobra.ExactArgs(1),
	RunE:  runReleaseToBundle,
	Example: `  oci-toolbox release to-bundle releases/2026.08.10.01.yml \
    --name tsm-sda-2.4 \
    --output bundles/tsm-sda-2.4.yml

  oci-toolbox release to-bundle releases/2026.08.10.01.yml \
    --name tsm-sda-2.4 \
    --source-mode tag \
    --output bundles/tsm-sda-2.4-tags.yml`,
}

var releaseConvertCmd = &cobra.Command{
	Use:   "convert RELEASE_MANIFEST",
	Short: "Convert native ImageRelease v1/v2 to opt-in v2 (offline; not a product importer)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		manifest, err := bundle.LoadRelease(args[0])
		if err != nil {
			return err
		}
		return writeEncodedReleaseManifest(cmd, releaseConvertOutput, manifest, bundle.EncodeReleaseV2)
	},
	Example: "  oci-toolbox release convert releases/old.yml --output releases/converted.yml",
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
	releaseReconstructCmd.Flags().StringVarP(&reconstructOptions.namespace, "namespace", "n", "", "reconstruct deployment selectors from a Kubernetes namespace")
	releaseReconstructCmd.Flags().StringVar(&reconstructOptions.kubeconfig, "kubeconfig", "", "kubeconfig path; defaults to KUBECONFIG or ~/.kube/config")
	releaseReconstructCmd.Flags().StringVar(&reconstructOptions.kubeContext, "context", "", "Kubernetes context override")
	releaseReconstructCmd.Flags().BoolVar(&reconstructOptions.kubeInsecure, "kube-insecure", false, "skip Kubernetes API TLS certificate verification")
	releaseReconstructCmd.Flags().StringVar(&reconstructOptions.imagePrefix, "image-prefix", "", "include only Kubernetes images under this registry/repository prefix")
	releaseReconstructCmd.Flags().BoolVar(&reconstructOptions.excludeScaledToZero, "exclude-scaled-to-zero", false, "exclude Deployments with spec.replicas=0 (requires --namespace)")
	releaseSelectorsCmd.Flags().StringVar(&selectorsOptions.format, "format", "table", "output format: table, plain, or json")
	releaseCompareCmd.Flags().StringVar(&compareOptions.mode, "mode", "digest", "comparison mode: digest, image, or full")
	releaseCompareCmd.Flags().StringVar(&compareOptions.format, "format", "table", "output format: table, summary, or json")
	releaseCompareCmd.Flags().StringVar(&compareOptions.color, "color", "auto", "colorize table and summary output: auto, always, or never")
	releaseCompareCmd.Flags().BoolVar(&compareOptions.changedOnly, "changed-only", false, "omit unchanged deployment selectors")
	releaseCompareCmd.Flags().BoolVar(&compareOptions.failOnChange, "fail-on-change", false, "return a non-zero exit status when manifests differ")
	releaseToBundleCmd.Flags().StringVar(&toBundleOptions.name, "name", "", "name stored in the generated bundle (required)")
	releaseToBundleCmd.Flags().StringVarP(&toBundleOptions.output, "output", "o", "-", "bundle YAML output path, or - for stdout")
	releaseToBundleCmd.Flags().StringVar(&toBundleOptions.sourceMode, "source-mode", "digest", "source reference mode: digest (exact and immutable) or tag (reusable but mutable)")
	releaseConvertCmd.Flags().StringVarP(&releaseConvertOutput, "output", "o", "-", "v2 release manifest output path, or - for stdout; existing files are never overwritten")
	releaseCmd.AddCommand(releaseReconstructCmd, releaseSelectorsCmd, releaseCompareCmd, releaseToBundleCmd, releaseConvertCmd)
	rootCmd.AddCommand(releaseCmd)
}

func runReleaseToBundle(cmd *cobra.Command, args []string) error {
	if strings.TrimSpace(toBundleOptions.name) == "" {
		return errors.New("--name is required")
	}
	mode := bundle.BundleSourceMode(strings.ToLower(strings.TrimSpace(toBundleOptions.sourceMode)))
	if mode != bundle.BundleSourceDigest && mode != bundle.BundleSourceTag {
		return fmt.Errorf("invalid --source-mode %q: expected digest or tag", toBundleOptions.sourceMode)
	}
	manifest, err := bundle.LoadRelease(args[0])
	if err != nil {
		return err
	}
	converted, err := bundle.BundleFromRelease(manifest, toBundleOptions.name, mode)
	if err != nil {
		return fmt.Errorf("converting release manifest %q: %w", args[0], err)
	}
	if err := writeBundleYAML(cmd, toBundleOptions.output, converted); err != nil {
		return err
	}
	if toBundleOptions.output != "-" {
		fmt.Fprintf(cmd.ErrOrStderr(), "bundle written to %s\n", toBundleOptions.output)
	}
	return nil
}

func writeBundleYAML(cmd *cobra.Command, output string, value *bundle.Bundle) error {
	if output == "-" {
		return bundle.Encode(cmd.OutOrStdout(), value)
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
	if err := bundle.Encode(tmp, value); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, output)
}

func runReleaseSelectors(cmd *cobra.Command, args []string) error {
	format := strings.ToLower(strings.TrimSpace(selectorsOptions.format))
	if format != "table" && format != "plain" && format != "json" {
		return fmt.Errorf("invalid --format %q: expected table, plain, or json", selectorsOptions.format)
	}
	manifest, err := bundle.LoadRelease(args[0])
	if err != nil {
		return err
	}
	selectors := bundle.ReleaseSelectors(manifest)
	switch format {
	case "plain":
		for _, item := range selectors {
			if _, err := fmt.Fprintln(cmd.OutOrStdout(), item.Selector); err != nil {
				return err
			}
		}
		return nil
	case "json":
		report := struct {
			ReleaseID string                   `json:"release_id"`
			Selectors []bundle.ReleaseSelector `json:"selectors"`
		}{ReleaseID: manifest.ReleaseID, Selectors: selectors}
		return writeReleaseJSON(cmd, report)
	default:
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
		if _, err := fmt.Fprintln(w, "SELECTOR\tID\tIMAGE\tTAG\tDIGEST\tPLATFORM"); err != nil {
			return err
		}
		for _, item := range selectors {
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", item.Selector, item.ID, item.Image, item.Tag, item.Digest, item.Platform); err != nil {
				return err
			}
		}
		return w.Flush()
	}
}

func runReleaseCompare(cmd *cobra.Command, args []string) error {
	format := strings.ToLower(strings.TrimSpace(compareOptions.format))
	if format != "table" && format != "summary" && format != "json" {
		return fmt.Errorf("invalid --format %q: expected table, summary, or json", compareOptions.format)
	}
	mode, err := releaseComparisonMode(compareOptions.mode)
	if err != nil {
		return err
	}
	color, err := releaseComparisonColor(cmd, format, compareOptions.color)
	if err != nil {
		return err
	}
	a, err := bundle.LoadRelease(args[0])
	if err != nil {
		return err
	}
	b, err := bundle.LoadRelease(args[1])
	if err != nil {
		return err
	}
	comparison := bundle.CompareReleasesWithMode(a, b, mode)
	images := comparison.Images
	if compareOptions.changedOnly {
		images = changedReleaseImages(images)
	}

	if format == "json" {
		report := struct {
			Mode      string `json:"mode"`
			MatchedBy string `json:"matched_by"`
			Match     bool   `json:"match"`
			ReleaseA  struct {
				Path      string    `json:"path"`
				ReleaseID string    `json:"release_id"`
				CreatedAt time.Time `json:"created_at"`
			} `json:"release_a"`
			ReleaseB struct {
				Path      string    `json:"path"`
				ReleaseID string    `json:"release_id"`
				CreatedAt time.Time `json:"created_at"`
			} `json:"release_b"`
			Summary bundle.ReleaseDiffSummary `json:"summary"`
			Images  []bundle.ReleaseDiff      `json:"images"`
		}{}
		report.Mode = string(mode)
		report.MatchedBy = "app_name/container_name"
		report.Match = !comparison.HasChanges()
		report.ReleaseA.Path, report.ReleaseA.ReleaseID, report.ReleaseA.CreatedAt = args[0], a.ReleaseID, a.CreatedAt
		report.ReleaseB.Path, report.ReleaseB.ReleaseID, report.ReleaseB.CreatedAt = args[1], b.ReleaseID, b.CreatedAt
		report.Summary, report.Images = comparison.Summary, images
		if err := writeReleaseJSON(cmd, report); err != nil {
			return err
		}
	} else if format == "summary" {
		if err := writeReleaseComparisonSummary(cmd, comparison, color); err != nil {
			return err
		}
	} else if err := writeReleaseComparisonTable(cmd, images, comparison.Summary, mode, color); err != nil {
		return err
	}
	if compareOptions.failOnChange && comparison.HasChanges() {
		return fmt.Errorf(
			"release manifests differ: %d changed, %d added, %d removed",
			comparison.Summary.Changed, comparison.Summary.Added, comparison.Summary.Removed,
		)
	}
	return nil
}

func releaseComparisonColor(cmd *cobra.Command, format, value string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "always":
		return format != "json", nil
	case "never":
		return false, nil
	case "auto":
		if format == "json" || os.Getenv("NO_COLOR") != "" {
			return false, nil
		}
		return isTTYWriter(cmd.OutOrStdout()), nil
	default:
		return false, fmt.Errorf("invalid --color %q: expected auto, always, or never", value)
	}
}

func releaseComparisonMode(value string) (bundle.ReleaseCompareMode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "digest":
		return bundle.ReleaseCompareDigest, nil
	case "image":
		return bundle.ReleaseCompareImage, nil
	case "full":
		return bundle.ReleaseCompareFull, nil
	default:
		return "", fmt.Errorf("invalid --mode %q: expected digest, image, or full", value)
	}
}

func writeReleaseJSON(cmd *cobra.Command, value any) error {
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(value)
}

func changedReleaseImages(images []bundle.ReleaseDiff) []bundle.ReleaseDiff {
	filtered := make([]bundle.ReleaseDiff, 0, len(images))
	for _, item := range images {
		if item.Status != "same" {
			filtered = append(filtered, item)
		}
	}
	return filtered
}

func writeReleaseComparisonTable(cmd *cobra.Command, images []bundle.ReleaseDiff, summary bundle.ReleaseDiffSummary, mode bundle.ReleaseCompareMode, color bool) error {
	var output bytes.Buffer
	w := tabwriter.NewWriter(&output, 0, 4, 2, ' ', 0)
	if mode == bundle.ReleaseCompareDigest {
		if _, err := fmt.Fprintln(w, "STATUS\tSELECTOR\tDIGEST A\tDIGEST B"); err != nil {
			return err
		}
		for _, item := range images {
			var digestA, digestB string
			if item.A != nil {
				digestA = item.A.Digest
			}
			if item.B != nil {
				digestB = item.B.Digest
			}
			if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", item.Status, item.Selector, digestA, digestB); err != nil {
				return err
			}
		}
		if err := writeReleaseComparisonSummaryTo(w, summary); err != nil {
			return err
		}
		if err := w.Flush(); err != nil {
			return err
		}
		return writeReleaseComparisonTableOutput(cmd, output.String(), color)
	}
	if _, err := fmt.Fprintln(w, "STATUS\tSELECTOR\tCHANGED\tIMAGE A\tTAG A\tDIGEST A\tIMAGE B\tTAG B\tDIGEST B"); err != nil {
		return err
	}
	for _, item := range images {
		var imageA, tagA, digestA, imageB, tagB, digestB string
		if item.A != nil {
			imageA, tagA, digestA = item.A.Image, item.A.Tag, item.A.Digest
		}
		if item.B != nil {
			imageB, tagB, digestB = item.B.Image, item.B.Tag, item.B.Digest
		}
		if _, err := fmt.Fprintf(
			w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			item.Status, item.Selector, strings.Join(item.ChangedFields, ","), imageA, tagA, digestA, imageB, tagB, digestB,
		); err != nil {
			return err
		}
	}
	if err := writeReleaseComparisonSummaryTo(w, summary); err != nil {
		return err
	}
	if err := w.Flush(); err != nil {
		return err
	}
	return writeReleaseComparisonTableOutput(cmd, output.String(), color)
}

func writeReleaseComparisonTableOutput(cmd *cobra.Command, output string, color bool) error {
	if color {
		output = colorizeReleaseComparisonTable(output)
	}
	_, err := fmt.Fprint(cmd.OutOrStdout(), output)
	return err
}

func colorizeReleaseComparisonTable(output string) string {
	lines := strings.SplitAfter(output, "\n")
	for i, line := range lines {
		for status, code := range map[string]string{
			"added": ansiGreen, "removed": ansiRed, "changed": ansiYellow, "same": ansiDim,
		} {
			if strings.HasPrefix(line, status+" ") {
				lines[i] = code + status + ansiReset + line[len(status):]
				break
			}
		}
	}
	return strings.Join(lines, "")
}

func writeReleaseComparisonSummary(cmd *cobra.Command, comparison bundle.ReleaseComparison, color bool) error {
	result := "MATCH"
	code := ansiGreen
	if comparison.HasChanges() {
		result = "DIFFERENT"
		code = ansiRed
	}
	if color {
		result = code + result + ansiReset
	}
	_, err := fmt.Fprintf(
		cmd.OutOrStdout(), "%s: same=%d changed=%d added=%d removed=%d\n",
		result, comparison.Summary.Same, comparison.Summary.Changed, comparison.Summary.Added, comparison.Summary.Removed,
	)
	return err
}

func writeReleaseComparisonSummaryTo(w *tabwriter.Writer, summary bundle.ReleaseDiffSummary) error {
	_, err := fmt.Fprintf(
		w, "\nSUMMARY\tsame=%d changed=%d added=%d removed=%d\n",
		summary.Same, summary.Changed, summary.Added, summary.Removed,
	)
	return err
}

func runReleaseReconstruct(cmd *cobra.Command, args []string) error {
	if strings.TrimSpace(reconstructOptions.output) == "" {
		return errors.New("--output is required")
	}
	if err := ensureReleaseOutputAvailable(reconstructOptions.output); err != nil {
		return err
	}
	items, err := loadReconstructItems(cmd.Context(), args, reconstructOptions)
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

func loadReconstructItems(
	ctx context.Context,
	args []string,
	opts releaseReconstructOptions,
) ([]bundle.ReconstructImage, error) {
	namespace := strings.TrimSpace(opts.namespace)
	if namespace == "" {
		if opts.kubeconfig != "" || opts.kubeContext != "" || opts.kubeInsecure || strings.TrimSpace(opts.imagePrefix) != "" || opts.excludeScaledToZero {
			return nil, errors.New("--kubeconfig, --context, --kube-insecure, --image-prefix, and --exclude-scaled-to-zero require --namespace")
		}
		return reconstructItems(args, opts.file)
	}
	if strings.TrimSpace(opts.file) != "" || len(args) > 0 {
		return nil, errors.New("--namespace cannot be combined with --file or positional IMAGE arguments")
	}
	client, err := kubeimages.New(kubeimages.Options{
		Kubeconfig: opts.kubeconfig,
		Context:    opts.kubeContext,
		Insecure:   opts.kubeInsecure,
	})
	if err != nil {
		return nil, err
	}
	images, err := client.ListDeploymentImages(ctx, namespace)
	if err != nil {
		return nil, fmt.Errorf("listing deployments in namespace %q: %w", namespace, err)
	}
	if len(images) == 0 {
		return nil, fmt.Errorf("namespace %q contains no deployment container images", namespace)
	}
	if opts.excludeScaledToZero {
		images = excludeScaledToZeroDeploymentImages(images)
		if len(images) == 0 {
			return nil, fmt.Errorf("namespace %q contains no deployment container images after excluding Deployments with spec.replicas=0", namespace)
		}
	}
	if prefix := normalizeImagePrefix(opts.imagePrefix); prefix != "" {
		images = filterDeploymentImages(images, prefix)
		if len(images) == 0 {
			return nil, fmt.Errorf(
				"namespace %q contains no deployment container images matching --image-prefix %q",
				namespace,
				prefix,
			)
		}
	}
	return reconstructDeploymentItems(images), nil
}

func excludeScaledToZeroDeploymentImages(images []kubeimages.DeploymentImage) []kubeimages.DeploymentImage {
	filtered := make([]kubeimages.DeploymentImage, 0, len(images))
	for _, image := range images {
		if !image.ScaledToZero {
			filtered = append(filtered, image)
		}
	}
	return filtered
}

func normalizeImagePrefix(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimPrefix(value, "http://")
	return strings.TrimRight(stripScheme(value), "/")
}

func filterDeploymentImages(
	images []kubeimages.DeploymentImage,
	prefix string,
) []kubeimages.DeploymentImage {
	filtered := make([]kubeimages.DeploymentImage, 0, len(images))
	for _, image := range images {
		ref := stripScheme(strings.TrimSpace(image.Image))
		if ref == prefix ||
			strings.HasPrefix(ref, prefix+"/") ||
			strings.HasPrefix(ref, prefix+":") ||
			strings.HasPrefix(ref, prefix+"@") {
			filtered = append(filtered, image)
		}
	}
	return filtered
}

func reconstructDeploymentItems(images []kubeimages.DeploymentImage) []bundle.ReconstructImage {
	items := make([]bundle.ReconstructImage, 0, len(images))
	for _, image := range images {
		id := image.AppName
		if image.ContainerName != image.AppName {
			id += "/" + image.ContainerName
		}
		items = append(items, bundle.ReconstructImage{
			Ref:           image.Image,
			ID:            id,
			AppName:       image.AppName,
			ContainerName: image.ContainerName,
		})
	}
	return items
}
