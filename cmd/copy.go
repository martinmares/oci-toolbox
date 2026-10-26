package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/daemon"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/remote/transport"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/martinmares/oci-toolbox/internal/auth"
	"github.com/martinmares/oci-toolbox/internal/bundle"
	"github.com/martinmares/oci-toolbox/internal/image"
	"github.com/martinmares/oci-toolbox/internal/logging"
	"github.com/martinmares/oci-toolbox/internal/ociarchive"
	"github.com/martinmares/oci-toolbox/internal/progress"
	"github.com/martinmares/oci-toolbox/internal/recipe"
)

var (
	recipePaths      []string
	srcCreds         string
	destCreds        string
	debugMode        bool
	destNoReuse      bool
	showProgress     bool
	showProgressJSON bool
	reducelayers     bool
	reduceChanges    bool
	batchFile        string
	releaseManifest  string
	srcBase          string
	destBase         string
	legacySrcPrefix  string
	legacyDestPrefix string
	copyOutput       string
	copyReleaseID    string
	continueOnError  bool
	wrapEntrypoint   bool
	noWrapEntrypoint bool
	srcInsecure      bool
	destInsecure     bool
	srcProxy         string
	destProxy        string
	destTags         []string
	retryCount       int
	retryInitial     time.Duration
	retryMax         time.Duration
)

type copyRunOptions struct {
	RecipePaths      []string
	SrcCreds         string
	DestCreds        string
	Debug            bool
	DestNoReuse      bool
	Progress         bool
	ProgressJSON     bool
	Reduce           bool
	ReduceChanges    bool
	WrapEntrypoint   bool
	NoWrapEntrypoint bool
	SrcInsecure      bool
	DestInsecure     bool
	SrcProxy         string
	DestProxy        string
	ExpectedDigest   string
}

type copyRetryPolicy struct {
	Retries      int
	InitialDelay time.Duration
	MaxDelay     time.Duration
}

func currentCopyRetryPolicy() copyRetryPolicy {
	return copyRetryPolicy{
		Retries:      retryCount,
		InitialDelay: retryInitial,
		MaxDelay:     retryMax,
	}
}

func currentCopyRunOptions() copyRunOptions {
	return copyRunOptions{
		RecipePaths:      append([]string(nil), recipePaths...),
		SrcCreds:         srcCreds,
		DestCreds:        destCreds,
		Debug:            debugMode,
		DestNoReuse:      destNoReuse,
		Progress:         showProgress,
		ProgressJSON:     showProgressJSON,
		Reduce:           reducelayers,
		ReduceChanges:    reduceChanges,
		WrapEntrypoint:   wrapEntrypoint,
		NoWrapEntrypoint: noWrapEntrypoint,
		SrcInsecure:      srcInsecure,
		DestInsecure:     destInsecure,
		SrcProxy:         srcProxy,
		DestProxy:        destProxy,
	}
}

var copyCmd = &cobra.Command{
	Use:   "copy SOURCE DESTINATION | copy --batch-file FILE | copy --release-manifest FILE --dest-base BASE",
	Short: "Copy and patch an OCI image between registries or Docker archives",
	Args:  copyArgs,
	Example: `  oci-toolbox copy docker://registry.example.com/app:latest docker://registry.example.com/app:patched
  oci-toolbox copy file://app.tgz docker://registry.example.com/app:restored
  oci-toolbox copy --src-creds user:pass --dest-creds user:pass docker://src/repo:tag docker://dst/repo:tag
  oci-toolbox copy --progress --recipe inject-tool.yml docker://src/repo:tag docker://dst/repo:tag
  oci-toolbox copy --src-proxy http://proxy.example.com:8080 --dest-proxy direct docker://external/repo:tag docker://internal/repo:tag
  oci-toolbox copy --batch-file images.yml --release-id RE_2026.08.10.01 --output release.yml --src-insecure --dest-insecure
  oci-toolbox copy --release-manifest release.yml --src-base registry.example.com/team --dest-base mirror.example.com/team --release-id RE_2026.09.10.02 --output mirrored-release.yml`,
	RunE: runCopy,
}

func init() {
	copyCmd.Flags().StringArrayVar(&recipePaths, "recipe", nil, "recipe YAML file (may be specified multiple times)")
	copyCmd.Flags().StringVar(&srcCreds, "src-creds", "", "source registry credentials user:password")
	copyCmd.Flags().StringVar(&destCreds, "dest-creds", "", "destination registry credentials user:password")
	copyCmd.Flags().BoolVar(&debugMode, "debug", false, "enable verbose debug output")
	copyCmd.Flags().BoolVar(&destNoReuse, "dest-no-reuse", false, "disable blob reuse at destination (fallback for some registries)")
	copyCmd.Flags().BoolVar(&showProgress, "progress", false, "show progress bars for pull and push")
	copyCmd.Flags().BoolVar(&showProgressJSON, "progress-json", false, "emit machine-readable progress events as JSONL")
	copyCmd.Flags().BoolVar(&reducelayers, "reduce", false, "squash all layers into one before pushing (fewer layers, size may increase)")
	copyCmd.Flags().BoolVar(&reduceChanges, "reduce-changes", false, "preserve base layers and reduce recipe changes into one delta layer")
	copyCmd.Flags().StringVar(&batchFile, "batch-file", "", "YAML file with copy jobs to run sequentially")
	copyCmd.Flags().StringVar(&releaseManifest, "release-manifest", "", "release manifest whose digest-locked images should be copied")
	copyCmd.Flags().StringVar(&srcBase, "src-base", "", "source registry/repository base to replace; defaults to release manifest registry_base (v2: repository_prefix)")
	copyCmd.Flags().StringVar(&destBase, "dest-base", "", "destination registry/repository base for release manifest copies")
	copyCmd.Flags().StringVar(&legacySrcPrefix, "src-prefix", "", "renamed to --src-base")
	copyCmd.Flags().StringVar(&legacyDestPrefix, "dest-prefix", "", "renamed to --dest-base")
	_ = copyCmd.Flags().MarkHidden("src-prefix")
	_ = copyCmd.Flags().MarkHidden("dest-prefix")
	copyCmd.Flags().StringVarP(&copyOutput, "output", "o", "", "write a release manifest for copied destination images")
	copyCmd.Flags().StringVar(&copyReleaseID, "release-id", "", "release identifier for batch output or destination tags in release-manifest mode")
	copyCmd.Flags().BoolVar(&continueOnError, "continue-on-error", false, "continue batch processing after a copy fails")
	copyCmd.Flags().BoolVar(&wrapEntrypoint, "wrap-entrypoint", false, "wrap SOURCE entrypoint and command for all shadow recipe steps")
	copyCmd.Flags().BoolVar(&noWrapEntrypoint, "no-wrap-entrypoint", false, "do not wrap the original entrypoint for any shadow recipe step")
	copyCmd.Flags().BoolVar(&srcInsecure, "src-insecure", false, "skip TLS certificate verification for source registry")
	copyCmd.Flags().BoolVar(&destInsecure, "dest-insecure", false, "skip TLS certificate verification for destination registry")
	copyCmd.Flags().StringVar(&srcProxy, "src-proxy", "", "source registry proxy URL, or direct to bypass environment proxies")
	copyCmd.Flags().StringVar(&destProxy, "dest-proxy", "", "destination registry proxy URL, or direct to bypass environment proxies")
	copyCmd.Flags().StringArrayVar(&destTags, "dest-tag", nil, "additional destination tag in the same repository (may be specified multiple times)")
	copyCmd.Flags().IntVar(&retryCount, "retries", 4, "additional attempts for transient network failures; 0 disables retries")
	copyCmd.Flags().DurationVar(&retryInitial, "retry-initial-delay", 2*time.Second, "delay before the first retry")
	copyCmd.Flags().DurationVar(&retryMax, "retry-max-delay", 30*time.Second, "maximum delay between retries")

	// Legacy --from/--to flags (hidden, for oci-toolbox native style)
	copyCmd.Flags().StringVar(&fromRef, "from", "", "source image reference")
	copyCmd.Flags().StringVar(&toRef, "to", "", "destination image reference")
	_ = copyCmd.Flags().MarkHidden("from")
	_ = copyCmd.Flags().MarkHidden("to")

	rootCmd.AddCommand(copyCmd)
}

var (
	fromRef string
	toRef   string
)

