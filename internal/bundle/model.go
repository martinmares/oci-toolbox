package bundle

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strings"
	"unicode"

	"github.com/google/go-containerregistry/pkg/name"
	"gopkg.in/yaml.v3"
)

type Bundle struct {
	Name        string     `yaml:"name"`
	Description string     `yaml:"description,omitempty"`
	Registries  Registries `yaml:"registries,omitempty"`
	ExtraTags   []string   `yaml:"extra_tags,omitempty"`
	Images      []Image    `yaml:"images"`
}

type Registries struct {
	Source Registry `yaml:"source,omitempty"`
	Target Registry `yaml:"target,omitempty"`
}

type Registry struct {
	URL     string `yaml:"url,omitempty"`
	Project string `yaml:"project,omitempty"`
}

type Image struct {
	ID         string     `yaml:"id"`
	Source     Source     `yaml:"source"`
	Target     Target     `yaml:"target"`
	Deployment Deployment `yaml:"deployment"`
	ExtraTags  []string   `yaml:"extra_tags,omitempty"`
}

type Source struct {
	Repository string `yaml:"repository"`
	Tag        string `yaml:"tag,omitempty"`
	Digest     string `yaml:"digest,omitempty"`
}

type Target struct {
	Repository string `yaml:"repository"`
}

type Deployment struct {
	AppName       string `yaml:"app_name"`
	ContainerName string `yaml:"container_name,omitempty"`
}

type ResolveOptions struct {
	ReleaseID      string
	SourceRegistry string
	TargetRegistry string
	SourceProject  string
	TargetProject  string
	ExtraTags      []string
}

type Plan struct {
	Name           string
	ReleaseID      string
	SourceRegistry string
	TargetRegistry string
	SourceProject  string
	TargetProject  string
	ExtraTags      []string
	Images         []PlanImage
}

type PlanImage struct {
	ID              string
	SourceRef       string
	SourceImage     string
	RequestedTag    string
	TargetImage     string
	TargetRef       string
	AppName         string
	ContainerName   string
	ExtraTags       []string
	FromBaseRelease bool
}

func Load(path string) (*Bundle, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading bundle %q: %w", path, err)
	}
	defer f.Close()

	b, err := Decode(f)
	if err != nil {
		return nil, fmt.Errorf("parsing bundle %q: %w", path, err)
	}
	return b, nil
}

func Decode(r io.Reader) (*Bundle, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)

	var b Bundle
	if err := dec.Decode(&b); err != nil {
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, errors.New("multiple YAML documents are not allowed")
		}
		return nil, err
	}
	if err := b.Validate(); err != nil {
		return nil, err
	}
	return &b, nil
}

// Encode writes a bundle as a single, consistently indented YAML document.
func Encode(w io.Writer, b *Bundle) error {
	enc := yaml.NewEncoder(w)
	enc.SetIndent(2)
	defer enc.Close()
	return enc.Encode(b)
}

