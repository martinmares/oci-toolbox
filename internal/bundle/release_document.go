package bundle

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

// DecodeReleaseDocument reads only the native ImageRelease schema and legacy
// unversioned files. Callers validate the result for their particular operation.
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
	if err := expandReleaseAliases(&doc); err != nil {
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
	if headerPresent && ((header.APIVersion != ReleaseAPIVersion && header.APIVersion != LegacyReleaseAPIVersion) || header.Kind != ReleaseKind) {
		return nil, fmt.Errorf("unsupported release format %q / %q; convert external YAML with yaml-map before using oci-toolbox", header.APIVersion, header.Kind)
	}
	if header.APIVersion != ReleaseAPIVersion && len(doc.Content) > 0 {
		if err := renameReleaseFields(doc.Content[0], true); err != nil {
			return nil, err
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

// renameReleaseFields translates only schema fields; unknown fields stay strict errors.
func renameReleaseFields(root *yaml.Node, toV2 bool) error {
	prefixFrom, prefixTo, imageFrom, imageTo := "registry_base", "repository_prefix", "image", "repository"
	if !toV2 {
		prefixFrom, prefixTo, imageFrom, imageTo = prefixTo, prefixFrom, imageTo, imageFrom
	}
	if err := renameLegacyField(root, prefixFrom, prefixTo); err != nil {
		return err
	}
	images := releaseMappingValue(root, "images")
	if images == nil || images.Kind != yaml.SequenceNode {
		return nil
	}
	for _, image := range images.Content {
		if err := renameLegacyField(image, imageFrom, imageTo); err != nil {
			return err
		}
		if source := releaseMappingValue(image, "source"); source != nil {
			if err := renameLegacyField(source, imageFrom, imageTo); err != nil {
				return err
			}
		}
	}
	return nil
}

func releaseMappingValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

func renameLegacyField(node *yaml.Node, old, canonical string) error {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	if releaseMappingValue(node, canonical) != nil {
		return fmt.Errorf("field %q requires apiVersion %s", canonical, ReleaseAPIVersion)
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i].Value == old {
			node.Content[i].Value = canonical
		}
	}
	return nil
}

// Resolve aliases/merges before schema checks so inherited fields cannot bypass them.
// Decode into an untyped value also rejects duplicate keys before re-encoding.
func expandReleaseAliases(doc *yaml.Node) error {
	if !releaseHasAliases(doc) {
		return nil
	}
	var value any
	if err := doc.Decode(&value); err != nil {
		return err
	}
	var mapping yaml.Node
	if err := mapping.Encode(value); err != nil {
		return err
	}
	*doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{&mapping}}
	return nil
}

func releaseHasAliases(node *yaml.Node) bool {
	if node.Kind == yaml.AliasNode || node.Tag == "!!merge" {
		return true
	}
	for _, child := range node.Content {
		if releaseHasAliases(child) {
			return true
		}
	}
	return false
}
