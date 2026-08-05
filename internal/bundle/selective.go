package bundle

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type SelectionFile struct {
	Selectors []string `yaml:"selectors"`
}

type SelectiveOptions struct {
	PreserveBaseOnly bool
}

func InferTargetRegistry(b *Bundle, base *ReleaseManifest, targetProject string) (string, error) {
	if b == nil || base == nil {
		return "", errors.New("bundle and base release manifest are required")
	}
	project := strings.Trim(strings.TrimSpace(targetProject), "/")
	if project == "" {
		project = strings.Trim(strings.TrimSpace(b.Registries.Target.Project), "/")
	}
	if registryBase := normalizeRepository(base.RegistryBase); registryBase != "" {
		if project == "" {
			return registryBase, nil
		}
		suffix := "/" + project
		if !strings.HasSuffix(registryBase, suffix) {
			return "", fmt.Errorf("base release registry_base %q does not end with target project %q", base.RegistryBase, project)
		}
		return strings.TrimSuffix(registryBase, suffix), nil
	}

	baseBySelector := make(map[string]ReleaseImage, len(base.Images))
	for _, item := range base.Images {
		baseBySelector[selectorKey(item.AppName, item.ContainerName)] = item
	}
	candidate := ""
	matched := 0
	for _, item := range b.Images {
		baseItem, exists := baseBySelector[selectorKey(item.Deployment.AppName, item.Deployment.ContainerName)]
		if !exists {
			continue
		}
		targetPath := strings.Trim(strings.TrimSpace(item.Target.Repository), "/")
		if project != "" {
			targetPath = project + "/" + targetPath
		}
		image := normalizeRepository(baseItem.Image)
		suffix := "/" + targetPath
		if targetPath == "" || !strings.HasSuffix(image, suffix) {
			return "", fmt.Errorf(
				"cannot infer destination registry: base image %q for deployment target %q does not end with target path %q",
				baseItem.Image, formatSelector(baseItem.AppName, baseItem.ContainerName), targetPath,
			)
		}
		inferred := strings.TrimSuffix(image, suffix)
		if candidate == "" {
			candidate = inferred
		} else if candidate != inferred {
			return "", fmt.Errorf("cannot infer one destination registry: deployment targets resolve to both %q and %q", candidate, inferred)
		}
		matched++
	}
	if matched == 0 || candidate == "" {
		return "", errors.New("cannot infer destination registry: bundle and base release have no common deployment targets")
	}
	return candidate, nil
}

func LoadSelectionFile(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading selection file %q: %w", path, err)
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	var input SelectionFile
	if err := dec.Decode(&input); err != nil {
		return nil, fmt.Errorf("parsing selection file %q: %w", path, err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("parsing selection file %q: multiple YAML documents are not allowed", path)
		}
		return nil, fmt.Errorf("parsing selection file %q: %w", path, err)
	}
	if len(input.Selectors) == 0 {
		return nil, fmt.Errorf("selection file %q contains no selectors", path)
	}
	if _, err := parseSelectors(input.Selectors); err != nil {
		return nil, fmt.Errorf("selection file %q: %w", path, err)
	}
	return input.Selectors, nil
}

// ApplyBaseRelease turns a regular bundle plan into a selective release plan.
// Selected deployment targets keep their bundle source; every other target is
// sourced by immutable digest from the base release and receives the new tag.
func ApplyBaseRelease(plan *Plan, base *ReleaseManifest, selectors []string) (*Plan, error) {
	return ApplyBaseReleaseWithOptions(plan, base, selectors, SelectiveOptions{})
}

func ApplyBaseReleaseWithOptions(plan *Plan, base *ReleaseManifest, selectors []string, opts SelectiveOptions) (*Plan, error) {
	if plan == nil {
		return nil, errors.New("bundle plan is required")
	}
	if base == nil {
		return nil, errors.New("base release manifest is required")
	}
	if err := base.Validate(); err != nil {
		return nil, fmt.Errorf("base release manifest: %w", err)
	}
	selected, err := parseSelectors(selectors)
	if err != nil {
		return nil, err
	}
	if len(selected) == 0 {
		return nil, errors.New("at least one --select APP/CONTAINER is required with --base-release")
	}

	baseBySelector := make(map[string]ReleaseImage, len(base.Images))
	for _, item := range base.Images {
		baseBySelector[selectorKey(item.AppName, item.ContainerName)] = item
	}
	planBySelector := make(map[string]PlanImage, len(plan.Images))
	for _, item := range plan.Images {
		planBySelector[selectorKey(item.AppName, item.ContainerName)] = item
	}
	if opts.PreserveBaseOnly {
		return applyBaseReleaseTemplate(plan, base, selected, baseBySelector, planBySelector)
	}
	if len(baseBySelector) != len(planBySelector) {
		return nil, fmt.Errorf("base release contains %d deployment targets, bundle contains %d", len(baseBySelector), len(planBySelector))
	}
	for key := range selected {
		if _, exists := planBySelector[key]; !exists {
			return nil, fmt.Errorf("selected deployment target %q does not exist in bundle", displaySelectorKey(key))
		}
	}
	for key, baseItem := range baseBySelector {
		planItem, exists := planBySelector[key]
		if !exists {
			return nil, fmt.Errorf("base deployment target %q is missing from bundle", formatSelector(baseItem.AppName, baseItem.ContainerName))
		}
		if normalizeRepository(baseItem.Image) != normalizeRepository(planItem.TargetImage) {
			return nil, fmt.Errorf(
				"deployment target %q changed target repository from %q to %q",
				formatSelector(baseItem.AppName, baseItem.ContainerName), baseItem.Image, planItem.TargetImage,
			)
		}
	}
	for key, item := range planBySelector {
		if _, exists := baseBySelector[key]; !exists {
			return nil, fmt.Errorf("bundle deployment target %q is missing from base release", formatSelector(item.AppName, item.ContainerName))
		}
	}
	result := *plan
	result.Images = append([]PlanImage(nil), plan.Images...)
	for i := range result.Images {
		item := &result.Images[i]
		key := selectorKey(item.AppName, item.ContainerName)
		if _, isSelected := selected[key]; isSelected {
			continue
		}
		baseItem := baseBySelector[key]
		item.SourceImage = strings.TrimSpace(baseItem.Image)
		item.SourceRef = item.SourceImage + "@" + strings.TrimSpace(baseItem.Digest)
		item.RequestedTag = strings.TrimSpace(baseItem.Tag)
		item.FromBaseRelease = true
	}
	return &result, nil
}

