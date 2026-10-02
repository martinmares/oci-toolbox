package bundle

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/google/go-containerregistry/pkg/name"
	"gopkg.in/yaml.v3"
)

// DecodeReleaseDocument normalizes supported input formats. Callers must validate
// the result for their operation; copy retains its existing validation contract.
func DecodeReleaseDocument(r io.Reader) (*ReleaseManifest, error) {
	var doc yaml.Node
	dec := yaml.NewDecoder(r)
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple YAML documents are not allowed")
		}
		return nil, err
	}
	var header struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
	}
	if err := doc.Decode(&header); err != nil {
		return nil, err
	}
	headerPresent := false
	if len(doc.Content) > 0 && doc.Content[0].Kind == yaml.MappingNode {
		fields := doc.Content[0].Content
		for i := 0; i < len(fields); i += 2 {
			if fields[i].Value == "apiVersion" || fields[i].Value == "kind" {
				headerPresent = true
			}
		}
	}
	if headerPresent {
		switch {
		case header.APIVersion == "release.tsm.datalite.cz/v1alpha1" && header.Kind == "TsmProductRelease":
			return decodeProductRelease(&doc)
		case header.APIVersion == ReleaseAPIVersion && header.Kind == ReleaseKind:
			// Internal v1 uses the same strict schema as legacy unversioned inputs.
		default:
			return nil, fmt.Errorf("unsupported release format %q / %q", header.APIVersion, header.Kind)
		}
	}

	raw, err := yaml.Marshal(&doc)
	if err != nil {
		return nil, err
	}
	strict := yaml.NewDecoder(bytes.NewReader(raw))
	strict.KnownFields(true)
	var result ReleaseManifest
	if err := strict.Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

type productContainer struct {
	Image  string `yaml:"image"`
	Tag    string `yaml:"tag"`
	Digest string `yaml:"digest"`
}

func decodeProductRelease(doc *yaml.Node) (*ReleaseManifest, error) {
	var input struct {
		Metadata struct {
			Name      string    `yaml:"name"`
			CreatedAt time.Time `yaml:"createdAt"`
		} `yaml:"metadata"`
		Spec struct {
			Version struct {
				Product string `yaml:"product"`
			} `yaml:"version"`
			ReleaseTag string                      `yaml:"releaseTag"`
			Containers map[string]productContainer `yaml:"containers"`
			Frontend   struct {
				Apps map[string]productContainer `yaml:"apps"`
			} `yaml:"frontend"`
		} `yaml:"spec"`
	}
	// Maven, sources and verification are product metadata, not image inputs.
	if err := doc.Decode(&input); err != nil {
		return nil, err
	}
	id := strings.TrimSpace(input.Spec.Version.Product)
	if id == "" {
		id = strings.TrimSpace(input.Metadata.Name)
	}
	if id == "" {
		return nil, errors.New("product release needs spec.version.product or metadata.name")
	}
	if input.Metadata.Name != "" && input.Spec.Version.Product != "" && input.Metadata.Name != input.Spec.Version.Product {
		return nil, errors.New("metadata.name differs from spec.version.product")
	}
	result := &ReleaseManifest{ReleaseID: id, CreatedAt: input.Metadata.CreatedAt,
		Bundle: &ReleaseBundle{Name: id, Revision: input.Spec.ReleaseTag}}
	repositories := []string{}
	appendItems := func(items map[string]productContainer, frontend bool) error {
		keys := make([]string, 0, len(items))
		for key := range items {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			item := items[key]
			repo, err := name.NewRepository(item.Image, name.WeakValidation)
			if err != nil || item.Image == "" {
				return fmt.Errorf("container %q has invalid image %q", key, item.Image)
			}
			// Require a repository, rather than a tag/digest embedded in image.
			if repo.Name() != item.Image || strings.ContainsAny(repo.RepositoryStr(), ":@") {
				return fmt.Errorf("container %q image must be a fully qualified repository", key)
			}
			app, imageID := key, key
			if frontend {
				app = path.Base(repo.RepositoryStr())
				imageID = "frontend-" + key
			}
			result.Images = append(result.Images, ReleaseImage{ID: imageID, AppName: app, ContainerName: app,
				Image: item.Image, Tag: item.Tag, Digest: item.Digest,
				Source: &ReleaseSource{Image: item.Image, Tag: item.Tag, Digest: item.Digest}})
			repositories = append(repositories, item.Image)
		}
		return nil
	}
	if err := appendItems(input.Spec.Containers, false); err != nil {
		return nil, err
	}
	if err := appendItems(input.Spec.Frontend.Apps, true); err != nil {
		return nil, err
	}
	if len(repositories) == 0 {
		return nil, errors.New("product release contains no container images")
	}
	// Multiple registries cannot share registry_base; copy then requires --src-base
	// and verifies that every image belongs to it, preserving the old behavior.
	registry, project, _, err := splitRepositorySet("product", repositories)
	if err == nil {
		result.RegistryBase = registry
		if project != "" {
			result.RegistryBase += "/" + project
		}
	}
	return result, nil
}
