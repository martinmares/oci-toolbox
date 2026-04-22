package cmd

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/spf13/cobra"

	"github.com/datalite/oci-patch/internal/auth"
	"github.com/datalite/oci-patch/internal/image"
	"github.com/datalite/oci-patch/internal/logging"
	"github.com/datalite/oci-patch/internal/progress"
	"github.com/datalite/oci-patch/internal/recipe"
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
	srcInsecure      bool
	destInsecure     bool
)

var copyCmd = &cobra.Command{
	Use:   "copy SOURCE DESTINATION",
	Short: "Copy and patch an OCI image between registries",
	Args:  cobra.ExactArgs(2),
	Example: `  oci-patch copy docker://registry.example.com/app:latest docker://registry.example.com/app:patched
  oci-patch copy --src-creds user:pass --dest-creds user:pass docker://src/repo:tag docker://dst/repo:tag
  oci-patch copy --progress --recipe inject-tool.yml docker://src/repo:tag docker://dst/repo:tag`,
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
	copyCmd.Flags().BoolVar(&srcInsecure, "src-insecure", false, "skip TLS certificate verification for source registry")
	copyCmd.Flags().BoolVar(&destInsecure, "dest-insecure", false, "skip TLS certificate verification for destination registry")

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

func runCopy(_ *cobra.Command, args []string) error {
	src := fromRef
	dst := toRef
	if len(args) == 2 {
		src = args[0]
		dst = args[1]
	}
	if src == "" || dst == "" {
		return fmt.Errorf("source and destination are required (provide as positional arguments or --from/--to)")
	}

	if showProgress && showProgressJSON {
		return fmt.Errorf("--progress and --progress-json cannot be used together")
	}

	if debugMode {
		logging.Setup(slog.LevelDebug)
	} else {
		logging.Setup(slog.LevelInfo)
	}

	srcOpts := auth.RemoteOptions(parseCreds(srcCreds))
	dstOpts := auth.RemoteOptions(parseCreds(destCreds))
	if srcInsecure {
		srcOpts = append(srcOpts, auth.InsecureTransport())
	}
	if destInsecure {
		dstOpts = append(dstOpts, auth.InsecureTransport())
	}

	// --- setup progress UI ---
	isFileDst := strings.HasPrefix(dst, "file://")
	var ui *progress.UI
	var jsonEmitter *progress.JSONEmitter
	var srcCh, dstCh chan<- v1.Update
	if showProgress {
		srcLabel := shortRef(src)
		if isFileDst {
			filePath := strings.TrimPrefix(dst, "file://")
			ui = progress.New("pull: "+srcLabel, "save: "+filePath)
		} else {
			dstLabel := shortRef(dst)
			ui = progress.New("pull: "+srcLabel, "push: "+dstLabel)
		}
		ui.Run()
		srcCh = ui.SrcChannel()
		dstCh = ui.DstChannel()
		srcOpts = append(srcOpts, remote.WithProgress(srcCh))
	} else if showProgressJSON {
		jsonEmitter = progress.NewJSONEmitter(os.Stdout)
		srcCh = jsonEmitter.Channel("pull")
		if isFileDst {
			dstCh = jsonEmitter.Channel("save")
		} else {
			dstCh = jsonEmitter.Channel("push")
		}
		srcOpts = append(srcOpts, remote.WithProgress(srcCh))
	}

	// --- pull source image ---
	srcParsed, err := name.ParseReference(stripScheme(src))
	if err != nil {
		closeProg(srcCh, dstCh)
		if jsonEmitter != nil {
			jsonEmitter.Error("invalid source reference: " + err.Error())
		}
		return fmt.Errorf("invalid source reference: %w", err)
	}

	if debugMode {
		slog.Debug("pulling source image", "ref", srcParsed.String())
	}
	if ui != nil {
		ui.SetStatus("phase: pulling source image")
	}
	if jsonEmitter != nil {
		jsonEmitter.Phase("pull", srcParsed.String())
	}

	img, err := remote.Image(srcParsed, srcOpts...)
	if err != nil {
		closeProg(srcCh, dstCh)
		if jsonEmitter != nil {
			jsonEmitter.Error("pulling source image: " + err.Error())
		}
		return fmt.Errorf("pulling source image: %w", err)
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

		if ui != nil {
			name := r.Name
			if name == "" {
				name = path
			}
			ui.SetStatus("phase: applying recipe " + name)
		}
		if jsonEmitter != nil {
			name := r.Name
			if name == "" {
				name = path
			}
			jsonEmitter.Phase("recipe", name)
		}
		report := func(text string) {
			if ui != nil {
				ui.SetStatus("phase: " + text)
			}
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
	if srcCh != nil {
		close(srcCh)
		srcCh = nil
	}

	// --- squash layers if requested ---
	if reducelayers {
		if !showProgress {
			slog.Info("reducing layers")
		}
		if ui != nil {
			ui.SetStatus("phase: squashing layers")
		}
		if jsonEmitter != nil {
			jsonEmitter.Phase("squash", "layers")
		}
		if reduceBase != nil {
			img, err = image.ReduceToBase(reduceBase, img)
		} else {
			img, err = image.Squash(img)
		}
		if err != nil {
			closeProg(nil, dstCh)
			if jsonEmitter != nil {
				jsonEmitter.Error("squashing layers: " + err.Error())
			}
			return fmt.Errorf("squashing layers: %w", err)
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
		if ui != nil {
			ui.SetStatus("phase: writing tarball")
		}
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
		} else {
			slog.Info("saved image tarball", "source", srcParsed.String(), "path", filePath)
		}
		if jsonEmitter != nil {
			jsonEmitter.Done("success", "saved image tarball")
		}
		return nil
	}

	dstParsed, err := name.ParseReference(stripScheme(dst))
	if err != nil {
		closeProg(nil, dstCh)
		if jsonEmitter != nil {
			jsonEmitter.Error("invalid destination reference: " + err.Error())
		}
		return fmt.Errorf("invalid destination reference: %w", err)
	}

	writeOpts := dstOpts
	if destNoReuse {
		img = image.WithoutMountReuse(img)
	}
	if showProgress && dstCh != nil {
		writeOpts = append(writeOpts, remote.WithProgress(dstCh))
	}

	if debugMode {
		slog.Debug("pushing destination image", "ref", dstParsed.String())
	}
	if ui != nil {
		ui.SetStatus("phase: pushing destination image")
	}
	if jsonEmitter != nil {
		jsonEmitter.Phase("push", dstParsed.String())
	}

	if err := remote.Write(dstParsed, img, writeOpts...); err != nil {
		closeProg(nil, dstCh)
		if jsonEmitter != nil {
			jsonEmitter.Error("pushing image: " + err.Error())
		}
		return fmt.Errorf("pushing image: %w", err)
	}

	if dstCh != nil {
		close(dstCh)
	}

	if ui != nil {
		ui.Wait()
	} else {
		slog.Info("copied image", "source", srcParsed.String(), "destination", dstParsed.String())
	}
	if jsonEmitter != nil {
		jsonEmitter.Done("success", "copied image")
	}

	return nil
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
	return ref
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