func copyArgs(_ *cobra.Command, args []string) error {
	if legacySrcPrefix != "" && legacyDestPrefix != "" {
		return errors.New("--src-prefix and --dest-prefix were renamed to --src-base and --dest-base; update the command")
	}
	if legacySrcPrefix != "" {
		return errors.New("--src-prefix was renamed to --src-base; update the command")
	}
	if legacyDestPrefix != "" {
		return errors.New("--dest-prefix was renamed to --dest-base; update the command")
	}
	if batchFile != "" && releaseManifest != "" {
		return fmt.Errorf("--batch-file and --release-manifest cannot be used together")
	}
	if releaseManifest == "" && (srcBase != "" || destBase != "") {
		return fmt.Errorf("--src-base and --dest-base require --release-manifest")
	}
	if copyOutput != "" && batchFile == "" && releaseManifest == "" {
		return fmt.Errorf("--output requires --batch-file or --release-manifest")
	}
	if copyReleaseID != "" && batchFile == "" && releaseManifest == "" {
		return fmt.Errorf("--release-id requires --batch-file or --release-manifest")
	}
	if copyReleaseID != "" && batchFile != "" && copyOutput == "" {
		return fmt.Errorf("--release-id requires --output")
	}
	if batchFile != "" {
		if len(args) != 0 {
			return fmt.Errorf("--batch-file cannot be combined with positional SOURCE DESTINATION")
		}
		if fromRef != "" || toRef != "" {
			return fmt.Errorf("--batch-file cannot be combined with --from/--to")
		}
		return nil
	}
	if releaseManifest != "" {
		if len(args) != 0 {
			return fmt.Errorf("--release-manifest cannot be combined with positional SOURCE DESTINATION")
		}
		if fromRef != "" || toRef != "" {
			return fmt.Errorf("--release-manifest cannot be combined with --from/--to")
		}
		return nil
	}
	if fromRef != "" || toRef != "" {
		if len(args) != 0 {
			return fmt.Errorf("--from/--to cannot be combined with positional SOURCE DESTINATION")
		}
		return nil
	}
	if len(args) != 2 {
		return fmt.Errorf("accepts 2 arg(s), received %d", len(args))
	}
	return nil
}

func runCopy(cmd *cobra.Command, args []string) error {
	src := fromRef
	dst := toRef
	if len(args) == 2 {
		src = args[0]
		dst = args[1]
	}

	if showProgress && showProgressJSON {
		return fmt.Errorf("--progress and --progress-json cannot be used together")
	}
	if reducelayers && reduceChanges {
		return fmt.Errorf("--reduce and --reduce-changes cannot be used together")
	}
	if err := validateCopyRetryPolicy(currentCopyRetryPolicy()); err != nil {
		return err
	}
	if err := validateShadowEntrypointFlags(wrapEntrypoint, noWrapEntrypoint); err != nil {
		return err
	}

	if debugMode && !showProgress {
		logging.Setup(slog.LevelDebug)
	} else {
		logging.Setup(slog.LevelInfo)
	}

	if batchFile != "" {
		if len(destTags) > 0 {
			return fmt.Errorf("--dest-tag cannot be combined with --batch-file")
		}
		batch, err := loadCopyBatch(batchFile)
		if err != nil {
			return err
		}
		var manifest *bundle.ReleaseManifest
		if copyOutput != "" {
			if err := ensureReleaseOutputAvailable(copyOutput); err != nil {
				return err
			}
			manifest, err = prepareBatchReleaseManifest(batch, copyReleaseID)
			if err != nil {
				return err
			}
		}
		if err := runCopyBatch(cmd.Context(), batch); err != nil {
			return err
		}
		if manifest != nil {
			if err := resolveBatchReleaseManifest(cmd, manifest, batch); err != nil {
				return err
			}
			if err := writeReleaseManifest(cmd, copyOutput, manifest); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "release manifest written to %s\n", copyOutput)
		}
		return nil
	}
	if releaseManifest != "" {
		if len(destTags) > 0 {
			return fmt.Errorf("--dest-tag cannot be combined with --release-manifest")
		}
		if len(recipePaths) > 0 || reducelayers || reduceChanges || wrapEntrypoint || noWrapEntrypoint {
			return fmt.Errorf("recipes, layer reduction, and entrypoint wrapping cannot be combined with --release-manifest")
		}
		if destNoReuse {
			return fmt.Errorf("--dest-no-reuse cannot be combined with --release-manifest")
		}
		if copyOutput != "" {
			if err := ensureReleaseOutputAvailable(copyOutput); err != nil {
				return err
			}
		}
		plan, err := loadReleaseCopyPlan(releaseManifest, srcBase, destBase, copyReleaseID)
		if err != nil {
			return err
		}
		if err := runCopyBatch(cmd.Context(), plan.Items); err != nil {
			return err
		}
		if copyOutput != "" {
			if err := writeReleaseManifest(cmd, copyOutput, plan.Manifest); err != nil {
				return err
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "release manifest written to %s\n", copyOutput)
		}
		return nil
	}

	if src == "" || dst == "" {
		return fmt.Errorf("source and destination are required (provide as positional arguments or --from/--to)")
	}

	return runCopyWithRetry(
		cmd.Context(),
		src,
		dst,
		"",
		destTags,
		currentCopyRunOptions(),
		currentCopyRetryPolicy(),
	)
}

type copyBatch struct {
	Copy []copyBatchItem `yaml:"copy"`
}

type copyBatchItem struct {
	Src            string   `yaml:"src,omitempty"`
	Source         string   `yaml:"source,omitempty"`
	Dest           string   `yaml:"dest,omitempty"`
	Destination    string   `yaml:"destination,omitempty"`
	DestTags       []string `yaml:"dest_tags,omitempty"`
	ID             string   `yaml:"id,omitempty"`
	AppName        string   `yaml:"app_name,omitempty"`
	ContainerName  string   `yaml:"container_name,omitempty"`
	ExpectedDigest string   `yaml:"-"`
}

type releaseCopyPlan struct {
	Items    []copyBatchItem
	Manifest *bundle.ReleaseManifest
}

type copyBatchFailure struct {
	Index int
	Item  copyBatchItem
	Err   error
}

type copyBatchError struct {
	Failures []copyBatchFailure
}

func (e copyBatchError) Error() string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("%d batch copy job(s) failed:", len(e.Failures)))
	for _, failure := range e.Failures {
		sb.WriteString(fmt.Sprintf("\n  #%d %s -> %s: %v", failure.Index, failure.Item.source(), failure.Item.target(), failure.Err))
	}
	return sb.String()
}

func loadCopyBatch(path string) ([]copyBatchItem, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading batch file %q: %w", path, err)
	}

	var batch copyBatch
	if err := yaml.Unmarshal(data, &batch); err != nil {
		return nil, fmt.Errorf("parsing batch file %q: %w", path, err)
	}
	if len(batch.Copy) == 0 {
		return nil, fmt.Errorf("batch file %q has no copy entries", path)
	}

	seenDest := map[string]int{}
	for i := range batch.Copy {
		item := &batch.Copy[i]
		item.Src = strings.TrimSpace(item.Src)
		item.Source = strings.TrimSpace(item.Source)
		item.Dest = strings.TrimSpace(item.Dest)
		item.Destination = strings.TrimSpace(item.Destination)
		if item.source() == "" {
			return nil, fmt.Errorf("batch file %q copy[%d] missing src", path, i)
		}
		if item.Src != "" && item.Source != "" && item.Src != item.Source {
			return nil, fmt.Errorf("batch file %q copy[%d] has both src and source with different values", path, i)
		}
		if item.target() == "" {
			return nil, fmt.Errorf("batch file %q copy[%d] missing dest", path, i)
		}
		if item.Dest != "" && item.Destination != "" && item.Dest != item.Destination {
			return nil, fmt.Errorf("batch file %q copy[%d] has both dest and destination with different values", path, i)
		}
		destinations, err := item.destinations()
		if err != nil {
			return nil, fmt.Errorf("batch file %q copy[%d]: %w", path, i, err)
		}
		for _, destination := range destinations {
			if prev, ok := seenDest[destination]; ok {
				return nil, fmt.Errorf("batch file %q copy[%d] duplicates destination from copy[%d]: %s", path, i, prev, destination)
			}
			seenDest[destination] = i
		}
	}

	return batch.Copy, nil
}

