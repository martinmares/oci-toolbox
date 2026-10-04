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
	if headerPresent && (header.APIVersion != ReleaseAPIVersion || header.Kind != ReleaseKind) {
		return nil, fmt.Errorf("unsupported release format %q / %q; convert external YAML with yaml-map before using oci-toolbox", header.APIVersion, header.Kind)
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
