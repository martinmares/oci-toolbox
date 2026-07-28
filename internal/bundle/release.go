package bundle

import (
	"errors"
	"fmt"
	"io"
	"time"

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
	Image  string `yaml:"image"`
	Tag    string `yaml:"tag,omitempty"`
	Digest string `yaml:"digest"`
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