func prepareBatchReleaseManifest(batch []copyBatchItem, requestedReleaseID string) (*bundle.ReleaseManifest, error) {
	releaseID := strings.TrimSpace(requestedReleaseID)
	if releaseID == "" {
		var commonTag string
		for i, item := range batch {
			ref, err := parseBatchRegistryReference(item.target(), "destination")
			if err != nil {
				return nil, fmt.Errorf("copy[%d]: %w", i, err)
			}
			tagged, ok := ref.(name.Tag)
			if !ok {
				return nil, errors.New("--release-id is required when batch destinations do not all use the same tag")
			}
			if commonTag == "" {
				commonTag = tagged.Identifier()
			} else if commonTag != tagged.Identifier() {
				return nil, errors.New("--release-id is required when batch destinations use different tags")
			}
		}
		releaseID = commonTag
	}
	if _, err := name.NewTag("example.invalid/repository:"+releaseID, name.WeakValidation); err != nil {
		return nil, fmt.Errorf("invalid --release-id %q: %w", releaseID, err)
	}

	manifest := &bundle.ReleaseManifest{
		ReleaseID: releaseID,
		Images:    make([]bundle.ReleaseImage, 0, len(batch)),
	}
	selectors := make(map[string]int, len(batch))
	ids := make(map[string]int, len(batch))
	for i, item := range batch {
		dstRef, err := parseBatchRegistryReference(item.target(), "destination")
		if err != nil {
			return nil, fmt.Errorf("copy[%d]: %w", i, err)
		}
		if _, err := parseBatchRegistryReference(item.source(), "source"); err != nil {
			return nil, fmt.Errorf("copy[%d]: %w", i, err)
		}
		inferred := bundle.InferredName(dstRef.Context().Name())
		appName := strings.TrimSpace(item.AppName)
		if appName == "" {
			appName = inferred
		}
		containerName := strings.TrimSpace(item.ContainerName)
		if containerName == "" {
			containerName = inferred
		}
		id := strings.TrimSpace(item.ID)
		if id == "" {
			id = appName
			if containerName != appName {
				id += "/" + containerName
			}
		}
		selector := appName + "\x00" + containerName
		if previous, exists := selectors[selector]; exists {
			return nil, fmt.Errorf("copy[%d] duplicates deployment selector from copy[%d]: %s/%s", i, previous, appName, containerName)
		}
		if previous, exists := ids[id]; exists {
			return nil, fmt.Errorf("copy[%d] duplicates image id from copy[%d]: %s", i, previous, id)
		}
		selectors[selector] = i
		ids[id] = i
		tag := ""
		if tagged, ok := dstRef.(name.Tag); ok {
			tag = tagged.Identifier()
		}
		manifest.Images = append(manifest.Images, bundle.ReleaseImage{
			ID:            id,
			AppName:       appName,
			ContainerName: containerName,
			Image:         dstRef.Context().Name(),
			Tag:           tag,
			ExtraTags:     normalizeAdditionalTags(item.DestTags),
		})
	}
	manifest.RegistryBase = commonReleaseRegistryBase(manifest.Images)
	return manifest, nil
}

func resolveBatchReleaseManifest(cmd *cobra.Command, manifest *bundle.ReleaseManifest, batch []copyBatchItem) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	srcOpts, err := auth.RemoteOptionsWithTransport(parseCreds(srcCreds), srcProxy, srcInsecure)
	if err != nil {
		return fmt.Errorf("source registry transport: %w", err)
	}
	dstOpts, err := auth.RemoteOptionsWithTransport(parseCreds(destCreds), destProxy, destInsecure)
	if err != nil {
		return fmt.Errorf("destination registry transport: %w", err)
	}
	srcOpts = append(srcOpts, remote.WithContext(ctx))
	dstOpts = append(dstOpts, remote.WithContext(ctx))
	var counter *progress.Counter
	if showProgress {
		counter = progress.NewCounter(len(batch), "record release manifest", cmd.ErrOrStderr())
		defer func() { _ = counter.Exit() }()
	}
	commonPlatform := ""
	mixedPlatforms := false
	for i, item := range batch {
		selector := manifest.Images[i].AppName + "/" + manifest.Images[i].ContainerName
		counter.Describe(fmt.Sprintf("record %d/%d: %s", i+1, len(batch), selector))
		dstRef, err := parseBatchRegistryReference(item.target(), "destination")
		if err != nil {
			return err
		}
		dstDesc, err := fetchManifestDescriptor(dstRef.String(), dstOpts)
		if err != nil {
			return fmt.Errorf("inspecting copied destination %s: %w", dstRef, err)
		}
		platform, err := reconstructPlatform(dstDesc)
		if err != nil {
			return fmt.Errorf("inspecting copied destination %s platform: %w", dstRef, err)
		}
		if commonPlatform == "" {
			commonPlatform = platform
		} else if commonPlatform != platform {
			mixedPlatforms = true
		}

		srcRef, err := parseBatchRegistryReference(item.source(), "source")
		if err != nil {
			return err
		}
		srcDesc, err := fetchManifestDescriptor(srcRef.String(), srcOpts)
		if err != nil {
			return fmt.Errorf("inspecting batch source %s: %w", srcRef, err)
		}
		sourceTag := ""
		if tagged, ok := srcRef.(name.Tag); ok {
			sourceTag = tagged.Identifier()
		}
		manifest.Images[i].Digest = dstDesc.Digest.String()
		manifest.Images[i].Platform = platform
		manifest.Images[i].Source = &bundle.ReleaseSource{
			Image:  srcRef.Context().Name(),
			Tag:    sourceTag,
			Digest: srcDesc.Digest.String(),
		}
		if err := counter.Advance(); err != nil {
			return fmt.Errorf("updating release manifest progress: %w", err)
		}
	}
	if err := counter.Finish(); err != nil {
		return fmt.Errorf("finishing release manifest progress: %w", err)
	}
	counter = nil
	manifest.CreatedAt = time.Now().UTC()
	if !mixedPlatforms {
		manifest.Platform = commonPlatform
	}
	if err := manifest.Validate(); err != nil {
		return fmt.Errorf("generated release manifest: %w", err)
	}
	return nil
}

func parseBatchRegistryReference(value, role string) (name.Reference, error) {
	if isFileRef(value) || isOCIArchiveRef(value) || isDaemonRef(value) {
		return nil, fmt.Errorf("%s %q is not a registry reference; release manifest output requires registry sources and destinations", role, value)
	}
	ref, err := name.ParseReference(stripScheme(value), name.WeakValidation)
	if err != nil {
		return nil, fmt.Errorf("invalid %s reference %q: %w", role, value, err)
	}
	return ref, nil
}

func commonReleaseRegistryBase(images []bundle.ReleaseImage) string {
	if len(images) == 0 {
		return ""
	}
	common := strings.Split(strings.Trim(images[0].Image, "/"), "/")
	for _, image := range images[1:] {
		parts := strings.Split(strings.Trim(image.Image, "/"), "/")
		limit := len(common)
		if len(parts) < limit {
			limit = len(parts)
		}
		i := 0
		for i < limit && common[i] == parts[i] {
			i++
		}
		common = common[:i]
	}
	if len(images) == 1 && len(common) > 1 {
		common = common[:len(common)-1]
	}
	return strings.Join(common, "/")
}

