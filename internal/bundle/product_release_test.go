package bundle

import (
	"strings"
	"testing"
)

const productFixture = `apiVersion: release.tsm.datalite.cz/v1alpha1
kind: TsmProductRelease
metadata:
  name: 2.4.11.5
  createdAt: '2026-09-28T19:52:34+02:00'
spec:
  version:
    product: 2.4.11.5
  changedComponents: []
  maven:
    unrelated: ignored
  containers:
    api:
      image: registry.example.com/tsm/api
      tag: 2.4.11.2
      originRelease: 2.4.11.2
      digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
  frontend:
    apps:
      datalite:
        image: registry.example.com/tsm/tsm-ui
        tag: datalite-2.4.1102
        digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
`

func TestProductReleaseNormalizationAndRoundTrip(t *testing.T) {
	m, err := DecodeRelease(strings.NewReader(productFixture))
	if err != nil {
		t.Fatal(err)
	}
	if m.ReleaseID != "2.4.11.5" || m.RegistryBase != "registry.example.com/tsm" || len(m.Images) != 2 {
		t.Fatalf("manifest = %#v", m)
	}
	if m.Images[0].Tag != "2.4.11.2" || m.Images[1].ID != "frontend-datalite" || m.Images[1].AppName != "tsm-ui" || m.Images[1].ContainerName != "tsm-ui" {
		t.Fatalf("images = %#v", m.Images)
	}
	if m.CreatedAt.IsZero() {
		t.Fatal("creation time was lost")
	}
	b, err := BundleFromRelease(m, "product", BundleSourceDigest)
	if err != nil {
		t.Fatal(err)
	}
	if b.Images[1].Source.Digest != m.Images[1].Digest {
		t.Fatal("source digest was lost")
	}
	var out strings.Builder
	if err := EncodeRelease(&out, m); err != nil {
		t.Fatal(err)
	}
	again, err := DecodeRelease(strings.NewReader(out.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Images) != 2 || again.Images[1].Source.Tag != m.Images[1].Tag {
		t.Fatal("round trip lost images or source metadata")
	}
}

func TestReleaseInputRejectsMalformedDocuments(t *testing.T) {
	for _, tc := range []struct{ name, input string }{
		{"embedded tag", strings.Replace(productFixture, "registry.example.com/tsm/api", "registry.example.com/tsm/api:unexpected", 1)},
		{"version", strings.Replace(productFixture, "v1alpha1", "v99", 1)},
		{"mismatched identity", strings.Replace(productFixture, "name: 2.4.11.5", "name: 2.4.11.4", 1)},
		{"missing digest", strings.ReplaceAll(productFixture, "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", "bad")},
		{"multiple documents", productFixture + "\n---\n{}\n"},
		{"duplicate key", strings.Replace(productFixture, "  name: 2.4.11.5", "  name: 2.4.11.5\n  name: duplicate", 1)},
		{"legacy unknown field", "release_id: old\nmisspelled: true\nimages: []\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := DecodeRelease(strings.NewReader(tc.input)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestProductReleaseMultipleRegistriesHaveNoInferredBase(t *testing.T) {
	raw := strings.Replace(productFixture, "registry.example.com/tsm/tsm-ui", "other.example.com/ui", 1)
	m, err := DecodeRelease(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if m.RegistryBase != "" {
		t.Fatalf("ambiguous registry_base = %q", m.RegistryBase)
	}
}
