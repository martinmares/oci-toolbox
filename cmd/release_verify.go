package cmd

import (
	"context"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/spf13/cobra"

	"github.com/martinmares/oci-toolbox/internal/bundle"
	"github.com/martinmares/oci-toolbox/internal/kubeimages"
	"github.com/martinmares/oci-toolbox/internal/progress"
)

type releaseVerifyOptions struct {
	format       string
	creds        string
	insecure     bool
	namespace    string
	kubeconfig   string
	kubeContext  string
	kubeInsecure bool
	imagePrefix  string
	progress     bool
}

type releaseVerifySummary struct {
	Verified int `json:"verified"`
	Drift    int `json:"drift"`
	Errors   int `json:"errors"`
}

type releaseVerifyItem struct {
	Selector      string                    `json:"selector"`
	Status        string                    `json:"status"`
	ChangedFields []string                  `json:"changed_fields,omitempty"`
	TagChanged    bool                      `json:"tag_changed,omitempty"`
	Expected      *bundle.ReleaseImageState `json:"expected,omitempty"`
	Actual        *bundle.ReleaseImageState `json:"actual,omitempty"`
	Error         string                    `json:"error,omitempty"`
}

type releaseVerifyReport struct {
	Mode      string               `json:"mode"`
	ReleaseID string               `json:"release_id"`
	Manifest  string               `json:"manifest"`
	Namespace string               `json:"namespace,omitempty"`
	Summary   releaseVerifySummary `json:"summary"`
	Images    []releaseVerifyItem  `json:"images"`
}

type resolvedDeploymentImage struct {
	state *bundle.ReleaseImageState
	err   error
}

var verifyOptions releaseVerifyOptions

var releaseVerifyCmd = &cobra.Command{
	Use:   "verify RELEASE_MANIFEST",
	Short: "Verify a release manifest against its registry or a Kubernetes namespace",
	Args:  cobra.ExactArgs(1),
	RunE:  runReleaseVerify,
	Example: `  oci-toolbox release verify release.yml --progress

  oci-toolbox release verify release.yml \
    --namespace example \
    --image-prefix registry.example.com/team \
    --format json`,
}

func init() {
	releaseVerifyCmd.Flags().StringVar(&verifyOptions.format, "format", "table", "output format: table or json")
	releaseVerifyCmd.Flags().StringVar(&verifyOptions.creds, "creds", "", "registry credentials user:password")
	releaseVerifyCmd.Flags().BoolVar(&verifyOptions.insecure, "insecure", false, "skip registry TLS verification")
	releaseVerifyCmd.Flags().StringVarP(&verifyOptions.namespace, "namespace", "n", "", "verify against Deployment containers in this Kubernetes namespace")
	releaseVerifyCmd.Flags().StringVar(&verifyOptions.kubeconfig, "kubeconfig", "", "kubeconfig path; defaults to KUBECONFIG or ~/.kube/config")
	releaseVerifyCmd.Flags().StringVar(&verifyOptions.kubeContext, "context", "", "Kubernetes context override")
	releaseVerifyCmd.Flags().BoolVar(&verifyOptions.kubeInsecure, "kube-insecure", false, "skip Kubernetes API TLS certificate verification")
	releaseVerifyCmd.Flags().StringVar(&verifyOptions.imagePrefix, "image-prefix", "", "include only Kubernetes images under this registry/repository prefix")
	releaseVerifyCmd.Flags().BoolVar(&verifyOptions.progress, "progress", false, "show remote image verification progress and ETA")
	releaseCmd.AddCommand(releaseVerifyCmd)
}

func runReleaseVerify(cmd *cobra.Command, args []string) error {
	format := strings.ToLower(strings.TrimSpace(verifyOptions.format))
	if format != "table" && format != "json" {
		return fmt.Errorf("invalid --format %q: expected table or json", verifyOptions.format)
	}
	manifest, err := bundle.LoadRelease(args[0])
	if err != nil {
		return err
	}
	var report releaseVerifyReport
	if strings.TrimSpace(verifyOptions.namespace) == "" {
		if verifyOptions.kubeconfig != "" || verifyOptions.kubeContext != "" || verifyOptions.kubeInsecure || verifyOptions.imagePrefix != "" {
			return fmt.Errorf("--kubeconfig, --context, --kube-insecure, and --image-prefix require --namespace")
		}
		var counter *progress.Counter
		if verifyOptions.progress {
			counter = progress.NewCounter(len(manifest.Images), "verify registry images", cmd.ErrOrStderr())
			defer func() { _ = counter.Exit() }()
		}
		report, err = verifyReleaseRegistry(cmd.Context(), args[0], manifest, registryOptions(verifyOptions.creds, verifyOptions.insecure), counter)
		if err != nil {
			return err
		}
		if err := counter.Finish(); err != nil {
			return fmt.Errorf("finishing verification progress: %w", err)
		}
		counter = nil
	} else {
		report, err = verifyReleaseKubernetes(cmd.Context(), args[0], manifest, verifyOptions, cmd.ErrOrStderr())
		if err != nil {
			return err
		}
	}
	if format == "json" {
		if err := writeReleaseJSON(cmd, report); err != nil {
			return err
		}
	} else if err := writeReleaseVerifyTable(cmd, report); err != nil {
		return err
	}
	if report.Summary.Drift > 0 || report.Summary.Errors > 0 {
		return fmt.Errorf("release verification failed: %d drift, %d errors", report.Summary.Drift, report.Summary.Errors)
	}
	return nil
}

