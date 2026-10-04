package bundle

import (
	"strings"
	"testing"
)

const legacyHeaderFixture = `release_id: example
images:
  - app_name: api
    image: registry.example.com/team/api
    digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
`

func TestReleaseHeaderCompatibility(t *testing.T) {
	for _, input := range []string{legacyHeaderFixture, "apiVersion: oci-toolbox/v1\nkind: ImageRelease\n" + legacyHeaderFixture} {
		m, err := DecodeRelease(strings.NewReader(input))
		if err != nil {
			t.Fatal(err)
		}
		originalVersion := m.APIVersion
		var output strings.Builder
		if err := EncodeRelease(&output, m); err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(output.String(), "apiVersion: oci-toolbox/v1\nkind: ImageRelease\n") {
			t.Fatalf("missing output header: %s", output.String())
		}
		if m.APIVersion != originalVersion {
			t.Fatal("encoding mutated input")
		}
		restored, err := DecodeRelease(strings.NewReader(output.String()))
		if err != nil {
			t.Fatal(err)
		}
		if restored.ReleaseID != m.ReleaseID || len(restored.Images) != len(m.Images) {
			t.Fatal("round trip changed release")
		}
	}
}

func TestExternalReleaseRequiresConversion(t *testing.T) {
	input := "apiVersion: release.example.com/v1alpha1\nkind: ProductRelease\nmetadata: {name: example}\nspec: {containers: {}}\n"
	if _, err := DecodeRelease(strings.NewReader(input)); err == nil || !strings.Contains(err.Error(), "yaml-map") {
		t.Fatalf("foreign release error = %v, want conversion guidance", err)
	}
}

func TestNativeReleaseRejectsMalformedDocuments(t *testing.T) {
	for _, input := range []string{
		legacyHeaderFixture + "\n---\n{}\n",
		"release_id: old\nmisspelled: true\nimages: []\n",
		strings.Replace(legacyHeaderFixture, "release_id: example", "release_id: example\nrelease_id: duplicate", 1),
	} {
		if _, err := DecodeRelease(strings.NewReader(input)); err == nil {
			t.Fatalf("accepted malformed input %q", input)
		}
	}
}

func TestReleaseHeaderRejectsUnknownAndIncomplete(t *testing.T) {
	for _, header := range []string{
		"apiVersion: oci-toolbox/v2\nkind: ImageRelease\n",
		"apiVersion: oci-toolbox/v1\nkind: Bundle\n",
		"apiVersion: oci-toolbox/v1\n",
		"kind: ImageRelease\n",
		"apiVersion: ''\nkind: ''\n",
		"apiVersion: oci-toolbox/v1\nkind: ImageRelease\nunknown: true\n",
	} {
		if _, err := DecodeRelease(strings.NewReader(header + legacyHeaderFixture)); err == nil {
			t.Fatalf("accepted header %q", header)
		}
	}
}