func (b *Bundle) Validate() error {
	if err := requiredText("name", b.Name); err != nil {
		return err
	}
	if err := cleanText("description", b.Description); err != nil {
		return err
	}
	if len(b.Images) == 0 {
		return errors.New("images must contain at least one item")
	}
	for _, field := range []textField{
		{name: "registries.source.url", value: b.Registries.Source.URL},
		{name: "registries.source.project", value: b.Registries.Source.Project},
		{name: "registries.target.url", value: b.Registries.Target.URL},
		{name: "registries.target.project", value: b.Registries.Target.Project},
	} {
		if err := cleanText(field.name, field.value); err != nil {
			return err
		}
	}
	if err := validateTags("extra_tags", b.ExtraTags); err != nil {
		return err
	}

	ids := map[string]struct{}{}
	selectors := map[string]struct{}{}
	targets := map[string]struct{}{}
	for i := range b.Images {
		img := &b.Images[i]
		prefix := fmt.Sprintf("images[%d]", i)
		for _, field := range []textField{
			{name: prefix + ".id", value: img.ID},
			{name: prefix + ".source.repository", value: img.Source.Repository},
			{name: prefix + ".target.repository", value: img.Target.Repository},
			{name: prefix + ".deployment.app_name", value: img.Deployment.AppName},
			{name: prefix + ".deployment.container_name", value: img.Deployment.ContainerName},
			{name: prefix + ".source.tag", value: img.Source.Tag},
			{name: prefix + ".source.digest", value: img.Source.Digest},
		} {
			if err := cleanText(field.name, field.value); err != nil {
				return err
			}
		}
		for _, field := range []textField{
			{name: prefix + ".id", value: img.ID},
			{name: prefix + ".source.repository", value: img.Source.Repository},
			{name: prefix + ".target.repository", value: img.Target.Repository},
			{name: prefix + ".deployment.app_name", value: img.Deployment.AppName},
		} {
			if strings.TrimSpace(field.value) == "" {
				return fmt.Errorf("%s is required", field.name)
			}
		}
		if (img.Source.Tag == "") == (img.Source.Digest == "") {
			return fmt.Errorf("%s.source must contain exactly one of tag or digest", prefix)
		}
		if err := validateRepository(prefix+".source.repository", img.Source.Repository); err != nil {
			return err
		}
		if err := validateRepository(prefix+".target.repository", img.Target.Repository); err != nil {
			return err
		}
		if img.Source.Tag != "" {
			if err := validateTag(prefix+".source.tag", img.Source.Tag); err != nil {
				return err
			}
		}
		if img.Source.Digest != "" {
			if _, err := name.NewDigest("example.invalid/repository@" + img.Source.Digest); err != nil {
				return fmt.Errorf("%s.source.digest is invalid: %w", prefix, err)
			}
		}
		if err := validateTags(prefix+".extra_tags", img.ExtraTags); err != nil {
			return err
		}

		if _, exists := ids[img.ID]; exists {
			return fmt.Errorf("duplicate image id %q", img.ID)
		}
		ids[img.ID] = struct{}{}

		selector := img.Deployment.AppName + "\x00" + img.Deployment.ContainerName
		if _, exists := selectors[selector]; exists {
			return fmt.Errorf("duplicate deployment selector %q/%q", img.Deployment.AppName, img.Deployment.ContainerName)
		}
		selectors[selector] = struct{}{}

		target := strings.Trim(img.Target.Repository, "/")
		if _, exists := targets[target]; exists {
			return fmt.Errorf("duplicate target repository %q", target)
		}
		targets[target] = struct{}{}
	}
	return nil
}