// applyBaseReleaseTemplate follows simple-release-management semantics: the
// base release is the authoritative target list and ordering. The bundle only
// supplies a new source for explicitly selected targets. Every other target is
// reused by digest from the base release and receives the new release tag.
func applyBaseReleaseTemplate(plan *Plan, base *ReleaseManifest, selected map[string]struct{}, baseBySelector map[string]ReleaseImage, planBySelector map[string]PlanImage) (*Plan, error) {
	for key := range selected {
		if _, exists := baseBySelector[key]; !exists {
			return nil, fmt.Errorf("selected deployment target %q does not exist in base release", displaySelectorKey(key))
		}
		if _, exists := planBySelector[key]; !exists {
			return nil, fmt.Errorf("selected deployment target %q does not exist in bundle", displaySelectorKey(key))
		}
	}

	result := *plan
	result.Images = make([]PlanImage, 0, len(base.Images))
	for _, baseItem := range base.Images {
		key := selectorKey(baseItem.AppName, baseItem.ContainerName)
		planItem, existsInBundle := planBySelector[key]
		_, isSelected := selected[key]
		if isSelected {
			if !existsInBundle {
				return nil, fmt.Errorf("selected deployment target %q does not exist in bundle", formatSelector(baseItem.AppName, baseItem.ContainerName))
			}
			if normalizeRepository(baseItem.Image) != normalizeRepository(planItem.TargetImage) {
				return nil, fmt.Errorf(
					"deployment target %q changed target repository from %q to %q",
					formatSelector(baseItem.AppName, baseItem.ContainerName), baseItem.Image, planItem.TargetImage,
				)
			}
			planItem.ID = releaseImageID(baseItem)
			planItem.AppName = baseItem.AppName
			planItem.ContainerName = baseItem.ContainerName
			planItem.TargetImage = strings.TrimSpace(baseItem.Image)
			planItem.TargetRef = planItem.TargetImage + ":" + plan.ReleaseID
			result.Images = append(result.Images, planItem)
			continue
		}

		result.Images = append(result.Images, reusedBasePlanImage(plan, baseItem))
	}
	return &result, nil
}

func reusedBasePlanImage(plan *Plan, baseItem ReleaseImage) PlanImage {
	targetImage := strings.TrimSpace(baseItem.Image)
	return PlanImage{
		ID:              releaseImageID(baseItem),
		SourceRef:       targetImage + "@" + strings.TrimSpace(baseItem.Digest),
		SourceImage:     targetImage,
		RequestedTag:    strings.TrimSpace(baseItem.Tag),
		TargetImage:     targetImage,
		TargetRef:       targetImage + ":" + plan.ReleaseID,
		AppName:         baseItem.AppName,
		ContainerName:   baseItem.ContainerName,
		ExtraTags:       append([]string(nil), plan.ExtraTags...),
		FromBaseRelease: true,
	}
}

func releaseImageID(item ReleaseImage) string {
	if id := strings.TrimSpace(item.ID); id != "" {
		return id
	}
	return formatSelector(item.AppName, item.ContainerName)
}

func parseSelectors(values []string) (map[string]struct{}, error) {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		parts := strings.Split(value, "/")
		if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
			return nil, fmt.Errorf("invalid selector %q: expected APP_NAME/CONTAINER_NAME", value)
		}
		key := selectorKey(parts[0], parts[1])
		if _, exists := out[key]; exists {
			return nil, fmt.Errorf("duplicate selector %q", value)
		}
		out[key] = struct{}{}
	}
	return out, nil
}

func selectorKey(appName, containerName string) string {
	return strings.TrimSpace(appName) + "\x00" + strings.TrimSpace(containerName)
}

func formatSelector(appName, containerName string) string {
	return strings.TrimSpace(appName) + "/" + strings.TrimSpace(containerName)
}

func displaySelectorKey(key string) string {
	parts := strings.SplitN(key, "\x00", 2)
	if len(parts) != 2 {
		return key
	}
	return formatSelector(parts[0], parts[1])
}

func normalizeRepository(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "docker://")
	return strings.Trim(value, "/")
}
