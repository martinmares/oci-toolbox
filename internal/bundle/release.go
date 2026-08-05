package bundle

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"gopkg.in/yaml.v3"
)

type ReleaseManifest struct {
	ReleaseID    string         `yaml:"release_id"`
	CreatedAt    time.Time      `yaml:"created_at"`
	Bundle       *ReleaseBundle `yaml:"bundle,omitempty"`
	RegistryBase string         `yaml:"registry_base,omitempty"`
	Platform     string         `yaml:"platform,omitempty"`
	Images       []ReleaseImage `yaml:"images"`
	ExtraTags    []string       `yaml:"extra_tags,omitempty"`
}

type ReleaseBundle struct {
	Name     string `yaml:"name"`
	Revision string `yaml:"revision,omitempty"`
}

type ReleaseImage struct {
	ID            string         `yaml:"id,omitempty"`
	AppName       string         `yaml:"app_name"`
	ContainerName string         `yaml:"container_name,omitempty"`
	Source        *ReleaseSource `yaml:"source,omitempty"`
	Image         string         `yaml:"image"`
	Tag           string         `yaml:"tag,omitempty"`
	Digest        string         `yaml:"digest"`
	ExtraTags     []string       `yaml:"extra_tags,omitempty"`
	Platform      string         `yaml:"platform,omitempty"`
}

type ReleaseSource struct {
	Image  string `yaml:"image" json:"image"`
	Tag    string `yaml:"tag,omitempty" json:"tag,omitempty"`
	Digest string `yaml:"digest" json:"digest"`
}

type ReconstructFile struct {
	Images []ReconstructImage `yaml:"images"`
}

type ReconstructImage struct {
	Ref           string `yaml:"ref"`
	ID            string `yaml:"id,omitempty"`
	AppName       string `yaml:"app_name,omitempty"`
	ContainerName string `yaml:"container_name,omitempty"`
}

func LoadRelease(path string) (*ReleaseManifest, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading release manifest %q: %w", path, err)
	}
	defer f.Close()
	manifest, err := DecodeRelease(f)
	if err != nil {
		return nil, fmt.Errorf("parsing release manifest %q: %w", path, err)
	}
	return manifest, nil
}

func DecodeRelease(r io.Reader) (*ReleaseManifest, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	var manifest ReleaseManifest
	if err := dec.Decode(&manifest); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple YAML documents are not allowed")
		}
		return nil, err
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func (m *ReleaseManifest) Validate() error {
	if err := requiredText("release_id", m.ReleaseID); err != nil {
		return err
	}
	if _, err := name.NewTag("example.invalid/repository:"+strings.TrimSpace(m.ReleaseID), name.WeakValidation); err != nil {
		return fmt.Errorf("release_id %q is invalid: %w", m.ReleaseID, err)
	}
	if len(m.Images) == 0 {
		return errors.New("images must contain at least one item")
	}
	selectors := make(map[string]struct{}, len(m.Images))
	ids := make(map[string]struct{}, len(m.Images))
	for i := range m.Images {
		item := &m.Images[i]
		prefix := fmt.Sprintf("images[%d]", i)
		for _, field := range []textField{
			{name: prefix + ".id", value: item.ID},
			{name: prefix + ".app_name", value: item.AppName},
			{name: prefix + ".container_name", value: item.ContainerName},
			{name: prefix + ".image", value: item.Image},
			{name: prefix + ".tag", value: item.Tag},
			{name: prefix + ".digest", value: item.Digest},
		} {
			if err := cleanText(field.name, field.value); err != nil {
				return err
			}
		}
		for _, field := range []textField{
			{name: prefix + ".app_name", value: item.AppName},
			{name: prefix + ".image", value: item.Image},
			{name: prefix + ".digest", value: item.Digest},
		} {
			if strings.TrimSpace(field.value) == "" {
				return fmt.Errorf("%s is required", field.name)
			}
		}
		if _, err := name.NewDigest(strings.TrimSpace(item.Image)+"@"+strings.TrimSpace(item.Digest), name.WeakValidation); err != nil {
			return fmt.Errorf("%s digest is invalid: %w", prefix, err)
		}
		if item.Tag != "" {
			if _, err := name.NewTag(strings.TrimSpace(item.Image)+":"+strings.TrimSpace(item.Tag), name.WeakValidation); err != nil {
				return fmt.Errorf("%s tag is invalid: %w", prefix, err)
			}
		}
		selector := selectorKey(item.AppName, item.ContainerName)
		if _, exists := selectors[selector]; exists {
			return fmt.Errorf("duplicate deployment selector %q", formatSelector(item.AppName, item.ContainerName))
		}
		selectors[selector] = struct{}{}
		if item.ID != "" {
			if _, exists := ids[item.ID]; exists {
				return fmt.Errorf("duplicate image id %q", item.ID)
			}
			ids[item.ID] = struct{}{}
		}
	}
	return nil
}

func DecodeReconstruct(r io.Reader) (*ReconstructFile, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	var input ReconstructFile
	if err := dec.Decode(&input); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple YAML documents are not allowed")
		}
		return nil, err
	}
	if len(input.Images) == 0 {
		return nil, errors.New("images must contain at least one item")
	}
	for i := range input.Images {
		item := &input.Images[i]
		if err := requiredText(fmt.Sprintf("images[%d].ref", i), item.Ref); err != nil {
			return nil, err
		}
		for _, field := range []textField{
			{name: fmt.Sprintf("images[%d].id", i), value: item.ID},
			{name: fmt.Sprintf("images[%d].app_name", i), value: item.AppName},
			{name: fmt.Sprintf("images[%d].container_name", i), value: item.ContainerName},
		} {
			if err := cleanText(field.name, field.value); err != nil {
				return nil, err
			}
		}
	}
	return &input, nil
}

func EncodeRelease(w io.Writer, manifest *ReleaseManifest) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	defer enc.Close()
	return enc.Encode(manifest)
}
