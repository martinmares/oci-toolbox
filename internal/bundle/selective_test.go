package bundle_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/martinmares/oci-toolbox/internal/bundle"
)

const selectiveDigestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const selectiveDigestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
const selectiveDigestC = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"

func TestDecodeReleaseStrictAndValidateSelectors(t *testing.T) {
	manifest, err := bundle.DecodeRelease(strings.NewReader(`
release_id: release-1
images:
  - app_name: api
    container_name: application
    image: registry.example.com/release/api
    tag: release-1
    digest: ` + selectiveDigestA + `
`))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ReleaseID != "release-1" {
		t.Fatalf("manifest = %#v", manifest)
	}
	_, err = bundle.DecodeRelease(strings.NewReader(`
release_id: release-1
unknown: true
images: []
`))
	if err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadSelectionFileStrict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "selected.yml")
	if err := os.WriteFile(path, []byte("selectors:\n  - api/application\n  - worker/application\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	selectors, err := bundle.LoadSelectionFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(selectors, ",") != "api/application,worker/application" {
		t.Fatalf("selectors = %#v", selectors)
	}
	if err := os.WriteFile(path, []byte("selectors: [api/application]\nunknown: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.LoadSelectionFile(path); err == nil || !strings.Contains(err.Error(), "field unknown not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestApplyBaseReleaseReusesUnselectedDigest(t *testing.T) {
	plan := selectivePlan()
	base := selectiveBaseRelease()
	got, err := bundle.ApplyBaseRelease(plan, base, []string{"api/application"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Images[0].FromBaseRelease {
		t.Fatal("selected image unexpectedly reused base release")
	}
	worker := got.Images[1]
	if !worker.FromBaseRelease || worker.SourceRef != "registry.example.com/release/worker@"+selectiveDigestB || worker.RequestedTag != "release-1" {
		t.Fatalf("worker = %#v", worker)
	}
	if plan.Images[1].FromBaseRelease || plan.Images[1].SourceRef == worker.SourceRef {
		t.Fatal("input plan was mutated")
	}
}

func TestApplyBaseReleaseRejectsMissingAndChangedTopology(t *testing.T) {
	plan := selectivePlan()
	base := selectiveBaseRelease()
	if _, err := bundle.ApplyBaseRelease(plan, base, []string{"missing/application"}); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("missing selector error = %v", err)
	}
	base.Images[1].Image = "registry.example.com/release/renamed-worker"
	if _, err := bundle.ApplyBaseRelease(plan, base, []string{"api/application"}); err == nil || !strings.Contains(err.Error(), "changed target repository") {
		t.Fatalf("repository error = %v", err)
	}
}

func TestApplyBaseReleasePreservesBaseOnlyTargets(t *testing.T) {
	plan := selectivePlan()
	plan.ExtraTags = []string{"stable"}
	plan.Images = append(plan.Images, bundle.PlanImage{
		ID: "bundle-only", AppName: "new-app", ContainerName: "application",
		SourceRef: "registry.example.com/build/new-app:latest", SourceImage: "registry.example.com/build/new-app",
		TargetImage: "registry.example.com/release/new-app", TargetRef: "registry.example.com/release/new-app:release-2",
	})
	base := selectiveBaseRelease()
	base.Images = append(base.Images,
		bundle.ReleaseImage{ID: "z-sidecar", AppName: "z-app", ContainerName: "sidecar", Image: "registry.example.com/release/z-sidecar", Tag: "release-1", Digest: selectiveDigestC},
		bundle.ReleaseImage{ID: "a-sidecar", AppName: "a-app", ContainerName: "sidecar", Image: "registry.example.com/release/a-sidecar", Tag: "release-1", Digest: selectiveDigestC},
	)

	got, err := bundle.ApplyBaseReleaseWithOptions(plan, base, []string{"api/application"}, bundle.SelectiveOptions{PreserveBaseOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Images) != 4 {
		t.Fatalf("images = %#v", got.Images)
	}
	if selectors := []string{
		got.Images[0].AppName + "/" + got.Images[0].ContainerName,
		got.Images[1].AppName + "/" + got.Images[1].ContainerName,
		got.Images[2].AppName + "/" + got.Images[2].ContainerName,
		got.Images[3].AppName + "/" + got.Images[3].ContainerName,
	}; strings.Join(selectors, ",") != "api/application,worker/application,z-app/sidecar,a-app/sidecar" {
		t.Fatalf("base release order was not preserved: %v", selectors)
	}
	for i, selector := range []string{"z-app/sidecar", "a-app/sidecar"} {
		item := got.Images[i+2]
		if item.AppName+"/"+item.ContainerName != selector || !item.FromBaseRelease || item.SourceRef != item.TargetImage+"@"+selectiveDigestC || item.TargetRef != item.TargetImage+":release-2" || strings.Join(item.ExtraTags, ",") != "stable" {
			t.Fatalf("preserved image %d = %#v", i, item)
		}
	}
	if _, err := bundle.ApplyBaseReleaseWithOptions(plan, base, []string{"a-app/sidecar"}, bundle.SelectiveOptions{PreserveBaseOnly: true}); err == nil || !strings.Contains(err.Error(), "does not exist in bundle") {
		t.Fatalf("selected base-only error = %v", err)
	}
	if _, err := bundle.ApplyBaseReleaseWithOptions(plan, base, []string{"new-app/application"}, bundle.SelectiveOptions{PreserveBaseOnly: true}); err == nil || !strings.Contains(err.Error(), "does not exist in base release") {
		t.Fatalf("selected bundle-only error = %v", err)
	}
}

func TestInferTargetRegistryFromBaseRelease(t *testing.T) {
	b := &bundle.Bundle{Images: []bundle.Image{
		{Target: bundle.Target{Repository: "api"}, Deployment: bundle.Deployment{AppName: "api", ContainerName: "application"}},
		{Target: bundle.Target{Repository: "worker"}, Deployment: bundle.Deployment{AppName: "worker", ContainerName: "application"}},
	}}
	base := selectiveBaseRelease()
	got, err := bundle.InferTargetRegistry(b, base, "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "registry.example.com/release" {
		t.Fatalf("registry = %q", got)
	}
	base.Images[1].Image = "other.example.com/release/worker"
	if _, err := bundle.InferTargetRegistry(b, base, ""); err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("ambiguous registry error = %v", err)
	}
}

func selectivePlan() *bundle.Plan {
	return &bundle.Plan{ReleaseID: "release-2", Images: []bundle.PlanImage{
		{ID: "api", AppName: "api", ContainerName: "application", SourceRef: "registry.example.com/build/api:latest", SourceImage: "registry.example.com/build/api", TargetImage: "registry.example.com/release/api", TargetRef: "registry.example.com/release/api:release-2"},
		{ID: "worker", AppName: "worker", ContainerName: "application", SourceRef: "registry.example.com/build/worker:latest", SourceImage: "registry.example.com/build/worker", TargetImage: "registry.example.com/release/worker", TargetRef: "registry.example.com/release/worker:release-2"},
	}}
}

func selectiveBaseRelease() *bundle.ReleaseManifest {
	return &bundle.ReleaseManifest{ReleaseID: "release-1", Images: []bundle.ReleaseImage{
		{ID: "api", AppName: "api", ContainerName: "application", Image: "registry.example.com/release/api", Tag: "release-1", Digest: selectiveDigestA},
		{ID: "worker", AppName: "worker", ContainerName: "application", Image: "registry.example.com/release/worker", Tag: "release-1", Digest: selectiveDigestB},
	}}
}