func loadReleaseCopyPlan(path, sourceBase, targetBase, requestedReleaseID string) (*releaseCopyPlan, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading release manifest %q: %w", path, err)
	}
	defer f.Close()

	manifest, err := bundle.DecodeReleaseDocument(f)
	if err != nil {
		return nil, fmt.Errorf("parsing release manifest %q: %w", path, err)
	}

	manifestReleaseID := strings.TrimSpace(manifest.ReleaseID)
	if manifestReleaseID == "" {
		return nil, fmt.Errorf("release manifest %q has no release_id", path)
	}
	releaseID := strings.TrimSpace(requestedReleaseID)
	if releaseID == "" {
		releaseID = manifestReleaseID
	}
	if _, err := name.NewTag("example.invalid/repository:"+releaseID, name.WeakValidation); err != nil {
		return nil, fmt.Errorf("invalid destination release tag %q (set --release-id to a valid tag): %w", releaseID, err)
	}
	if len(manifest.Images) == 0 {
		return nil, fmt.Errorf("release manifest %q has no images", path)
	}

	sourceBase = normalizeCopyPrefix(sourceBase)
	if sourceBase == "" {
		sourceBase = normalizeCopyPrefix(manifest.RegistryBase)
	}
	if sourceBase == "" {
		return nil, fmt.Errorf("--src-base is required when release manifest %q has no registry_base (v2: repository_prefix)", path)
	}
	targetBase = normalizeCopyPrefix(targetBase)
	if targetBase == "" {
		return nil, fmt.Errorf("--dest-base is required with --release-manifest")
	}
	if _, err := name.NewRepository(sourceBase+"/base-check", name.WeakValidation); err != nil {
		return nil, fmt.Errorf("invalid source base %q: %w", sourceBase, err)
	}
	if _, err := name.NewRepository(targetBase+"/base-check", name.WeakValidation); err != nil {
		return nil, fmt.Errorf("invalid destination base %q: %w", targetBase, err)
	}

	items := make([]copyBatchItem, 0, len(manifest.Images))
	targetImages := make([]bundle.ReleaseImage, 0, len(manifest.Images))
	destinations := make(map[string]int, len(manifest.Images))
	for i, releaseImage := range manifest.Images {
		repository := normalizeCopyPrefix(releaseImage.Image)
		if repository == "" {
			return nil, fmt.Errorf("release manifest %q images[%d].repository is required", path, i)
		}
		if !strings.HasPrefix(repository, sourceBase+"/") {
			return nil, fmt.Errorf(
				"release manifest %q images[%d].repository %q is outside source base %q",
				path,
				i,
				repository,
				sourceBase,
			)
		}
		relativeRepository := strings.TrimPrefix(repository, sourceBase+"/")
		if relativeRepository == "" {
			return nil, fmt.Errorf("release manifest %q images[%d].repository has no repository below source base", path, i)
		}

		digest := strings.TrimSpace(releaseImage.Digest)
		source, err := name.NewDigest(repository+"@"+digest, name.WeakValidation)
		if err != nil {
			return nil, fmt.Errorf("release manifest %q images[%d].digest is invalid: %w", path, i, err)
		}
		destination, err := name.NewTag(
			targetBase+"/"+relativeRepository+":"+releaseID,
			name.WeakValidation,
		)
		if err != nil {
			return nil, fmt.Errorf("release manifest %q images[%d] destination is invalid: %w", path, i, err)
		}
		if previous, exists := destinations[destination.String()]; exists {
			return nil, fmt.Errorf(
				"release manifest %q images[%d] duplicates destination from images[%d]: %s",
				path,
				i,
				previous,
				destination,
			)
		}
		destinations[destination.String()] = i
		items = append(items, copyBatchItem{
			Src:            source.String(),
			Dest:           destination.String(),
			ExpectedDigest: source.DigestStr(),
		})
		targetImages = append(targetImages, bundle.ReleaseImage{
			ID:            releaseImage.ID,
			AppName:       releaseImage.AppName,
			ContainerName: releaseImage.ContainerName,
			Source: &bundle.ReleaseSource{
				Image:  repository,
				Tag:    releaseImage.Tag,
				Digest: source.DigestStr(),
			},
			Image:    destination.Context().Name(),
			Tag:      releaseID,
			Digest:   source.DigestStr(),
			Platform: releaseImage.Platform,
		})
	}
	return &releaseCopyPlan{
		Items: items,
		Manifest: &bundle.ReleaseManifest{
			ReleaseID:    releaseID,
			CreatedAt:    time.Now().UTC(),
			Bundle:       manifest.Bundle,
			RegistryBase: targetBase,
			Platform:     manifest.Platform,
			Images:       targetImages,
		},
	}, nil
}

func normalizeCopyPrefix(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimPrefix(value, "http://")
	return strings.Trim(stripScheme(value), "/")
}

func runCopyBatch(ctx context.Context, batch []copyBatchItem) error {
	opts := currentCopyRunOptions()
	var failures []copyBatchFailure
	for i, item := range batch {
		label := fmt.Sprintf("copy %d/%d", i+1, len(batch))
		if !opts.Progress {
			slog.Info("running batch copy", "job", label, "source", item.source(), "destination", item.target())
		}
		itemOpts := opts
		itemOpts.ExpectedDigest = item.ExpectedDigest
		err := runCopyWithRetry(
			ctx,
			item.source(),
			item.target(),
			label,
			item.DestTags,
			itemOpts,
			currentCopyRetryPolicy(),
		)
		if err != nil {
			failures = append(failures, copyBatchFailure{Index: i + 1, Item: item, Err: err})
			if !continueOnError {
				return fmt.Errorf("%s failed: %w", label, err)
			}
			if !opts.Progress {
				slog.Error("batch copy failed; continuing", "job", label, "source", item.source(), "destination", item.target(), "err", err)
			}
		}
	}
	if len(failures) > 0 {
		return copyBatchError{Failures: failures}
	}
	return nil
}

func runCopyWithRetry(
	ctx context.Context,
	src string,
	dst string,
	batchLabel string,
	additionalDestTags []string,
	opts copyRunOptions,
	policy copyRetryPolicy,
) error {
	label := batchLabel
	if label == "" {
		label = shortRef(src)
	}
	return runWithCopyRetry(ctx, label, policy, func() error {
		return runCopyOne(ctx, src, dst, batchLabel, additionalDestTags, opts)
	})
}

func runWithCopyRetry(
	ctx context.Context,
	label string,
	policy copyRetryPolicy,
	operation func() error,
) error {
	for attempt := 0; ; attempt++ {
		err := operation()
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt >= policy.Retries || !isRetryableCopyError(err) {
			return err
		}

		retryNumber := attempt + 1
		delay := retryDelay(policy, retryNumber)
		fmt.Fprintf(
			os.Stderr,
			"retry: %s: transient error: %v; retry %d/%d in %s\n",
			label,
			err,
			retryNumber,
			policy.Retries,
			delay,
		)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func validateCopyRetryPolicy(policy copyRetryPolicy) error {
	if policy.Retries < 0 {
		return fmt.Errorf("--retries must be zero or greater")
	}
	if policy.InitialDelay <= 0 {
		return fmt.Errorf("--retry-initial-delay must be greater than zero")
	}
	if policy.MaxDelay <= 0 {
		return fmt.Errorf("--retry-max-delay must be greater than zero")
	}
	if policy.MaxDelay < policy.InitialDelay {
		return fmt.Errorf("--retry-max-delay must be greater than or equal to --retry-initial-delay")
	}
	return nil
}

func retryDelay(policy copyRetryPolicy, retryNumber int) time.Duration {
	delay := policy.InitialDelay
	for i := 1; i < retryNumber && delay < policy.MaxDelay; i++ {
		if delay > policy.MaxDelay/2 {
			return policy.MaxDelay
		}
		delay *= 2
	}
	if delay > policy.MaxDelay {
		return policy.MaxDelay
	}
	return delay
}

func isRetryableCopyError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNREFUSED) ||
		errors.Is(err, syscall.ETIMEDOUT) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}

	var networkError net.Error
	if errors.As(err, &networkError) && (networkError.Timeout() || networkError.Temporary()) {
		return true
	}
	var temporary interface{ Temporary() bool }
	if errors.As(err, &temporary) && temporary.Temporary() {
		return true
	}

	message := strings.ToLower(err.Error())
	for _, fragment := range []string{
		"unexpected eof",
		"operation timed out",
		"connection reset",
		"connection refused",
		"broken pipe",
		"tls handshake timeout",
		"server closed idle connection",
		"temporary failure",
	} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

func (i copyBatchItem) source() string {
	if i.Src != "" {
		return i.Src
	}
	return i.Source
}

func (i copyBatchItem) target() string {
	if i.Dest != "" {
		return i.Dest
	}
	return i.Destination
}

func (i *copyBatchItem) destinations() ([]string, error) {
	target := i.target()
	destinations := []string{target}
	if len(i.DestTags) == 0 {
		return destinations, nil
	}
	targetTag, err := name.NewTag(stripScheme(target), name.WeakValidation)
	if err != nil {
		return nil, fmt.Errorf("invalid dest reference for dest_tags: %w", err)
	}
	destinations[0] = targetTag.String()
	for index, tag := range i.DestTags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			return nil, fmt.Errorf("dest_tags[%d] is empty", index)
		}
		parsedTag, err := name.NewTag(targetTag.Context().Name()+":"+tag, name.WeakValidation)
		if err != nil {
			return nil, fmt.Errorf("invalid dest_tags[%d] %q: %w", index, tag, err)
		}
		i.DestTags[index] = tag
		destinations = append(destinations, parsedTag.String())
	}
	return destinations, nil
}

