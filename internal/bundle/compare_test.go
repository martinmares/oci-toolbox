package bundle

import (
	"reflect"
	"strings"
	"testing"
)

func TestReleaseSelectorsAreSorted(t *testing.T) {
	manifest := &ReleaseManifest{Images: []ReleaseImage{
		{ID: "worker", AppName: "zis-worker", ContainerName: "application", Image: "registry/team/worker", Tag: "R2", Digest: compareDigest("b")},
		{ID: "api", AppName: "zis-api", ContainerName: "application", Image: "registry/team/api", Tag: "R2", Digest: compareDigest("a")},
	}}

	got := ReleaseSelectors(manifest)
	if len(got) != 2 || got[0].Selector != "zis-api/application" || got[1].Selector != "zis-worker/application" {
		t.Fatalf("unexpected selectors: %#v", got)
	}
}

func TestCompareReleasesReportsAllStatusesAndChangedFields(t *testing.T) {
	a := &ReleaseManifest{Images: []ReleaseImage{
		{ID: "api", AppName: "api", ContainerName: "app", Image: "registry/team/api", Tag: "R1", Digest: compareDigest("a"), Platform: "linux/amd64"},
		{ID: "same", AppName: "same", ContainerName: "app", Image: "registry/team/same", Tag: "R1", Digest: compareDigest("b")},
		{ID: "old", AppName: "removed", ContainerName: "app", Image: "registry/team/old", Tag: "R1", Digest: compareDigest("c")},
	}}
	b := &ReleaseManifest{Images: []ReleaseImage{
		{ID: "api-renamed", AppName: "api", ContainerName: "app", Image: "registry/team/api", Tag: "R2", Digest: compareDigest("d"), Platform: "linux/arm64"},
		{ID: "same-renamed", AppName: "same", ContainerName: "app", Image: "registry/team/same", Tag: "R1", Digest: compareDigest("b")},
		{ID: "new", AppName: "added", ContainerName: "app", Image: "registry/team/new", Tag: "R2", Digest: compareDigest("e")},
	}}

	got := CompareReleases(a, b)
	if got.Summary != (ReleaseDiffSummary{Same: 1, Changed: 1, Added: 1, Removed: 1}) {
		t.Fatalf("unexpected summary: %#v", got.Summary)
	}
	if !got.HasChanges() {
		t.Fatal("comparison should report changes")
	}
	if selectors := []string{got.Images[0].Selector, got.Images[1].Selector, got.Images[2].Selector, got.Images[3].Selector}; !reflect.DeepEqual(selectors, []string{"added/app", "api/app", "removed/app", "same/app"}) {
		t.Fatalf("unexpected sort order: %#v", selectors)
	}
	if got.Images[1].Status != "changed" || !reflect.DeepEqual(got.Images[1].ChangedFields, []string{"digest", "platform"}) || !got.Images[1].TagChanged {
		t.Fatalf("unexpected changed row: %#v", got.Images[1])
	}
	if got.Images[3].Status != "same" {
		t.Fatalf("ID-only metadata change must not change deployed image: %#v", got.Images[3])
	}
}

func TestCompareReleasesTreatsReleaseRetagAsSameContent(t *testing.T) {
	a := &ReleaseManifest{Images: []ReleaseImage{{
		AppName: "api", ContainerName: "app", Image: "registry/team/api", Tag: "R1", Digest: compareDigest("a"),
	}}}
	b := &ReleaseManifest{Images: []ReleaseImage{{
		AppName: "api", ContainerName: "app", Image: "registry/team/api", Tag: "R2", Digest: compareDigest("a"),
	}}}

	got := CompareReleases(a, b)
	if got.HasChanges() || got.Summary.Same != 1 || !got.Images[0].TagChanged {
		t.Fatalf("unexpected comparison: %#v", got)
	}
}

func TestCompareReleasesModes(t *testing.T) {
	a := &ReleaseManifest{Images: []ReleaseImage{{
		ID: "api-a", AppName: "api", ContainerName: "app",
		Source: &ReleaseSource{Image: "registry/build/api", Tag: "build-1", Digest: compareDigest("b")},
		Image:  "registry-a/team/api", Tag: "R1", Digest: compareDigest("a"), ExtraTags: []string{"stable"}, Platform: "linux/amd64",
	}}}
	b := &ReleaseManifest{Images: []ReleaseImage{{
		ID: "api-b", AppName: "api", ContainerName: "app",
		Source: &ReleaseSource{Image: "registry/build/api", Tag: "build-2", Digest: compareDigest("b")},
		Image:  "registry-b/team/api", Tag: "R2", Digest: compareDigest("a"), ExtraTags: []string{"candidate"}, Platform: "linux/arm64",
	}}}

	digest := CompareReleasesWithMode(a, b, ReleaseCompareDigest)
	if digest.HasChanges() || digest.Summary.Same != 1 {
		t.Fatalf("digest comparison = %#v", digest)
	}
	image := CompareReleasesWithMode(a, b, ReleaseCompareImage)
	if got := image.Images[0].ChangedFields; !reflect.DeepEqual(got, []string{"image", "platform"}) {
		t.Fatalf("image changed fields = %#v", got)
	}
	full := CompareReleasesWithMode(a, b, ReleaseCompareFull)
	wantFull := []string{"id", "source.tag", "image", "tag", "extra_tags", "platform"}
	if got := full.Images[0].ChangedFields; !reflect.DeepEqual(got, wantFull) {
		t.Fatalf("full changed fields = %#v, want %#v", got, wantFull)
	}
}

func TestCompareReleasesWithoutChanges(t *testing.T) {
	manifest := &ReleaseManifest{Images: []ReleaseImage{{
		AppName: "api", ContainerName: "app", Image: "registry/team/api", Tag: "R1", Digest: compareDigest("a"),
	}}}
	got := CompareReleases(manifest, manifest)
	if got.HasChanges() || got.Summary.Same != 1 {
		t.Fatalf("unexpected comparison: %#v", got)
	}
}

func compareDigest(char string) string {
	return "sha256:" + strings.Repeat(char, 64)
}
