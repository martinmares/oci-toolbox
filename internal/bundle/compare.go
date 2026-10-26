package bundle

import (
	"slices"
	"sort"
	"strings"
)

type ReleaseCompareMode string

const (
	ReleaseCompareDigest ReleaseCompareMode = "digest"
	ReleaseCompareImage  ReleaseCompareMode = "image"
	ReleaseCompareFull   ReleaseCompareMode = "full"
)

type ReleaseSelector struct {
	Selector      string `json:"selector"`
	ID            string `json:"id,omitempty"`
	AppName       string `json:"app_name"`
	ContainerName string `json:"container_name"`
	Image         string `json:"image"`
	Tag           string `json:"tag,omitempty"`
	Digest        string `json:"digest"`
	Platform      string `json:"platform,omitempty"`
}

type ReleaseImageState struct {
	ID        string         `json:"id,omitempty"`
	Source    *ReleaseSource `json:"source,omitempty"`
	Image     string         `json:"image"`
	Tag       string         `json:"tag,omitempty"`
	Digest    string         `json:"digest"`
	ExtraTags []string       `json:"extra_tags,omitempty"`
	Platform  string         `json:"platform,omitempty"`
}

type ReleaseDiff struct {
	Selector      string             `json:"selector"`
	AppName       string             `json:"app_name"`
	ContainerName string             `json:"container_name"`
	Status        string             `json:"status"`
	ChangedFields []string           `json:"changed_fields,omitempty"`
	TagChanged    bool               `json:"tag_changed,omitempty"`
	A             *ReleaseImageState `json:"a,omitempty"`
	B             *ReleaseImageState `json:"b,omitempty"`
}