func runCopyOne(ctx context.Context, src, dst, batchLabel string, additionalDestTags []string, opts copyRunOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	srcOpts, err := auth.RemoteOptionsWithTransport(parseCreds(opts.SrcCreds), opts.SrcProxy, opts.SrcInsecure)
	if err != nil {
		return fmt.Errorf("source registry transport: %w", err)
	}
	dstOpts, err := auth.RemoteOptionsWithTransport(parseCreds(opts.DestCreds), opts.DestProxy, opts.DestInsecure)
	if err != nil {
		return fmt.Errorf("destination registry transport: %w", err)
	}
	srcOpts = append(srcOpts, remote.WithContext(ctx))
	dstOpts = append(dstOpts, remote.WithContext(ctx))
	recipeSourceTransportOpts := []remote.Option{remote.WithContext(ctx)}
	if strings.TrimSpace(opts.SrcProxy) != "" || opts.SrcInsecure {
		transportOpt, transportErr := auth.Transport(opts.SrcProxy, opts.SrcInsecure)
		if transportErr != nil {
			return fmt.Errorf("source registry transport: %w", transportErr)
		}
		recipeSourceTransportOpts = append(recipeSourceTransportOpts, transportOpt)
	}

	srcIsDaemon := isDaemonRef(src)
	srcIsOCIArchive := isOCIArchiveRef(src)
	srcIsFile := isFileRef(src) || srcIsOCIArchive
	dstIsDaemon := isDaemonRef(dst)
	dstIsOCIArchive := isOCIArchiveRef(dst)
	isFileDst := isFileRef(dst) || dstIsOCIArchive
	if len(additionalDestTags) > 0 && (isFileDst || dstIsDaemon) {
		return fmt.Errorf("--dest-tag is supported only for registry destinations")
	}

	if shouldTryRegistryDigestReuse(srcIsDaemon, srcIsFile, dstIsDaemon, isFileDst, opts) {
		if opts.Progress {
			label := "copy"
			if batchLabel != "" {
				label = batchLabel
			}
			fmt.Fprintf(os.Stderr, "phase: %s: comparing source and destination digests\n", label)
		}
		reused, err := tryRegistryDigestReuse(src, dst, additionalDestTags, opts, srcOpts, dstOpts)
		if err != nil {
			return err
		}
		if reused {
			if opts.ProgressJSON {
				emitter := progress.NewJSONEmitter(os.Stdout)
				emitter.Done("success", "destination already contains source digest; no layers transferred")
			}
			slog.Info(
				"destination already contains source digest; no layers transferred",
				"job", batchLabel,
				"source", stripScheme(src),
				"destination", stripScheme(dst),
			)
			return nil
		}
	}

	// --- setup progress UI ---
	var ui *progress.UI
	var jsonEmitter *progress.JSONEmitter
	var srcCh, dstCh chan<- v1.Update
	phaseTimer := progress.NewPhaseTimer()
	setPhase := func(text string) {
		if batchLabel != "" {
			text = batchLabel + ": " + text
		}
		phaseTimer.Start(text)
		if ui != nil {
			ui.SetStatus("phase: " + text)
		}
	}
	printTimings := func() {
		if opts.Progress {
			if summary := phaseTimer.Summary(); summary != "" {
				fmt.Fprint(os.Stderr, summary)
			}
		}
	}
	if opts.Progress {
		srcLabel := shortRef(src)
		switch {
		case isFileDst:
			filePath := strings.TrimPrefix(dst, "file://")
			ui = progress.NewSingle("download+save: " + filePath)
			dstCh = ui.SrcChannel()
		case dstIsDaemon:
			ui = progress.NewSingle("pull: " + srcLabel)
			srcCh = ui.SrcChannel()
		default:
			dstLabel := shortRef(dst)
			ui = progress.New("pull: "+srcLabel, "push: "+dstLabel)
			srcCh = ui.SrcChannel()
			dstCh = ui.DstChannel()
		}
		ui.Run()
		if srcCh != nil && !srcIsDaemon && !srcIsFile {
			srcOpts = append(srcOpts, remote.WithProgress(srcCh))
		} else if dstCh != nil && dstIsOCIArchive && !srcIsDaemon && !srcIsFile {
			srcOpts = append(srcOpts, remote.WithProgress(dstCh))
		}
	} else if opts.ProgressJSON {
		jsonEmitter = progress.NewJSONEmitter(os.Stdout)
		srcCh = jsonEmitter.Channel("pull")
		switch {
		case isFileDst:
			dstCh = jsonEmitter.Channel("save")
		case dstIsDaemon:
			// daemon writes currently emit phase/done only
		default:
			dstCh = jsonEmitter.Channel("push")
		}
		if !srcIsDaemon {
			srcOpts = append(srcOpts, remote.WithProgress(srcCh))
		}
	}
	defer func() {
		closeProg(srcCh, dstCh)
		if ui != nil {
			ui.Wait()
		}
	}()

	// --- load source image ---
	var (
		img         v1.Image
		srcIndex    v1.ImageIndex
		indexDigest string
		srcLabel    string
		archiveRoot *ociarchive.Content
	)
	if srcIsOCIArchive {
		filePath := strings.TrimPrefix(src, "oci-archive://")
		srcLabel = filePath
		setPhase("loading source image from OCI archive")
		if jsonEmitter != nil {
			jsonEmitter.Phase("pull", filePath)
		}
		archiveRoot, err = ociarchive.Open(filePath)
		if err == nil {
			defer archiveRoot.Close()
			img = archiveRoot.Image
			srcIndex = archiveRoot.Index
			if srcIndex != nil {
				var digest v1.Hash
				digest, err = srcIndex.Digest()
				indexDigest = digest.String()
			}
		}
		if err != nil {
			closeProg(srcCh, dstCh)
			return fmt.Errorf("loading source image from OCI archive %q: %w", filePath, err)
		}
	} else if isFileRef(src) {
		filePath := strings.TrimPrefix(src, "file://")
		srcLabel = filePath
		if opts.Debug {
			slog.Debug("loading source image from Docker archive", "path", filePath)
		}
		setPhase("loading source image from Docker archive")
		if jsonEmitter != nil {
			jsonEmitter.Phase("pull", filePath)
		}
		img, err = loadFileImage(src)
		if err != nil {
			closeProg(srcCh, dstCh)
			if jsonEmitter != nil {
				jsonEmitter.Error("loading source image from Docker archive: " + err.Error())
			}
			return err
		}
	} else if srcIsDaemon {
		srcTag, err := parseDaemonTag(src)
		if err != nil {
			closeProg(srcCh, dstCh)
			if jsonEmitter != nil {
				jsonEmitter.Error("invalid daemon source reference: " + err.Error())
			}
			return fmt.Errorf("invalid daemon source reference: %w", err)
		}
		srcLabel = srcTag.String()
		if opts.Debug {
			slog.Debug("loading source image from daemon", "ref", srcTag.String())
		}
		setPhase("loading source image from docker daemon")
		if jsonEmitter != nil {
			jsonEmitter.Phase("pull", srcTag.String())
		}
		img, err = daemon.Image(srcTag)
		if err != nil {
			closeProg(srcCh, dstCh)
			if jsonEmitter != nil {
				jsonEmitter.Error("loading source image from daemon: " + err.Error())
			}
			return fmt.Errorf("loading source image from daemon: %w", err)
		}
	} else {
		srcParsed, err := name.ParseReference(stripScheme(src))
		if err != nil {
			closeProg(srcCh, dstCh)
			if jsonEmitter != nil {
				jsonEmitter.Error("invalid source reference: " + err.Error())
			}
			return fmt.Errorf("invalid source reference: %w", err)
		}
		srcLabel = srcParsed.String()
		if opts.Debug {
			slog.Debug("pulling source image", "ref", srcParsed.String())
		}
		setPhase("pulling source image")
		if jsonEmitter != nil {
			jsonEmitter.Phase("pull", srcParsed.String())
		}
		desc, getErr := remote.Get(srcParsed, srcOpts...)
		if getErr != nil {
			closeProg(srcCh, dstCh)
			if jsonEmitter != nil {
				jsonEmitter.Error("pulling source image: " + getErr.Error())
			}
			return fmt.Errorf("pulling source image: %w", getErr)
		}
		if desc.MediaType.IsIndex() {
			srcIndex, err = desc.ImageIndex()
			indexDigest = desc.Digest.String()
		} else {
			img, err = desc.Image()
		}
		if err != nil {
			closeProg(srcCh, dstCh)
			if jsonEmitter != nil {
				jsonEmitter.Error("reading source manifest: " + err.Error())
			}
			return fmt.Errorf("reading source manifest: %w", err)
		}
	}

	if srcIndex != nil {
		if len(opts.RecipePaths) > 0 || opts.Reduce || opts.ReduceChanges || opts.WrapEntrypoint || opts.NoWrapEntrypoint {
			closeProg(srcCh, dstCh)
			return fmt.Errorf("recipes, layer reduction, and entrypoint wrapping are not supported when copying a multi-platform image index")
		}
		if (isFileDst && !dstIsOCIArchive) || dstIsDaemon {
			closeProg(srcCh, dstCh)
			return fmt.Errorf("multi-platform image indexes can currently be copied only between registries or OCI archives")
		}
		if opts.DestNoReuse {
			closeProg(srcCh, dstCh)
			return fmt.Errorf("--dest-no-reuse is not supported when copying a multi-platform image index")
		}
		if len(additionalDestTags) > 0 {
			closeProg(srcCh, dstCh)
			return fmt.Errorf("--dest-tag is not supported when copying a multi-platform image index")
		}
		if dstIsOCIArchive {
			filePath := strings.TrimPrefix(dst, "oci-archive://")
			setPhase("writing OCI archive")
			if jsonEmitter != nil {
				jsonEmitter.Phase("save", filePath)
			}
			if err := ociarchive.WriteIndex(filePath, srcIndex); err != nil {
				closeProg(nil, dstCh)
				return fmt.Errorf("writing OCI archive: %w", err)
			}
			if dstCh != nil {
				safeClose(dstCh)
				dstCh = nil
			}
			if ui != nil {
				ui.Wait()
				printTimings()
			} else {
				slog.Info("saved image index OCI archive", "source", srcLabel, "path", filePath)
			}
			if jsonEmitter != nil {
				jsonEmitter.Done("success", "saved image index OCI archive")
			}
			return nil
		}
		dstParsed, err := name.ParseReference(stripScheme(dst))
		if err != nil {
			closeProg(srcCh, dstCh)
			return fmt.Errorf("invalid destination reference: %w", err)
		}
		writeOpts := dstOpts
		if dstCh != nil {
			writeOpts = append(writeOpts, remote.WithProgress(dstCh))
		}
		setPhase("pushing destination image index")
		if jsonEmitter != nil {
			jsonEmitter.Phase("push", dstParsed.String())
		}
		if err := remote.WriteIndex(dstParsed, srcIndex, writeOpts...); err != nil {
			closeProg(srcCh, dstCh)
			if jsonEmitter != nil {
				jsonEmitter.Error("pushing image index: " + err.Error())
			}
			return fmt.Errorf("pushing image index: %w", err)
		}
		pushed, err := remote.Get(dstParsed, dstOpts...)
		if err != nil {
			closeProg(srcCh, dstCh)
			return fmt.Errorf("verifying destination image index: %w", err)
		}
		expectedDigest := indexDigest
		if opts.ExpectedDigest != "" {
			expectedDigest = opts.ExpectedDigest
		}
		if pushed.Digest.String() != expectedDigest {
			closeProg(srcCh, dstCh)
			return fmt.Errorf(
				"destination image index digest %s differs from source digest %s",
				pushed.Digest,
				expectedDigest,
			)
		}
		if srcCh != nil {
			safeClose(srcCh)
			srcCh = nil
		}
		if ui != nil {
			ui.Wait()
			printTimings()
		} else {
			slog.Info("copied image index", "source", srcLabel, "destination", dstParsed.String())
		}
		if jsonEmitter != nil {
			jsonEmitter.Done("success", "copied image index")
		}
		return nil
	}

	// go-containerregistry pull is lazy — remote.WithProgress only fires during push (write.go).
	// For pull progress we manually stream all layers and report bytes to srcCh ourselves.
	if shouldEagerPull(isFileDst, opts.Progress, opts.ProgressJSON) {
		if err := eagerPull(img, srcCh); err != nil {
			closeProg(srcCh, dstCh)
			if jsonEmitter != nil {
				jsonEmitter.Error("pulling layers: " + err.Error())
			}
			return fmt.Errorf("pulling layers: %w", err)
		}
	}
	sourceImg := img
	var shadowEntrypointOverride *recipe.ShadowEntrypointOverride
	if opts.WrapEntrypoint {
		sourceCfg, cfgErr := sourceImg.ConfigFile()
		if cfgErr != nil {
			closeProg(srcCh, dstCh)
			return fmt.Errorf("reading source startup configuration: %w", cfgErr)
		}
		shadowEntrypointOverride = &recipe.ShadowEntrypointOverride{
			Entrypoint: append([]string(nil), sourceCfg.Config.Entrypoint...),
			Cmd:        append([]string(nil), sourceCfg.Config.Cmd...),
		}
	}

	// --- apply recipes ---
	var reduceBase v1.Image
	for _, path := range opts.RecipePaths {
		r, err := recipe.Load(path)
		if err != nil {
			closeProg(srcCh, dstCh)
			return err
		}
		if opts.WrapEntrypoint || opts.NoWrapEntrypoint {
			setShadowEntrypointWrapping(r, opts.WrapEntrypoint)
		}

		if !opts.Progress {
			slog.Info("applying recipe", "name", r.Name)
		}

		ctx, err := recipe.ResolveContextSources(r, src, img, recipeSourceTransportOpts...)
		if err != nil {
			closeProg(srcCh, dstCh)
			if jsonEmitter != nil {
				jsonEmitter.Error("resolving recipe sources: " + err.Error())
			}
			return fmt.Errorf("resolving recipe sources: %w", err)
		}
		ctx.ShadowEntrypointOverride = shadowEntrypointOverride
		for _, step := range r.Steps {
			if step.Action != "use-base" {
				continue
			}
			baseImg, resolveErr := recipe.ResolveSource(step.From, ctx)
			if resolveErr != nil {
				closeProg(srcCh, dstCh)
				return fmt.Errorf("resolving reduce base: %w", resolveErr)
			}
			reduceBase = baseImg
		}

		name := r.Name
		if name == "" {
			name = path
		}
		setPhase("applying recipe " + name)
		if jsonEmitter != nil {
			jsonEmitter.Phase("recipe", name)
		}
		report := func(text string) {
			setPhase(text)
			if jsonEmitter != nil {
				jsonEmitter.Status(text)
			}
		}
		img, err = recipe.ApplyWithReporter(img, r, ctx, report)
		if err != nil {
			closeProg(srcCh, dstCh)
			if jsonEmitter != nil {
				jsonEmitter.Error("applying recipe failed: " + err.Error())
			}
			return fmt.Errorf("applying recipe %q: %w", path, err)
		}
	}

	// signal source bar done after pull+recipe phase
	if srcCh != nil && !dstIsDaemon {
		safeClose(srcCh)
		srcCh = nil
	}

	// --- squash layers if requested ---
	if opts.Reduce {
		if !opts.Progress {
			slog.Info("reducing layers")
		}
		setPhase("squashing layers")
		if jsonEmitter != nil {
			jsonEmitter.Phase("squash", "layers")
		}
		img, err = image.Squash(img)
		if err != nil {
			closeProg(srcCh, dstCh)
			if jsonEmitter != nil {
				jsonEmitter.Error("squashing layers: " + err.Error())
			}
			return fmt.Errorf("squashing layers: %w", err)
		}
	}

	// --- reduce recipe changes if requested ---
	if opts.ReduceChanges {
		if !opts.Progress {
			slog.Info("reducing recipe changes")
		}
		setPhase("reducing recipe changes")
		if jsonEmitter != nil {
			jsonEmitter.Phase("reduce-changes", "recipe changes")
		}
		if reduceBase == nil {
			reduceBase = sourceImg
		}
		img, err = image.ReduceToBase(reduceBase, img)
		if err != nil {
			closeProg(srcCh, dstCh)
			if jsonEmitter != nil {
				jsonEmitter.Error("reducing recipe changes: " + err.Error())
			}
			return fmt.Errorf("reducing recipe changes: %w", err)
		}
	}

	// --- push destination ---
	if dstIsOCIArchive {
		filePath := strings.TrimPrefix(dst, "oci-archive://")
		setPhase("writing OCI archive")
		if jsonEmitter != nil {
			jsonEmitter.Phase("save", filePath)
		}
		if err := ociarchive.WriteImage(filePath, img); err != nil {
			closeProg(nil, dstCh)
			return fmt.Errorf("writing OCI archive: %w", err)
		}
		if dstCh != nil {
			safeClose(dstCh)
			dstCh = nil
		}
		if ui != nil {
			ui.Wait()
			printTimings()
		} else {
			slog.Info("saved image OCI archive", "source", srcLabel, "path", filePath)
		}
		if jsonEmitter != nil {
			jsonEmitter.Done("success", "saved image OCI archive")
		}
		return nil
	}
	if strings.HasPrefix(dst, "file://") {
		filePath := strings.TrimPrefix(dst, "file://")
		// Docker archives require a tag even when the source was addressed by digest.
		tag, tagErr := tarballTag(stripScheme(src))
		if tagErr != nil {
			closeProg(nil, dstCh)
			return fmt.Errorf("deriving tag from source: %w", tagErr)
		}
		if opts.Debug {
			slog.Debug("writing tarball", "path", filePath, "tag", tag.String())
		}
		setPhase("writing tarball")
		if jsonEmitter != nil {
			jsonEmitter.Phase("save", filePath)
		}
		tarOpts := []tarball.WriteOption{}
		if dstCh != nil {
			tarOpts = append(tarOpts, tarball.WithProgress(dstCh))
		}
		if err := tarball.WriteToFile(filePath, tag, img, tarOpts...); err != nil {
			closeProg(nil, dstCh)
			return fmt.Errorf("writing tarball: %w", err)
		}
		if dstCh != nil {
			safeClose(dstCh)
			dstCh = nil
		}
		if ui != nil {
			ui.Wait()
			printTimings()
		} else {
			slog.Info("saved image tarball", "source", srcLabel, "path", filePath)
		}
		if jsonEmitter != nil {
			jsonEmitter.Done("success", "saved image tarball")
		}
		return nil
	}

	if dstIsDaemon {
		dstTag, err := parseDaemonTag(dst)
		if err != nil {
			closeProg(nil, dstCh)
			if jsonEmitter != nil {
				jsonEmitter.Error("invalid daemon destination reference: " + err.Error())
			}
			return fmt.Errorf("invalid daemon destination reference: %w", err)
		}
		if opts.Debug {
			slog.Debug("loading image into docker daemon", "ref", dstTag.String())
		}
		setPhase("loading image into docker daemon")
		if jsonEmitter != nil {
			jsonEmitter.Phase("load", dstTag.String())
		}
		if _, err := daemon.Write(dstTag, img); err != nil {
			closeProg(srcCh, dstCh)
			if jsonEmitter != nil {
				jsonEmitter.Error("loading image into docker daemon: " + err.Error())
			}
			return fmt.Errorf("loading image into docker daemon: %w", err)
		}
		if srcCh != nil {
			safeClose(srcCh)
			srcCh = nil
		}
		if ui != nil {
			ui.Wait()
			printTimings()
		} else {
			slog.Info("copied image", "source", srcLabel, "destination", dstTag.String())
		}
		if jsonEmitter != nil {
			jsonEmitter.Done("success", "loaded image into docker daemon")
		}
		return nil
	}

	dstParsed, err := name.ParseReference(stripScheme(dst))
	if err != nil {
		closeProg(srcCh, dstCh)
		if jsonEmitter != nil {
			jsonEmitter.Error("invalid destination reference: " + err.Error())
		}
		return fmt.Errorf("invalid destination reference: %w", err)
	}

	writeOpts := dstOpts
	if opts.DestNoReuse {
		img = image.WithoutMountReuse(img)
	}
	if dstCh != nil {
		writeOpts = append(writeOpts, remote.WithProgress(dstCh))
	}

	if opts.Debug {
		slog.Debug("pushing destination image", "ref", dstParsed.String())
	}
	setPhase("pushing destination image")
	if jsonEmitter != nil {
		jsonEmitter.Phase("push", dstParsed.String())
	}

	if err := remote.Write(dstParsed, img, writeOpts...); err != nil {
		if jsonEmitter != nil {
			jsonEmitter.Error("pushing image: " + err.Error())
		}
		return fmt.Errorf("pushing image: %w", err)
	}

	if len(additionalDestTags) > 0 {
		if dstCh != nil {
			safeClose(dstCh)
			dstCh = nil
		}
		if err := tagAdditionalDestTags(dstParsed, img, dstOpts, additionalDestTags, setPhase, jsonEmitter); err != nil {
			return err
		}
	}
	if opts.ExpectedDigest != "" {
		pushed, err := remote.Get(dstParsed, dstOpts...)
		if err != nil {
			closeProg(srcCh, dstCh)
			if jsonEmitter != nil {
				jsonEmitter.Error("verifying destination image: " + err.Error())
			}
			return fmt.Errorf("verifying destination image: %w", err)
		}
		if pushed.Digest.String() != opts.ExpectedDigest {
			closeProg(srcCh, dstCh)
			if jsonEmitter != nil {
				jsonEmitter.Error("destination image digest differs from release manifest")
			}
			return fmt.Errorf(
				"destination image digest %s differs from release manifest digest %s",
				pushed.Digest,
				opts.ExpectedDigest,
			)
		}
	}

	if ui != nil {
		ui.Wait()
		printTimings()
	} else {
		slog.Info("copied image", "source", srcLabel, "destination", dstParsed.String())
	}
	if jsonEmitter != nil {
		jsonEmitter.Done("success", "copied image")
	}

	return nil
}

