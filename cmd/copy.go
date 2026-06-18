package cmd

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/daemon"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/martinmares/oci-patch/internal/auth"
	"github.com/martinmares/oci-patch/internal/image"
	"github.com/martinmares/oci-patch/internal/logging"
	"github.com/martinmares/oci-patch/internal/progress"
	"github.com/martinmares/oci-patch/internal/recipe"
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
	continueOnError  bool
	srcInsecure      bool
	destInsecure     bool
	destTags         []string
)

var copyCmd = &cobra.Command{
	Use:   "copy SOURCE DESTINATION | copy --batch-file FILE",
	Short: "Copy and patch an OCI image between registries",
	Args:  copyArgs,
	Example: `  oci-patch copy docker://registry.example.com/app:latest docker://registry.example.com/app:patched
  oci-patch copy --src-creds user:pass --dest-creds user:pass docker://src/repo:tag docker://dst/repo:tag
  oci-patch copy --progress --recipe inject-tool.yml docker://src/repo:tag docker://dst/repo:tag
  oci-patch copy --batch-file images.yml --src-insecure --dest-insecure`,
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
	copyCmd.Flags().BoolVar(&continueOnError, "continue-on-error", false, "continue batch processing after a copy fails")
	copyCmd.Flags().BoolVar(&srcInsecure, "src-insecure", false, "skip TLS certificate verification for source registry")
	copyCmd.Flags().BoolVar(&destInsecure, "dest-insecure", false, "skip TLS certificate verification for destination registry")
	copyCmd.Flags().StringArrayVar(&destTags, "dest-tag", nil, "additional destination tag in the same repository (may be specified multiple times)")

	// Legacy --from/--to flags (hidden, for oci-patch native style)
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
	if batchFile != "" {
		if len(args) != 0 {
			return fmt.Errorf("--batch-file cannot be combined with positional SOURCE DESTINATION")
		}
		if fromRef != "" || toRef != "" {
			return fmt.Errorf("--batch-file cannot be combined with --from/--to")
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

func runCopy(_ *cobra.Command, args []string) error {
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
		return runCopyBatch(batch)
	}

	if src == "" || dst == "" {
		return fmt.Errorf("source and destination are required (provide as positional arguments or --from/--to)")
	}

	return runCopyOne(src, dst, "", destTags)
}

type copyBatch struct {
	Copy []copyBatchItem `yaml:"copy"`
}

type copyBatchItem struct {
	Src         string `yaml:"src"`
	Source      string `yaml:"source"`
	Dest        string `yaml:"dest"`
	Destination string `yaml:"destination"`
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
		if prev, ok := seenDest[item.target()]; ok {
			return nil, fmt.Errorf("batch file %q copy[%d] duplicates destination from copy[%d]: %s", path, i, prev, item.target())
		}
		seenDest[item.target()] = i
	}

	return batch.Copy, nil
}

func runCopyBatch(batch []copyBatchItem) error {
	var failures []copyBatchFailure
	for i, item := range batch {
		label := fmt.Sprintf("copy %d/%d", i+1, len(batch))
		if !showProgress {
			slog.Info("running batch copy", "job", label, "source", item.source(), "destination", item.target())
		}
		if err := runCopyOne(item.source(), item.target(), label, nil); err != nil {
			failures = append(failures, copyBatchFailure{Index: i + 1, Item: item, Err: err})
			if !continueOnError {
				return fmt.Errorf("%s failed: %w", label, err)
			}
			if !showProgress {
				slog.Error("batch copy failed; continuing", "job", label, "source", item.source(), "destination", item.target(), "err", err)
			}
		}
	}
	if len(failures) > 0 {
		return copyBatchError{Failures: failures}
	}
	return nil
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

func runCopyOne(src, dst, batchLabel string, additionalDestTags []string) error {
	srcOpts := auth.RemoteOptions(parseCreds(srcCreds))
	dstOpts := auth.RemoteOptions(parseCreds(destCreds))
	if srcInsecure {
		srcOpts = append(srcOpts, auth.InsecureTransport())
	}
	if destInsecure {
		dstOpts = append(dstOpts, auth.InsecureTransport())
	}

	// --- setup progress UI ---
	srcIsDaemon := isDaemonRef(src)
	dstIsDaemon := isDaemonRef(dst)
	isFileDst := strings.HasPrefix(dst, "file://")
	if len(additionalDestTags) > 0 && (isFileDst || dstIsDaemon) {
		return fmt.Errorf("--dest-tag is supported only for registry destinations")
	}
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
		if showProgress {
			if summary := phaseTimer.Summary(); summary != "" {
				fmt.Fprint(os.Stderr, summary)
			}
		}
	}
	if showProgress {
		srcLabel := shortRef(src)
		switch {
		case isFileDst:
			filePath := strings.TrimPrefix(dst, "file://")
			ui = progress.New("pull: "+srcLabel, "save: "+filePath)
		case dstIsDaemon:
			ui = progress.NewSingle("pull: " + srcLabel)
		default:
			dstLabel := shortRef(dst)
			ui = progress.New("pull: "+srcLabel, "push: "+dstLabel)
		}
		ui.Run()
		srcCh = ui.SrcChannel()
		if !dstIsDaemon {
			dstCh = ui.DstChannel()
		}
		if !srcIsDaemon {
			srcOpts = append(srcOpts, remote.WithProgress(srcCh))
		}
	} else if showProgressJSON {
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

	// --- load source image ---
	var (
		img      v1.Image
		srcLabel string
		err      error
	)
	if srcIsDaemon {
		srcTag, err := parseDaemonTag(src)
		if err != nil {
			closeProg(srcCh, dstCh)
			if jsonEmitter != nil {
				jsonEmitter.Error("invalid daemon source reference: " + err.Error())
			}
			return fmt.Errorf("invalid daemon source reference: %w", err)
		}
		srcLabel = srcTag.String()
		if debugMode {
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
		if debugMode {
			slog.Debug("pulling source image", "ref", srcParsed.String())
		}
		setPhase("pulling source image")
		if jsonEmitter != nil {
			jsonEmitter.Phase("pull", srcParsed.String())
		}
		img, err = remote.Image(srcParsed, srcOpts...)
		if err != nil {
			closeProg(srcCh, dstCh)
			if jsonEmitter != nil {
				jsonEmitter.Error("pulling source image: " + err.Error())
			}
			return fmt.Errorf("pulling source image: %w", err)
		}
	}

	// go-containerregistry pull is lazy — remote.WithProgress only fires during push (write.go).
	// For pull progress we manually stream all layers and report bytes to srcCh ourselves.
	if showProgress || showProgressJSON || isFileDst {
		if err := eagerPull(img, srcCh); err != nil {
			closeProg(srcCh, dstCh)
			if jsonEmitter != nil {
				jsonEmitter.Error("pulling layers: " + err.Error())
			}
			return fmt.Errorf("pulling layers: %w", err)
		}
	}
	sourceImg := img

	// --- apply recipes ---
	var reduceBase v1.Image
	for _, path := range recipePaths {
		r, err := recipe.Load(path)
		if err != nil {
			closeProg(srcCh, dstCh)
			return err
		}

		if !showProgress {
			slog.Info("applying recipe", "name", r.Name)
		}

		ctx, err := recipe.ResolveContextSources(r, src, img)
		if err != nil {
			closeProg(srcCh, dstCh)
			if jsonEmitter != nil {
				jsonEmitter.Error("resolving recipe sources: " + err.Error())
			}
			return fmt.Errorf("resolving recipe sources: %w", err)
		}
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
		close(srcCh)
		srcCh = nil
	}

	// --- squash layers if requested ---
	if reducelayers {
		if !showProgress {
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
	if reduceChanges {
		if !showProgress {
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
	if strings.HasPrefix(dst, "file://") {
		filePath := strings.TrimPrefix(dst, "file://")
		// use source reference as image tag inside the tarball
		tag, tagErr := name.NewTag(stripScheme(src))
		if tagErr != nil {
			closeProg(nil, dstCh)
			return fmt.Errorf("deriving tag from source: %w", tagErr)
		}
		if debugMode {
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
			close(dstCh)
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
		if debugMode {
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
			close(srcCh)
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
	if destNoReuse {
		img = image.WithoutMountReuse(img)
	}
	if dstCh != nil {
		writeOpts = append(writeOpts, remote.WithProgress(dstCh))
	}

	if debugMode {
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
			close(dstCh)
			dstCh = nil
		}
		if err := tagAdditionalDestTags(dstParsed, img, dstOpts, additionalDestTags, setPhase, jsonEmitter); err != nil {
			return err
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
		close(srcCh)
	}
	if dstCh != nil {
		close(dstCh)
	}
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

func parseDaemonTag(ref string) (name.Tag, error) {
	ref = strings.TrimPrefix(ref, "docker-daemon://")
	ref = strings.TrimPrefix(ref, "daemon://")
	return name.NewTag(ref, name.WeakValidation)
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
