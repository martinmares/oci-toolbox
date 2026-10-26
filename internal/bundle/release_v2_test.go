package bundle

import (
	"reflect"
	"strings"
	"testing"
)

func TestReleaseV1V2RoundTripPreservesContent(t *testing.T) {
	legacy := `apiVersion: oci-toolbox/v1
kind: ImageRelease
release_id: customer-batch
created_at: 2026-10-03T13:16:23Z
registry_base: mirror.example/team
bundle: {name: legacy, revision: abc123}
platform: linux/amd64
extra_tags: [stable]
images:
  - id: config
    app_name: config-form
    container_name: main
    image: mirror.example/team/config-form
    tag: customer-batch
    digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
    extra_tags: [latest]
    source:
      image: registry.example/product/config
      tag: 2.4
      digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
`
	input, err := DecodeRelease(strings.NewReader(legacy))
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := EncodeReleaseV2(&output, input); err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"apiVersion: oci-toolbox/v2", "repository_prefix: mirror.example/team", "repository: mirror.example/team/config-form", "repository: registry.example/product/config"} {
		if !strings.Contains(output.String(), required) {
			t.Fatalf("missing %q in %s", required, output.String())
		}
	}
	for _, forbidden := range []string{"registry_base:", "    image:"} {
		if strings.Contains(output.String(), forbidden) {
			t.Fatalf("legacy field %q in v2", forbidden)
		}
	}
	restored, err := DecodeRelease(strings.NewReader(output.String()))
	if err != nil {
		t.Fatal(err)
	}
	if restored.ReleaseID != input.ReleaseID || restored.RegistryBase != input.RegistryBase || !reflect.DeepEqual(restored.Images, input.Images) || !reflect.DeepEqual(restored.Bundle, input.Bundle) || !reflect.DeepEqual(restored.ExtraTags, input.ExtraTags) || !restored.CreatedAt.Equal(input.CreatedAt) {
		t.Fatalf("conversion changed content: %#v -> %#v", input, restored)
	}
}

func TestReleaseV2RejectsMixedSchemas(t *testing.T) {
	for _, input := range []string{
		"apiVersion: oci-toolbox/v2\nkind: ImageRelease\n" + legacyHeaderFixture,
		"apiVersion: oci-toolbox/v1\nkind: ImageRelease\n" + strings.Replace(legacyHeaderFixture, "image:", "repository:", 1),
		"apiVersion: oci-toolbox/v2\nkind: ImageRelease\nregistry_base: registry.example\n" + strings.Replace(legacyHeaderFixture, "image:", "repository:", 1),
		"apiVersion: oci-toolbox/v2\nkind: ImageRelease\n" + strings.Replace(legacyHeaderFixture, "image:", "repository: registry.example/api\n    image:", 1),
	} {
		if _, err := DecodeRelease(strings.NewReader(input)); err == nil {
			t.Fatalf("accepted mixed schema: %s", input)
		}
	}
}

func TestReleaseV2AllowsMultipleRegistriesAndLogicalReleaseID(t *testing.T) {
	input := "apiVersion: oci-toolbox/v2\nkind: ImageRelease\n" + strings.Replace(legacyHeaderFixture, "image:", "repository:", 1)
	input = strings.Replace(input, "release_id: example", "release_id: 'customer/batch'", 1)
	input += "  - app_name: helper\n    repository: docker.io/library/nginx\n    digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n"
	if _, err := DecodeRelease(strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseV2DoesNotWriteMultiPlatformSentinel(t *testing.T) {
	input, err := DecodeRelease(strings.NewReader(legacyHeaderFixture))
	if err != nil {
		t.Fatal(err)
	}
	input.Platform = "multi-platform"
	input.Images[0].Platform = "multi-platform"
	var output strings.Builder
	if err := EncodeReleaseV2(&output, input); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "platform:") {
		t.Fatal(output.String())
	}
	if input.Platform != "multi-platform" || input.Images[0].Platform != "multi-platform" {
		t.Fatal("encoding mutated original")
	}
}

func TestReleaseMergedFieldsRespectSchema(t *testing.T) {
	for _, version := range []string{"oci-toolbox/v1", "oci-toolbox/v2"} {
		for _, field := range []string{"image", "repository"} {
			input := "apiVersion: " + version + "\nkind: ImageRelease\nrelease_id: test\nimages:\n  - app_name: api\n    <<: {" + field + ": registry.example.com/api}\n    digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n"
			_, err := DecodeRelease(strings.NewReader(input))
			valid := (version == LegacyReleaseAPIVersion && field == "image") || (version == ReleaseAPIVersion && field == "repository")
			if (err == nil) != valid {
				t.Fatalf("version=%s field=%s err=%v", version, field, err)
			}
		}
	}
	input := "apiVersion: oci-toolbox/v1\nkind: ImageRelease\nrelease_id: test\nimages:\n  - app_name: api\n    image: &repo registry.example.com/api\n    digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n    source: {<<: {repository: *repo}, digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef}\n"
	if _, err := DecodeRelease(strings.NewReader(input)); err == nil {
		t.Fatal("accepted mixed merged source schema")
	}
}

func TestReleaseV2ConversionComparesEqualAndDefaultOutputStaysV1(t *testing.T) {
	input, err := DecodeRelease(strings.NewReader(legacyHeaderFixture))
	if err != nil {
		t.Fatal(err)
	}
	input.Images[0].Platform = "multi-platform"
	var output strings.Builder
	if err := EncodeReleaseV2(&output, input); err != nil {
		t.Fatal(err)
	}
	v2, err := DecodeRelease(strings.NewReader(output.String()))
	if err != nil {
		t.Fatal(err)
	}
	if comparison := CompareReleasesWithMode(input, v2, ReleaseCompareFull); comparison.HasChanges() {
		t.Fatalf("normalization changed content: %#v", comparison)
	}
	output.Reset()
	if err := EncodeRelease(&output, v2); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "apiVersion: oci-toolbox/v1") || !strings.Contains(output.String(), "    image:") || strings.Contains(output.String(), "repository:") {
		t.Fatal(output.String())
	}
	legacy, err := DecodeRelease(strings.NewReader(output.String()))
	if err != nil {
		t.Fatal(err)
	}
	if comparison := CompareReleasesWithMode(input, legacy, ReleaseCompareFull); comparison.HasChanges() {
		t.Fatalf("default output changed content: %#v", comparison)
	}
}