func verifyReleaseRegistry(ctx context.Context, path string, manifest *bundle.ReleaseManifest, opts []remote.Option, counter *progress.Counter) (releaseVerifyReport, error) {
	report := releaseVerifyReport{
		Mode: "registry", ReleaseID: manifest.ReleaseID, Manifest: path,
		Images: make([]releaseVerifyItem, 0, len(manifest.Images)),
	}
	opts = append(opts, remote.WithContext(ctx))
	for i, image := range manifest.Images {
		expected := releaseState(image)
		item := releaseVerifyItem{Selector: releaseSelector(image), Expected: expected}
		func() {
			counter.Describe(fmt.Sprintf("verify digest %d/%d: %s", i+1, len(manifest.Images), item.Selector))
			desc, inspectErr := fetchManifestDescriptor(image.Image+"@"+image.Digest, opts)
			if inspectErr != nil {
				item.Status, item.Error = "error", inspectErr.Error()
				report.Summary.Errors++
				return
			}
			platform, platformErr := reconstructPlatform(desc)
			if platformErr != nil {
				item.Status, item.Error = "error", fmt.Sprintf("inspect platform: %v", platformErr)
				report.Summary.Errors++
				return
			}
			item.Actual = &bundle.ReleaseImageState{Image: image.Image, Digest: desc.Digest.String(), Platform: platform}
			item.ChangedFields = verifyChangedFields(item.Expected, item.Actual)
			if len(item.ChangedFields) > 0 {
				item.Status = "drift"
				report.Summary.Drift++
				return
			}
			if image.Tag != "" {
				counter.Describe(fmt.Sprintf("verify tag %d/%d: %s", i+1, len(manifest.Images), item.Selector))
				tagDesc, tagErr := fetchManifestDescriptor(image.Image+":"+image.Tag, opts)
				if tagErr != nil {
					item.Status, item.Error = "error", fmt.Sprintf("tag %q: %v", image.Tag, tagErr)
					report.Summary.Errors++
					return
				}
				item.Actual.Tag = image.Tag
				if tagDesc.Digest.String() != image.Digest {
					item.Status = "drift"
					item.ChangedFields = []string{"tag_digest"}
					item.Actual.Digest = tagDesc.Digest.String()
					report.Summary.Drift++
					return
				}
			}
			item.Status = "verified"
			report.Summary.Verified++
		}()
		report.Images = append(report.Images, item)
		if err := counter.Advance(); err != nil {
			return report, fmt.Errorf("updating verification progress: %w", err)
		}
	}
	sortVerifyItems(report.Images)
	return report, nil
}

func verifyReleaseKubernetes(ctx context.Context, path string, manifest *bundle.ReleaseManifest, opts releaseVerifyOptions, progressWriter io.Writer) (releaseVerifyReport, error) {
	client, err := kubeimages.New(kubeimages.Options{Kubeconfig: opts.kubeconfig, Context: opts.kubeContext, Insecure: opts.kubeInsecure})
	if err != nil {
		return releaseVerifyReport{}, err
	}
	images, err := client.ListDeploymentImages(ctx, strings.TrimSpace(opts.namespace))
	if err != nil {
		return releaseVerifyReport{}, fmt.Errorf("listing deployments in namespace %q: %w", opts.namespace, err)
	}
	images = filterDeploymentImages(images, opts.imagePrefix)
	var counter *progress.Counter
	if opts.progress {
		counter = progress.NewCounter(len(images), "verify Kubernetes images", progressWriter)
		defer func() { _ = counter.Exit() }()
	}
	resolved := make(map[string]resolvedDeploymentImage, len(images))
	registryOpts := append(registryOptions(opts.creds, opts.insecure), remote.WithContext(ctx))
	for i, image := range images {
		counter.Describe(fmt.Sprintf("verify deployment %d/%d: %s/%s", i+1, len(images), image.AppName, image.ContainerName))
		key := image.AppName + "\x00" + image.ContainerName
		state, resolveErr := resolveDeploymentImage(image.Image, registryOpts)
		resolved[key] = resolvedDeploymentImage{state: state, err: resolveErr}
		if err := counter.Advance(); err != nil {
			return releaseVerifyReport{}, fmt.Errorf("updating verification progress: %w", err)
		}
	}
	if err := counter.Finish(); err != nil {
		return releaseVerifyReport{}, fmt.Errorf("finishing verification progress: %w", err)
	}
	counter = nil
	report := compareReleaseToDeployments(path, strings.TrimSpace(opts.namespace), manifest, resolved)
	return report, nil
}

