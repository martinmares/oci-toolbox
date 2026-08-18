package bundle

import (
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
)

type BundleSourceMode string

const (
	BundleSourceDigest BundleSourceMode = "digest"
	BundleSourceTag    BundleSourceMode = "tag"
)

// BundleFromRelease converts immutable release data back into a publishable
// bundle. Registry projects are inferred as the common parent path of all
// source and target repositories.
func BundleFromRelease(manifest *ReleaseManifest, bundleName string, mode BundleSourceMode) (*Bundle, error) {
	if manifest == nil {
		return nil, errors.New("release manifest is required")
	}
	if err := manifest.Validate(); err != nil {
		return nil, fmt.Errorf("invalid release manifest: %w", err)
	}
	bundleName = strings.TrimSpace(bundleName)
	if bundleName == "" {
		return nil, errors.New("bundle name is required")
	}
	if mode != BundleSourceDigest && mode != BundleSourceTag {
		return nil, fmt.Errorf("invalid source mode %q: expected digest or tag", mode)
	}

	sourceImages := make([]string, len(manifest.Images))
	targetImages := make([]string, len(manifest.Images))
	for i, item := range manifest.Images {
		if item.Source == nil {
			return nil, fmt.Errorf("images[%d] (%s) has no source metadata", i, releaseImageSelector(item))
		}
		sourceImages[i] = item.Source.Image
		targetImages[i] = item.Image
	}

	sourceRegistry, sourceProject, sourceRepositories, err := splitRepositorySet("source", sourceImages)
	if err != nil {
		return nil, err
	}
	targetRegistry, targetProject, targetRepositories, err := splitRepositorySet("target", targetImages)
	if err != nil {
		return nil, err
	}

	result := &Bundle{
		Name: bundleName,
		Registries: Registries{
			Source: Registry{URL: sourceRegistry, Project: sourceProject},
			Target: Registry{URL: targetRegistry, Project: targetProject},
		},
		ExtraTags: append([]string(nil), manifest.ExtraTags...),
		Images:    make([]Image, 0, len(manifest.Images)),
	}
	for i, item := range manifest.Images {
		id := strings.TrimSpace(item.ID)
		if id == "" {
			id = path.Base(targetRepositories[i])
		}
		converted := Image{
			ID: id,
			Source: Source{
				Repository: sourceRepositories[i],
			},
			Target: Target{Repository: targetRepositories[i]},
			Deployment: Deployment{
				AppName:       item.AppName,
				ContainerName: item.ContainerName,
			},
			ExtraTags: append([]string(nil), item.ExtraTags...),
		}
		switch mode {
		case BundleSourceDigest:
			converted.Source.Digest = item.Source.Digest
			if converted.Source.Digest == "" {
				return nil, fmt.Errorf("images[%d] (%s) has no source digest", i, releaseImageSelector(item))
			}
		case BundleSourceTag:
			converted.Source.Tag = item.Source.Tag
			if converted.Source.Tag == "" {
				return nil, fmt.Errorf("images[%d] (%s) has no source tag required by --source-mode tag", i, releaseImageSelector(item))
			}
		}
		result.Images = append(result.Images, converted)
	}
	if err := result.Validate(); err != nil {
		return nil, fmt.Errorf("converted bundle is invalid: %w", err)
	}
	return result, nil
}

func splitRepositorySet(kind string, images []string) (string, string, []string, error) {
	if len(images) == 0 {
		return "", "", nil, fmt.Errorf("%s images must not be empty", kind)
	}
	registry := ""
	paths := make([][]string, len(images))
	commonLength := -1
	for i, image := range images {
		repository, err := name.NewRepository(strings.TrimSpace(image), name.WeakValidation)
		if err != nil {
			return "", "", nil, fmt.Errorf("images[%d] %s image %q is invalid: %w", i, kind, image, err)
		}
		if registry == "" {
			registry = repository.RegistryStr()
		} else if repository.RegistryStr() != registry {
			return "", "", nil, fmt.Errorf("%s images use multiple registries: %q and %q", kind, registry, repository.RegistryStr())
		}
		paths[i] = strings.Split(strings.Trim(repository.RepositoryStr(), "/"), "/")
		if len(paths[i]) == 0 || paths[i][0] == "" {
			return "", "", nil, fmt.Errorf("images[%d] %s image %q has no repository", i, kind, image)
		}
		limit := len(paths[i]) - 1 // Always leave a repository name after the project.
		if commonLength < 0 || limit < commonLength {
			commonLength = limit
		}
	}
	for segment := 0; segment < commonLength; segment++ {
		value := paths[0][segment]
		for i := 1; i < len(paths); i++ {
			if paths[i][segment] != value {
				commonLength = segment
				break
			}
		}
		if commonLength == segment {
			break
		}
	}
	project := strings.Join(paths[0][:commonLength], "/")
	repositories := make([]string, len(paths))
	for i := range paths {
		repositories[i] = strings.Join(paths[i][commonLength:], "/")
	}
	return registry, project, repositories, nil
}

func releaseImageSelector(item ReleaseImage) string {
	if item.ContainerName == "" {
		return item.AppName
	}
	return item.AppName + "/" + item.ContainerName
}