func setShadowEntrypointWrapping(r *recipe.Recipe, enabled bool) {
	for i := range r.Steps {
		if r.Steps[i].Action == "shadow" {
			r.Steps[i].ShadowWrapEntrypoint = &enabled
		}
	}
}

func validateShadowEntrypointFlags(wrap, noWrap bool) error {
	if wrap && noWrap {
		return fmt.Errorf("--wrap-entrypoint and --no-wrap-entrypoint cannot be used together")
	}
	return nil
}

func shouldTryRegistryDigestReuse(srcIsDaemon, srcIsFile, dstIsDaemon, dstIsFile bool, opts copyRunOptions) bool {
	return !srcIsDaemon && !srcIsFile && !dstIsDaemon && !dstIsFile &&
		len(opts.RecipePaths) == 0 && !opts.Reduce && !opts.ReduceChanges &&
		!opts.WrapEntrypoint && !opts.NoWrapEntrypoint && !opts.DestNoReuse
}

// tryRegistryDigestReuse avoids pulling layers when the exact source manifest
// already exists in the destination repository. It either leaves an already
// matching destination tag untouched or applies the requested tags directly to
// the destination manifest. A false result means a regular copy is required.
func tryRegistryDigestReuse(src, dst string, additionalDestTags []string, opts copyRunOptions, srcOpts, dstOpts []remote.Option) (bool, error) {
	srcRef, err := name.ParseReference(stripScheme(src))
	if err != nil {
		return false, fmt.Errorf("invalid source reference: %w", err)
	}
	dstRef, err := name.ParseReference(stripScheme(dst))
	if err != nil {
		return false, fmt.Errorf("invalid destination reference: %w", err)
	}

	sourceDesc, err := remote.Get(srcRef, srcOpts...)
	if err != nil {
		return false, fmt.Errorf("inspecting source manifest: %w", err)
	}
	sourceDigest := sourceDesc.Digest.String()
	if opts.ExpectedDigest != "" && sourceDigest != opts.ExpectedDigest {
		return false, fmt.Errorf("source image digest %s differs from expected digest %s", sourceDigest, opts.ExpectedDigest)
	}
	// Preserve the existing validation for indexes: additional destination tags
	// are currently supported only for single-platform images.
	if sourceDesc.MediaType.IsIndex() && len(additionalDestTags) > 0 {
		return false, nil
	}

	destinationDesc, destinationErr := remote.Get(dstRef, dstOpts...)
	if destinationErr == nil && destinationDesc.Digest.String() == sourceDigest {
		if err := tagExistingDestinationManifest(dstRef, destinationDesc, additionalDestTags, dstOpts); err != nil {
			return false, err
		}
		return true, nil
	}
	if destinationErr != nil && !isRegistryManifestNotFound(destinationErr) {
		return false, fmt.Errorf("inspecting destination manifest: %w", destinationErr)
	}

	dstTag, ok := dstRef.(name.Tag)
	if !ok {
		return false, nil
	}
	digestRef := dstTag.Context().Digest(sourceDigest)
	digestDesc, err := remote.Get(digestRef, dstOpts...)
	if err != nil {
		if isRegistryManifestNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("checking destination repository for digest %s: %w", sourceDigest, err)
	}
	if digestDesc.Digest.String() != sourceDigest {
		return false, fmt.Errorf("destination digest lookup returned %s, expected %s", digestDesc.Digest, sourceDigest)
	}
	if err := remote.Tag(dstTag, digestDesc, dstOpts...); err != nil {
		return false, fmt.Errorf("tagging existing destination manifest as %s: %w", dstTag, err)
	}
	if err := tagExistingDestinationManifest(dstTag, digestDesc, additionalDestTags, dstOpts); err != nil {
		return false, err
	}
	return true, nil
}