func resolveDeploymentImage(value string, opts []remote.Option) (*bundle.ReleaseImageState, error) {
	ref, err := name.ParseReference(stripScheme(value), name.WeakValidation)
	if err != nil {
		return nil, err
	}
	desc, err := fetchManifestDescriptor(ref.String(), opts)
	if err != nil {
		return nil, err
	}
	platform, err := reconstructPlatform(desc)
	if err != nil {
		return nil, err
	}
	tag := ""
	if tagged, ok := ref.(name.Tag); ok {
		tag = tagged.Identifier()
	}
	return &bundle.ReleaseImageState{
		Image: ref.Context().Name(), Tag: tag, Digest: desc.Digest.String(), Platform: platform,
	}, nil
}

func compareReleaseToDeployments(path, namespace string, manifest *bundle.ReleaseManifest, actual map[string]resolvedDeploymentImage) releaseVerifyReport {
	report := releaseVerifyReport{
		Mode: "kubernetes", ReleaseID: manifest.ReleaseID, Manifest: path, Namespace: namespace,
		Images: make([]releaseVerifyItem, 0, len(manifest.Images)+len(actual)),
	}
	expected := make(map[string]bundle.ReleaseImage, len(manifest.Images))
	for _, image := range manifest.Images {
		expected[image.AppName+"\x00"+image.ContainerName] = image
	}
	keys := make([]string, 0, len(expected)+len(actual))
	seen := make(map[string]struct{}, len(expected)+len(actual))
	for key := range expected {
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for key := range actual {
		if _, exists := seen[key]; !exists {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		expectedImage, hasExpected := expected[key]
		actualImage, hasActual := actual[key]
		item := releaseVerifyItem{Selector: strings.Replace(key, "\x00", "/", 1)}
		if hasExpected {
			item.Expected = releaseState(expectedImage)
		}
		if hasActual {
			item.Actual = actualImage.state
		}
		switch {
		case !hasExpected:
			item.Status, item.ChangedFields = "drift", []string{"unexpected_selector"}
			report.Summary.Drift++
		case !hasActual:
			item.Status, item.ChangedFields = "drift", []string{"missing_selector"}
			report.Summary.Drift++
		case actualImage.err != nil:
			item.Status, item.Error = "error", actualImage.err.Error()
			report.Summary.Errors++
		default:
			item.TagChanged = strings.TrimSpace(expectedImage.Tag) != strings.TrimSpace(actualImage.state.Tag)
			item.ChangedFields = verifyChangedFields(item.Expected, item.Actual)
			if len(item.ChangedFields) == 0 {
				item.Status = "verified"
				report.Summary.Verified++
			} else {
				item.Status = "drift"
				report.Summary.Drift++
			}
		}
		report.Images = append(report.Images, item)
	}
	return report
}

func verifyChangedFields(expected, actual *bundle.ReleaseImageState) []string {
	fields := make([]string, 0, 3)
	if expected.Image != actual.Image {
		fields = append(fields, "image")
	}
	if expected.Digest != actual.Digest {
		fields = append(fields, "digest")
	}
	if expected.Platform != "" && expected.Platform != actual.Platform {
		fields = append(fields, "platform")
	}
	return fields
}

func releaseState(image bundle.ReleaseImage) *bundle.ReleaseImageState {
	return &bundle.ReleaseImageState{ID: image.ID, Image: image.Image, Tag: image.Tag, Digest: image.Digest, Platform: image.Platform}
}

func releaseSelector(image bundle.ReleaseImage) string {
	return image.AppName + "/" + image.ContainerName
}

func sortVerifyItems(items []releaseVerifyItem) {
	sort.Slice(items, func(i, j int) bool { return items[i].Selector < items[j].Selector })
}

func writeReleaseVerifyTable(cmd *cobra.Command, report releaseVerifyReport) error {
	w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "STATUS\tSELECTOR\tDETAILS"); err != nil {
		return err
	}
	for _, item := range report.Images {
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\n", item.Status, item.Selector, releaseVerifyDetails(item)); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(w, "\nSUMMARY\tverified=%d drift=%d errors=%d\n", report.Summary.Verified, report.Summary.Drift, report.Summary.Errors); err != nil {
		return err
	}
	return w.Flush()
}

func releaseVerifyDetails(item releaseVerifyItem) string {
	if item.Error != "" {
		return item.Error
	}
	if len(item.ChangedFields) > 0 {
		return strings.Join(item.ChangedFields, ",")
	}
	if item.TagChanged {
		return "tag differs; digest matches"
	}
	return ""
}
