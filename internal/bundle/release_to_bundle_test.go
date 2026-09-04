package bundle

import (
	"strings"
	"testing"
	"time"
)

const testDigestA = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
const testDigestB = "sha256:abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"

func TestBundleFromReleaseInfersRegistriesProjectsAndRenames(t *testing.T) {
	manifest := conversionRelease()
	converted, err := BundleFromRelease(manifest, "tsm-sda-2.4", BundleSourceDigest)
	if err != nil {
		t.Fatal(err)
	}
	if converted.Registries.Source != (Registry{URL: "registry-source.example.com", Project: "tsm"}) {
		t.Fatalf("source registry = %#v", converted.Registries.Source)
	}
	if converted.Registries.Target != (Registry{URL: "registry-target.example.com", Project: "tsm/sda"}) {
		t.Fatalf("target registry = %#v", converted.Registries.Target)
	}
	if got := converted.Images[0]; got.Source.Repository != "tsm-config" || got.Target.Repository != "tsm-config-form" || got.Source.Digest != testDigestA || got.Source.Tag != "" {
		t.Fatalf("first image = %#v", got)
	}
	if got := converted.Images[1]; got.ID != "tsm-ui" || got.Source.Repository != "tsm-ui" || got.Deployment.ContainerName != "application" {
		t.Fatalf("second image = %#v", got)
	}
}

func TestBundleFromReleaseTagMode(t *testing.T) {
	converted, err := BundleFromRelease(conversionRelease(), "tsm-sda-2.4", BundleSourceTag)
	if err != nil {
		t.Fatal(err)
	}
	if converted.Images[0].Source.Tag != "2.4" || converted.Images[0].Source.Digest != "" {
		t.Fatalf("source = %#v", converted.Images[0].Source)
	}
	if converted.Images[1].Source.Tag != "customer-2.4" {
		t.Fatalf("source = %#v", converted.Images[1].Source)
	}
}

func TestBundleFromReleaseTagModeRequiresSourceTag(t *testing.T) {
	manifest := conversionRelease()
	manifest.Images[0].Source.Tag = ""
	_, err := BundleFromRelease(manifest, "test", BundleSourceTag)
	if err == nil || !strings.Contains(err.Error(), "has no source tag") {
		t.Fatalf("error = %v", err)
	}
}

func TestBundleFromReleaseRequiresSingleSourceRegistry(t *testing.T) {
	manifest := conversionRelease()
	manifest.Images[1].Source.Image = "other.example.com/tsm/tsm-ui"
	_, err := BundleFromRelease(manifest, "test", BundleSourceDigest)
	if err == nil || !strings.Contains(err.Error(), "source images use multiple registries") {
		t.Fatalf("error = %v", err)
	}
}

func conversionRelease() *ReleaseManifest {
	return &ReleaseManifest{
		ReleaseID: "2026.08.10.01",
		CreatedAt: time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC),
		Images: []ReleaseImage{
			{
				ID: "tsm-config-form", AppName: "tsm-config-form", ContainerName: "tsm-config-form",
				Source: &ReleaseSource{Image: "registry-source.example.com/tsm/tsm-config", Tag: "2.4", Digest: testDigestA},
				Image:  "registry-target.example.com/tsm/sda/tsm-config-form", Tag: "2.4", Digest: testDigestA,
			},
			{
				AppName: "tsm-ui", ContainerName: "application",
				Source: &ReleaseSource{Image: "registry-source.example.com/tsm/tsm-ui", Tag: "customer-2.4", Digest: testDigestB},
				Image:  "registry-target.example.com/tsm/sda/tsm-ui", Tag: "2.4", Digest: testDigestB,
			},
		},
	}
}