type ReleaseDiffSummary struct {
	Same    int `json:"same"`
	Changed int `json:"changed"`
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

type ReleaseComparison struct {
	Summary ReleaseDiffSummary `json:"summary"`
	Images  []ReleaseDiff      `json:"images"`
}

func ReleaseSelectors(manifest *ReleaseManifest) []ReleaseSelector {
	selectors := make([]ReleaseSelector, 0, len(manifest.Images))
	for _, item := range manifest.Images {
		selectors = append(selectors, ReleaseSelector{
			Selector:      formatSelector(item.AppName, item.ContainerName),
			ID:            item.ID,
			AppName:       item.AppName,
			ContainerName: item.ContainerName,
			Image:         item.Image,
			Tag:           item.Tag,
			Digest:        item.Digest,
			Platform:      item.Platform,
		})
	}
	sort.Slice(selectors, func(i, j int) bool {
		return selectors[i].Selector < selectors[j].Selector
	})
	return selectors
}

func CompareReleases(a, b *ReleaseManifest) ReleaseComparison {
	return CompareReleasesWithMode(a, b, ReleaseCompareImage)
}

func CompareReleasesWithMode(a, b *ReleaseManifest, mode ReleaseCompareMode) ReleaseComparison {
	aBySelector := releaseImagesBySelector(a)
	bBySelector := releaseImagesBySelector(b)
	keys := make([]string, 0, len(aBySelector)+len(bBySelector))
	seen := make(map[string]struct{}, len(aBySelector)+len(bBySelector))
	for key := range aBySelector {
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	for key := range bBySelector {
		if _, exists := seen[key]; exists {
			continue
		}
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		return displaySelectorKey(keys[i]) < displaySelectorKey(keys[j])
	})

	result := ReleaseComparison{Images: make([]ReleaseDiff, 0, len(keys))}
	for _, key := range keys {
		aItem, inA := aBySelector[key]
		bItem, inB := bBySelector[key]
		appName, containerName := selectorParts(key)
		diff := ReleaseDiff{
			Selector:      formatSelector(appName, containerName),
			AppName:       appName,
			ContainerName: containerName,
		}
		if inA {
			diff.A = releaseImageState(aItem)
		}
		if inB {
			diff.B = releaseImageState(bItem)
		}
		switch {
		case !inA:
			diff.Status = "added"
			result.Summary.Added++
		case !inB:
			diff.Status = "removed"
			result.Summary.Removed++
		default:
			diff.TagChanged = strings.TrimSpace(aItem.Tag) != strings.TrimSpace(bItem.Tag)
			diff.ChangedFields = changedReleaseFields(aItem, bItem, mode)
			if len(diff.ChangedFields) == 0 {
				diff.Status = "same"
				result.Summary.Same++
			} else {
				diff.Status = "changed"
				result.Summary.Changed++
			}
		}
		result.Images = append(result.Images, diff)
	}
	return result
}

func (c ReleaseComparison) HasChanges() bool {
	return c.Summary.Changed+c.Summary.Added+c.Summary.Removed > 0
}

func releaseImagesBySelector(manifest *ReleaseManifest) map[string]ReleaseImage {
	items := make(map[string]ReleaseImage, len(manifest.Images))
	for _, item := range manifest.Images {
		items[selectorKey(item.AppName, item.ContainerName)] = item
	}
	return items
}

func releaseImageState(item ReleaseImage) *ReleaseImageState {
	var source *ReleaseSource
	if item.Source != nil {
		copy := *item.Source
		source = &copy
	}
	return &ReleaseImageState{
		ID:        item.ID,
		Source:    source,
		Image:     item.Image,
		Tag:       item.Tag,
		Digest:    item.Digest,
		ExtraTags: append([]string(nil), item.ExtraTags...),
		Platform:  item.Platform,
	}
}

func changedReleaseFields(a, b ReleaseImage, mode ReleaseCompareMode) []string {
	fields := make([]string, 0, 9)
	if mode == ReleaseCompareFull && strings.TrimSpace(a.ID) != strings.TrimSpace(b.ID) {
		fields = append(fields, "id")
	}
	if mode == ReleaseCompareFull {
		fields = append(fields, changedReleaseSourceFields(a.Source, b.Source)...)
	}
	if mode != ReleaseCompareDigest && strings.TrimSpace(a.Image) != strings.TrimSpace(b.Image) {
		fields = append(fields, "image")
	}
	if mode == ReleaseCompareFull && strings.TrimSpace(a.Tag) != strings.TrimSpace(b.Tag) {
		fields = append(fields, "tag")
	}
	if strings.TrimSpace(a.Digest) != strings.TrimSpace(b.Digest) {
		fields = append(fields, "digest")
	}
	if mode == ReleaseCompareFull && !slices.Equal(normalizedReleaseStrings(a.ExtraTags), normalizedReleaseStrings(b.ExtraTags)) {
		fields = append(fields, "extra_tags")
	}
	if mode != ReleaseCompareDigest && normalizedReleasePlatform(a.Platform) != normalizedReleasePlatform(b.Platform) {
		fields = append(fields, "platform")
	}
	return fields
}

func changedReleaseSourceFields(a, b *ReleaseSource) []string {
	if a == nil && b == nil {
		return nil
	}
	aValue, bValue := ReleaseSource{}, ReleaseSource{}
	if a != nil {
		aValue = *a
	}
	if b != nil {
		bValue = *b
	}
	fields := make([]string, 0, 3)
	if strings.TrimSpace(aValue.Image) != strings.TrimSpace(bValue.Image) {
		fields = append(fields, "source.image")
	}
	if strings.TrimSpace(aValue.Tag) != strings.TrimSpace(bValue.Tag) {
		fields = append(fields, "source.tag")
	}
	if strings.TrimSpace(aValue.Digest) != strings.TrimSpace(bValue.Digest) {
		fields = append(fields, "source.digest")
	}
	return fields
}

func normalizedReleaseStrings(values []string) []string {
	normalized := make([]string, len(values))
	for i, value := range values {
		normalized[i] = strings.TrimSpace(value)
	}
	sort.Strings(normalized)
	return normalized
}

func selectorParts(key string) (string, string) {
	parts := strings.SplitN(key, "\x00", 2)
	if len(parts) != 2 {
		return key, ""
	}
	return parts[0], parts[1]
}

func normalizedReleasePlatform(value string) string {
	value = strings.TrimSpace(value)
	if value == "multi-platform" {
		return ""
	}
	return value
}
