package recipe

import (
	"fmt"
	"log/slog"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/martinmares/oci-toolbox/internal/auth"
	"github.com/martinmares/oci-toolbox/internal/image"
	"github.com/martinmares/oci-toolbox/internal/shadow"
)

// Context holds named image sources resolved before execution.
type Context struct {
	Sources                  map[string]v1.Image
	ShadowEntrypointOverride *ShadowEntrypointOverride
}

// ShadowEntrypointOverride supplies startup metadata that shadow should wrap
// instead of the current working image's metadata.
type ShadowEntrypointOverride struct {
	Entrypoint []string
	Cmd        []string
}

// Apply applies all steps from a recipe to the given working image.
// workingImg is the image being built (initially the --from image or a base).
func Apply(workingImg v1.Image, r *Recipe, ctx *Context) (v1.Image, error) {
	return ApplyWithReporter(workingImg, r, ctx, nil)
}

// ApplyWithReporter applies all steps and optionally reports human-readable progress.
func ApplyWithReporter(workingImg v1.Image, r *Recipe, ctx *Context, report func(string)) (v1.Image, error) {
	img := workingImg

	for i, step := range r.Steps {
		var err error
		slog.Debug("recipe step", "recipe", r.Name, "step", i+1, "action", step.Action)
		if report != nil {
			name := r.Name
			if name == "" {
				name = "unnamed recipe"
			}
			report(fmt.Sprintf("recipe %q step %d/%d: %s", name, i+1, len(r.Steps), step.Action))
		}

		switch step.Action {

		case "use-base":
			src, err := resolveSource(step.From, ctx, r)
			if err != nil {
				return nil, stepErr(i, step.Action, err)
			}
			img = src

		case "add-file":
			if step.Condition != nil && len(step.Condition.IfNotExistsIn) > 0 {
				img, err = image.AddFileLayerConditionalInDirs(img, step.Src, step.Dst, step.Mode, step.Owner, step.Condition.IfNotExistsIn)
			} else {
				condPath := ""
				if step.Condition != nil {
					condPath = step.Condition.IfNotExists
				}
				img, err = image.AddFileLayerConditional(img, step.Src, step.Dst, step.Mode, step.Owner, condPath)
			}

		case "add-dir":
			img, err = image.AddDirLayer(img, step.Src, step.Dst, step.Mode, step.Owner)

		case "remove-file":
			img, err = image.RemoveFile(img, step.Dst)

		case "remove-dir":
			img, err = image.RemoveFile(img, step.Dst)

		case "replace-file":
			img, err = image.ReplaceFile(img, step.Src, step.Dst, step.Mode, step.Owner, step.FindAll)

		case "copy-file-from":
			src, srcErr := resolveSource(step.From, ctx, r)
			if srcErr != nil {
				return nil, stepErr(i, step.Action, srcErr)
			}
			img, err = image.CopyFileFrom(img, src, step.Src, step.Dst, step.Mode, step.Owner)

		case "extract-file":
			src, srcErr := resolveSource(step.From, ctx, r)
			if srcErr != nil {
				return nil, stepErr(i, step.Action, srcErr)
			}
			err = image.ExtractFileToLocal(src, step.Src, step.DstLocal)

		case "unzip-to":
			img, err = image.UnzipTo(img, step.Src, step.Dst, step.Owner)

		case "set-env":
			img, err = image.SetEnv(img, step.Key, step.Value)

		case "set-label":
			img, err = image.SetLabel(img, step.Key, step.Value)

		case "set-entrypoint":
			img, err = image.SetEntrypoint(img, step.Argv)

		case "set-cmd":
			img, err = image.SetCmd(img, step.Argv)

		case "set-user":
			img, err = image.SetUser(img, step.Value)

		case "set-workdir":
			img, err = image.SetWorkdir(img, step.Value)

		case "fix-permissions":
			img, err = image.FixPermissions(img, step.Path, step.Owner, step.Mode, step.SetgidDirs)

		case "shadow":
			output := step.ShadowOutput
			if output == "" {
				output = "/app/app.self"
			}
			src := img
			if step.From != "" {
				src, err = resolveSource(step.From, ctx, r)
				if err != nil {
					return nil, stepErr(i, step.Action, err)
				}
			}
			img, err = shadow.Apply(img, src, step.Path, step.ShadowExtractTo, output)
			if err == nil && step.ShadowWrapEntrypoint != nil && *step.ShadowWrapEntrypoint {
				if ctx.ShadowEntrypointOverride != nil {
					img, err = image.SetEntrypoint(img, ctx.ShadowEntrypointOverride.Entrypoint)
				}
				if err == nil && ctx.ShadowEntrypointOverride != nil {
					img, err = image.SetCmd(img, ctx.ShadowEntrypointOverride.Cmd)
				}
				if err != nil {
					break
				}
				img, err = image.WrapEntrypointWithBootstrap(img, output)
			}

		default:
			return nil, stepErr(i, step.Action, fmt.Errorf("unknown action"))
		}

		if err != nil {
			return nil, stepErr(i, step.Action, err)
		}
	}

	return img, nil
}

// ResolveContextSources pulls all named sources defined in the recipe.
func ResolveContextSources(r *Recipe, mainRef string, mainImg v1.Image, transportOpts ...remote.Option) (*Context, error) {
	ctx := &Context{Sources: make(map[string]v1.Image)}

	for _, src := range r.Sources {
		ref := src.Ref

		// template: {{.SourceImage}} = the CLI --from image
		if ref == "{{.SourceImage}}" {
			ctx.Sources[src.ID] = mainImg
			continue
		}

		creds := registryCreds(r, ref)
		opts := auth.RemoteOptions(creds)
		opts = append(opts, transportOpts...)

		img, err := pullImage(ref, opts)
		if err != nil {
			return nil, fmt.Errorf("pulling source %q (%s): %w", src.ID, ref, err)
		}

		ctx.Sources[src.ID] = img
	}

	return ctx, nil
}

func resolveSource(id string, ctx *Context, r *Recipe) (v1.Image, error) {
	if ctx == nil {
		return nil, fmt.Errorf("no context — sources not resolved")
	}
	img, ok := ctx.Sources[id]
	if !ok {
		return nil, fmt.Errorf("source %q not defined in recipe sources", id)
	}
	return img, nil
}

// ResolveSource returns a named source image from an already prepared context.
func ResolveSource(id string, ctx *Context) (v1.Image, error) {
	return resolveSource(id, ctx, nil)
}

func registryCreds(r *Recipe, ref string) *auth.Options {
	for _, reg := range r.Registries {
		if len(ref) >= len(reg.URL) && ref[:len(reg.URL)] == reg.URL {
			return &auth.Options{
				Username: reg.Username,
				Password: reg.Password,
				AuthFile: reg.AuthFile,
			}
		}
	}
	return nil
}

func pullImage(ref string, opts []remote.Option) (v1.Image, error) {
	ref = stripScheme(ref)

	parsed, err := parseRef(ref)
	if err != nil {
		return nil, err
	}

	return remote.Image(parsed, opts...)
}

func stepErr(i int, action string, err error) error {
	return fmt.Errorf("step %d (%s): %w", i+1, action, err)
}
