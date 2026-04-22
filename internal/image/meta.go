package image

import (
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
)

// SetEnv sets or overrides an environment variable in the image config.
func SetEnv(img v1.Image, key, value string) (v1.Image, error) {
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, err
	}
	cfg = cfg.DeepCopy()

	entry := key + "=" + value
	for i, e := range cfg.Config.Env {
		if len(e) > len(key) && e[:len(key)+1] == key+"=" {
			cfg.Config.Env[i] = entry
			return mutate.ConfigFile(img, cfg)
		}
	}
	cfg.Config.Env = append(cfg.Config.Env, entry)
	return mutate.ConfigFile(img, cfg)
}

// SetLabel sets an OCI label in the image config.
func SetLabel(img v1.Image, key, value string) (v1.Image, error) {
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, err
	}
	cfg = cfg.DeepCopy()

	if cfg.Config.Labels == nil {
		cfg.Config.Labels = make(map[string]string)
	}
	cfg.Config.Labels[key] = value
	return mutate.ConfigFile(img, cfg)
}

// SetEntrypoint sets the image ENTRYPOINT.
func SetEntrypoint(img v1.Image, argv []string) (v1.Image, error) {
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, err
	}
	cfg = cfg.DeepCopy()
	cfg.Config.Entrypoint = argv
	return mutate.ConfigFile(img, cfg)
}

// SetCmd sets the image CMD.
func SetCmd(img v1.Image, argv []string) (v1.Image, error) {
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, err
	}
	cfg = cfg.DeepCopy()
	cfg.Config.Cmd = argv
	return mutate.ConfigFile(img, cfg)
}

// SetUser sets the image USER.
func SetUser(img v1.Image, user string) (v1.Image, error) {
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, err
	}
	cfg = cfg.DeepCopy()
	cfg.Config.User = user
	return mutate.ConfigFile(img, cfg)
}

// SetWorkdir sets the image WORKDIR.
func SetWorkdir(img v1.Image, dir string) (v1.Image, error) {
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, err
	}
	cfg = cfg.DeepCopy()
	cfg.Config.WorkingDir = dir
	return mutate.ConfigFile(img, cfg)
}

// SetStopSignal sets the image StopSignal.
func SetStopSignal(img v1.Image, signal string) (v1.Image, error) {
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, err
	}
	cfg = cfg.DeepCopy()
	cfg.Config.StopSignal = signal
	return mutate.ConfigFile(img, cfg)
}

// InspectOutput is the JSON-serialisable inspect result (skopeo-compatible).
type InspectOutput struct {
	Name          string        `json:"Name"`
	Digest        string        `json:"Digest"`
	RepoTags      []string      `json:"RepoTags"`
	Created       string        `json:"Created"`
	DockerVersion string        `json:"DockerVersion"`
	Labels        map[string]string `json:"Labels"`
	Architecture  string        `json:"Architecture"`
	Os            string        `json:"Os"`
	Layers        []string      `json:"Layers"`
	LayersData    []LayerData   `json:"LayersData"`
	Env           []string      `json:"Env"`
}

// LayerData holds per-layer metadata (skopeo-compatible).
type LayerData struct {
	MIMEType    string            `json:"MIMEType"`
	Digest      string            `json:"Digest"`
	Size        int64             `json:"Size"`
	Annotations map[string]string `json:"Annotations"`
}

// Inspect returns structured metadata for an image.
// repoTags is optional — pass nil when not fetching tags.
func Inspect(img v1.Image, refName string, repoTags []string) (*InspectOutput, error) {
	cfg, err := img.ConfigFile()
	if err != nil {
		return nil, err
	}
	manifest, err := img.Manifest()
	if err != nil {
		return nil, err
	}
	digest, err := img.Digest()
	if err != nil {
		return nil, err
	}

	layers := make([]string, 0, len(manifest.Layers))
	layersData := make([]LayerData, 0, len(manifest.Layers))
	for _, l := range manifest.Layers {
		layers = append(layers, l.Digest.String())
		layersData = append(layersData, LayerData{
			MIMEType:    string(l.MediaType),
			Digest:      l.Digest.String(),
			Size:        l.Size,
			Annotations: l.Annotations,
		})
	}

	created := ""
	if !cfg.Created.IsZero() {
		created = cfg.Created.UTC().Format("2006-01-02T15:04:05.999999999Z")
	}

	return &InspectOutput{
		Name:          refName,
		Digest:        digest.String(),
		RepoTags:      repoTags,
		Created:       created,
		DockerVersion: cfg.DockerVersion,
		Labels:        cfg.Config.Labels,
		Architecture:  cfg.Architecture,
		Os:            cfg.OS,
		Layers:        layers,
		LayersData:    layersData,
		Env:           cfg.Config.Env,
	}, nil
}