func Resolve(b *Bundle, opts ResolveOptions) (*Plan, error) {
	if err := b.Validate(); err != nil {
		return nil, err
	}
	releaseID := strings.TrimSpace(opts.ReleaseID)
	if releaseID == "" {
		return nil, errors.New("release ID is required")
	}
	if err := validateTag("release ID", releaseID); err != nil {
		return nil, err
	}

	srcRegistry := firstNonEmpty(opts.SourceRegistry, b.Registries.Source.URL)
	dstRegistry := firstNonEmpty(opts.TargetRegistry, b.Registries.Target.URL)
	if srcRegistry == "" {
		return nil, errors.New("source registry is required in bundle or --src-registry")
	}
	if dstRegistry == "" {
		return nil, errors.New("target registry is required in bundle or --dest-registry")
	}
	srcProject := firstNonEmpty(opts.SourceProject, b.Registries.Source.Project)
	dstProject := firstNonEmpty(opts.TargetProject, b.Registries.Target.Project)
	globalTags, err := mergeTags(releaseID, b.ExtraTags, opts.ExtraTags)
	if err != nil {
		return nil, err
	}

	p := &Plan{
		Name:           b.Name,
		ReleaseID:      releaseID,
		SourceRegistry: normalizeRegistry(srcRegistry),
		TargetRegistry: normalizeRegistry(dstRegistry),
		SourceProject:  strings.Trim(srcProject, "/"),
		TargetProject:  strings.Trim(dstProject, "/"),
		ExtraTags:      globalTags,
		Images:         make([]PlanImage, 0, len(b.Images)),
	}
	for i := range b.Images {
		img := &b.Images[i]
		sourceImage := joinReference(p.SourceRegistry, srcProject, img.Source.Repository)
		targetImage := joinReference(p.TargetRegistry, dstProject, img.Target.Repository)
		sourceRef := sourceImage
		switch {
		case img.Source.Tag != "":
			sourceRef += ":" + img.Source.Tag
		case img.Source.Digest != "":
			sourceRef += "@" + img.Source.Digest
		}
		if _, err := name.ParseReference(sourceRef); err != nil {
			return nil, fmt.Errorf("image %q source reference is invalid: %w", img.ID, err)
		}
		targetRef := targetImage + ":" + releaseID
		if _, err := name.NewTag(targetRef); err != nil {
			return nil, fmt.Errorf("image %q target reference is invalid: %w", img.ID, err)
		}
		imageTags, err := mergeTags(releaseID, globalTags, img.ExtraTags)
		if err != nil {
			return nil, fmt.Errorf("image %q: %w", img.ID, err)
		}
		p.Images = append(p.Images, PlanImage{
			ID:            img.ID,
			SourceRef:     sourceRef,
			SourceImage:   sourceImage,
			RequestedTag:  img.Source.Tag,
			TargetImage:   targetImage,
			TargetRef:     targetRef,
			AppName:       img.Deployment.AppName,
			ContainerName: img.Deployment.ContainerName,
			ExtraTags:     imageTags,
		})
	}
	return p, nil
}

func InferredName(image string) string {
	return path.Base(strings.Trim(strings.TrimSpace(image), "/"))
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func normalizeRegistry(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "docker://")
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimPrefix(value, "http://")
	return strings.Trim(value, "/")
}

func joinReference(registry, project, repository string) string {
	parts := []string{normalizeRegistry(registry)}
	if project = strings.Trim(project, "/"); project != "" {
		parts = append(parts, project)
	}
	parts = append(parts, strings.Trim(repository, "/"))
	return strings.Join(parts, "/")
}

func mergeTags(releaseID string, groups ...[]string) ([]string, error) {
	var result []string
	for _, group := range groups {
		for _, tag := range group {
			tag = strings.TrimSpace(tag)
			if tag == "" || tag == releaseID || slices.Contains(result, tag) {
				continue
			}
			if err := validateTag("extra tag", tag); err != nil {
				return nil, err
			}
			result = append(result, tag)
		}
	}
	return result, nil
}

func validateTags(field string, tags []string) error {
	for i, tag := range tags {
		if err := cleanText(fmt.Sprintf("%s[%d]", field, i), tag); err != nil {
			return err
		}
		if strings.TrimSpace(tag) == "" {
			return fmt.Errorf("%s[%d] must not be empty", field, i)
		}
		if err := validateTag(fmt.Sprintf("%s[%d]", field, i), tag); err != nil {
			return err
		}
	}
	return nil
}

func validateTag(field, tag string) error {
	if _, err := name.NewTag("example.invalid/repository:" + strings.TrimSpace(tag)); err != nil {
		return fmt.Errorf("%s is invalid: %w", field, err)
	}
	return nil
}

func validateRepository(field, repository string) error {
	repository = strings.Trim(strings.TrimSpace(repository), "/")
	if strings.Contains(repository, "://") || strings.Contains(repository, "@") {
		return fmt.Errorf("%s must be a repository path without registry scheme, tag, or digest", field)
	}
	if _, err := name.NewRepository("example.invalid/" + repository); err != nil {
		return fmt.Errorf("%s is invalid: %w", field, err)
	}
	return nil
}

func requiredText(field, value string) error {
	if err := cleanText(field, value); err != nil {
		return err
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is required", field)
	}
	return nil
}

type textField struct {
	name  string
	value string
}

func cleanText(field, value string) error {
	for _, r := range value {
		if unicode.IsControl(r) {
			return fmt.Errorf("%s contains a control character", field)
		}
	}
	return nil
}