func tagExistingDestinationManifest(dst name.Reference, desc *remote.Descriptor, tags []string, opts []remote.Option) error {
	if len(tags) == 0 {
		return nil
	}
	dstTag, ok := dst.(name.Tag)
	if !ok {
		return fmt.Errorf("--dest-tag requires destination reference to be a tag")
	}
	for _, tag := range normalizeAdditionalTags(tags) {
		target, err := name.NewTag(dstTag.Context().Name()+":"+tag, name.WeakValidation)
		if err != nil {
			return fmt.Errorf("invalid additional destination tag %q: %w", tag, err)
		}
		if err := remote.Tag(target, desc, opts...); err != nil {
			return fmt.Errorf("tagging additional destination %s: %w", target, err)
		}
		slog.Info("tagged additional destination", "destination", target.String())
	}
	return nil
}

func isRegistryManifestNotFound(err error) bool {
	var registryErr *transport.Error
	return errors.As(err, &registryErr) && registryErr.StatusCode == http.StatusNotFound
}

func tagAdditionalDestTags(dst name.Reference, img v1.Image, opts []remote.Option, tags []string, setPhase func(string), jsonEmitter *progress.JSONEmitter) error {
	dstTag, ok := dst.(name.Tag)
	if !ok {
		return fmt.Errorf("--dest-tag requires destination reference to be a tag")
	}
	digest, err := img.Digest()
	if err != nil {
		return fmt.Errorf("computing pushed image digest: %w", err)
	}
	desc, err := remote.Get(dstTag.Context().Digest(digest.String()), opts...)
	if err != nil {
		return fmt.Errorf("fetching pushed manifest for additional tags: %w", err)
	}
	for _, tag := range normalizeAdditionalTags(tags) {
		target, err := name.NewTag(dstTag.Context().Name()+":"+tag, name.WeakValidation)
		if err != nil {
			return fmt.Errorf("invalid additional destination tag %q: %w", tag, err)
		}
		setPhase("tagging additional destination " + target.String())
		if jsonEmitter != nil {
			jsonEmitter.Phase("tag", target.String())
		}
		if err := remote.Tag(target, desc, opts...); err != nil {
			if jsonEmitter != nil {
				jsonEmitter.Error("tagging additional destination " + target.String() + ": " + err.Error())
			}
			return fmt.Errorf("tagging additional destination %s: %w", target.String(), err)
		}
		slog.Info("tagged additional destination", "destination", target.String())
	}
	return nil
}

