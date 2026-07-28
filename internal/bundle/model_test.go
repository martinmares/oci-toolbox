package bundle_test

import (
	"strings"
	"testing"

	"github.com/martinmares/oci-toolbox/internal/bundle"
)

const validBundle = `
name: stable
registries:
  source:
    url: registry-source.example.com
  target:
    url: registry-target.example.com
    project: release
extra_tags: [stable]
images:
  - id: config
    source:
      repository: team/config
      tag: 2.3-latest
    target:
      repository: config
    deployment:
      app_name: config
      container_name: config
`

func TestDecodeAndResolve(t *testing.T) {
	b, err := bundle.Decode(strings.NewReader(validBundle))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := bundle.Resolve(b, bundle.ResolveOptions{
		ReleaseID: "RE_2026.07.28.01",
		ExtraTags: []string{"candidate", "stable"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Images) != 1 {
		t.Fatalf("images = %d", len(plan.Images))
	}
	item := plan.Images[0]
	if item.SourceRef != "registry-source.example.com/team/config:2.3-latest" {
		t.Fatalf("source = %q", item.SourceRef)
	}
	if item.TargetRef != "registry-target.example.com/release/config:RE_2026.07.28.01" {
		t.Fatalf("target = %q", item.TargetRef)
	}
	if strings.Join(item.ExtraTags, ",") != "stable,candidate" {
		t.Fatalf("extra tags = %#v", item.ExtraTags)
	}
}

func TestDecodeRejectsUnknownField(t *testing.T) {
	_, err := bundle.Decode(strings.NewReader(strings.Replace(validBundle, "name: stable", "name: stable\nunknown: true", 1)))
	if err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsControlCharacter(t *testing.T) {
	input := strings.Replace(validBundle, "app_name: config", "app_name: \"bad\\u001bname\"", 1)
	_, err := bundle.Decode(strings.NewReader(input))
	if err == nil || !strings.Contains(err.Error(), "control character") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeRejectsDuplicateSelector(t *testing.T) {
	input := validBundle + `
  - id: another
    source:
      repository: team/another
      tag: latest
    target:
      repository: another
    deployment:
      app_name: config
      container_name: config
`
	_, err := bundle.Decode(strings.NewReader(input))
	if err == nil || !strings.Contains(err.Error(), "duplicate deployment selector") {
		t.Fatalf("error = %v", err)
	}
}

func TestDecodeReconstructStrict(t *testing.T) {
	input, err := bundle.DecodeReconstruct(strings.NewReader(`
images:
  - ref: registry.example.com/team/api:release-1
    app_name: api
    container_name: api
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(input.Images) != 1 || input.Images[0].Ref == "" {
		t.Fatalf("input = %#v", input)
	}

	_, err = bundle.DecodeReconstruct(strings.NewReader(`
images:
  - ref: registry.example.com/team/api:release-1
    unexpected: true
`))
	if err == nil || !strings.Contains(err.Error(), "field unexpected not found") {
		t.Fatalf("error = %v", err)
	}
}