func normalizeAdditionalTags(tags []string) []string {
	out := make([]string, 0, len(tags))
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if _, ok := seen[tag]; ok {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out
}

// closeProg closes any open progress channels (cleanup on error).
func closeProg(srcCh, dstCh chan<- v1.Update) {
	if srcCh != nil {
		safeClose(srcCh)
	}
	if dstCh != nil {
		safeClose(dstCh)
	}
}

func safeClose(ch chan<- v1.Update) {
	defer func() {
		_ = recover()
	}()
	close(ch)
}

// shortRef returns a short human-readable label from a docker:// reference.
func shortRef(ref string) string {
	ref = stripScheme(ref)
	// keep only repo:tag, drop the registry hostname
	parts := strings.SplitN(ref, "/", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return ref
}

func stripScheme(ref string) string {
	ref = strings.TrimPrefix(ref, "docker://")
	ref = strings.TrimPrefix(ref, "oci://")
	ref = strings.TrimPrefix(ref, "docker-daemon://")
	ref = strings.TrimPrefix(ref, "daemon://")
	return ref
}

func isDaemonRef(ref string) bool {
	return strings.HasPrefix(ref, "docker-daemon://") || strings.HasPrefix(ref, "daemon://")
}

func isFileRef(ref string) bool {
	return strings.HasPrefix(ref, "file://")
}

func isOCIArchiveRef(ref string) bool {
	return strings.HasPrefix(ref, "oci-archive://")
}

func shouldEagerPull(isFileDst, progressEnabled, progressJSONEnabled bool) bool {
	// tarball.WriteToFile streams remote layers while writing the archive.
	// Reading them eagerly first would cause every layer to be downloaded twice.
	return !isFileDst && (progressEnabled || progressJSONEnabled)
}

func loadFileImage(ref string) (v1.Image, error) {
	path := strings.TrimPrefix(ref, "file://")
	if path == "" || path == ref {
		return nil, fmt.Errorf("invalid file source reference %q", ref)
	}
	img, err := tarball.ImageFromPath(path, nil)
	if err != nil {
		return nil, fmt.Errorf("loading image from Docker archive %q: %w", path, err)
	}
	return img, nil
}

func parseDaemonTag(ref string) (name.Tag, error) {
	ref = strings.TrimPrefix(ref, "docker-daemon://")
	ref = strings.TrimPrefix(ref, "daemon://")
	return name.NewTag(ref, name.WeakValidation)
}

func tarballTag(refValue string) (name.Tag, error) {
	ref, err := name.ParseReference(refValue, name.WeakValidation)
	if err != nil {
		return name.Tag{}, err
	}
	if tag, ok := ref.(name.Tag); ok {
		return tag, nil
	}
	return ref.Context().Tag("snapshot"), nil
}

func parseCreds(s string) *auth.Options {
	if s == "" {
		return nil
	}
	parts := strings.SplitN(s, ":", 2)
	if len(parts) != 2 {
		return nil
	}
	return &auth.Options{Username: parts[0], Password: parts[1]}
}

// eagerPull streams all compressed layers to warm the cache and optionally reports
// pull progress to ch (nil = discard). go-containerregistry's WithProgress only
// fires during push, so we do manual byte-counting here for pull progress bars.
func eagerPull(img v1.Image, ch chan<- v1.Update) error {
	layers, err := img.Layers()
	if err != nil {
		return err
	}

	// l.Size() is the compressed size from the manifest — best estimate we have upfront.
	var total int64
	for _, l := range layers {
		if sz, sErr := l.Size(); sErr == nil {
			total += sz
		}
	}
	if ch != nil {
		ch <- v1.Update{Total: total, Complete: 0}
	}

	var done int64
	buf := make([]byte, 32*1024)
	for _, l := range layers {
		rc, lErr := l.Compressed()
		if lErr != nil {
			return lErr
		}
		for {
			n, rErr := rc.Read(buf)
			if n > 0 {
				done += int64(n)
				if ch != nil {
					// clamp reported complete so bar never exceeds 100%
					reported := done
					if total > 0 && reported > total {
						reported = total - 1
					}
					ch <- v1.Update{Total: total, Complete: reported}
				}
			}
			if rErr == io.EOF {
				break
			}
			if rErr != nil {
				rc.Close()
				return rErr
			}
		}
		rc.Close()
	}
	// force bar to exactly 100% using manifest total (actual bytes may differ slightly)
	if ch != nil {
		ch <- v1.Update{Total: total, Complete: total}
	}
	return nil
}
