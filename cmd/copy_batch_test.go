package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
)

func TestLoadCopyBatch(t *testing.T) {
	path := writeBatchFile(t, `
copy:
  - src: registry.example.com/team/config:old
    dest: registry.example.com/team/config:new
  - src: registry.example.com/team/ticket:old
    destination: registry.example.com/team/ticket:new
`)

	items, err := loadCopyBatch(path)
	if err != nil {
		t.Fatalf("loadCopyBatch: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[1].target() != "registry.example.com/team/ticket:new" {
		t.Fatalf("destination alias not used: %q", items[1].target())
	}
	if items[0].source() != "registry.example.com/team/config:old" {
		t.Fatalf("src not used: %q", items[0].source())
	}
}

func TestCopyArgsAllowsBatchOutput(t *testing.T) {
	oldBatchFile, oldReleaseManifest := batchFile, releaseManifest
	oldOutput, oldReleaseID := copyOutput, copyReleaseID
	defer func() {
		batchFile, releaseManifest = oldBatchFile, oldReleaseManifest
		copyOutput, copyReleaseID = oldOutput, oldReleaseID
	}()
	batchFile = "images.yml"
	releaseManifest = ""
	copyOutput = "release.yml"
	copyReleaseID = "RE_2026.08.10.01"
	if err := copyArgs(nil, nil); err != nil {
		t.Fatalf("batch output rejected: %v", err)
	}
}

func TestCopyArgsAllowsReleaseManifestReleaseIDWithoutOutput(t *testing.T) {
	oldBatchFile, oldReleaseManifest := batchFile, releaseManifest
	oldOutput, oldReleaseID := copyOutput, copyReleaseID
	oldSrcBase, oldDestBase := srcBase, destBase
	oldFromRef, oldToRef := fromRef, toRef
	defer func() {
		batchFile, releaseManifest = oldBatchFile, oldReleaseManifest
		copyOutput, copyReleaseID = oldOutput, oldReleaseID
		srcBase, destBase = oldSrcBase, oldDestBase
		fromRef, toRef = oldFromRef, oldToRef
	}()

	batchFile = ""
	releaseManifest = "release.yml"
	copyOutput = ""
	copyReleaseID = "RE_2026.09.10.02"
	srcBase = ""
	destBase = "registry.example.com/archive"
	fromRef = ""
	toRef = ""

	if err := copyArgs(nil, nil); err != nil {
		t.Fatalf("release manifest release ID override rejected: %v", err)
	}
}

func TestCopyArgsReleaseIDModeValidation(t *testing.T) {
	oldBatchFile, oldReleaseManifest := batchFile, releaseManifest
	oldOutput, oldReleaseID := copyOutput, copyReleaseID
	oldSrcBase, oldDestBase := srcBase, destBase
	oldFromRef, oldToRef := fromRef, toRef
	defer func() {
		batchFile, releaseManifest = oldBatchFile, oldReleaseManifest
		copyOutput, copyReleaseID = oldOutput, oldReleaseID
		srcBase, destBase = oldSrcBase, oldDestBase
		fromRef, toRef = oldFromRef, oldToRef
	}()

	copyReleaseID = "RE_2026.09.10.02"
	srcBase = ""
	destBase = ""
	fromRef = ""
	toRef = ""

	batchFile = "images.yml"
	releaseManifest = ""
	copyOutput = ""
	if err := copyArgs(nil, nil); err == nil || !strings.Contains(err.Error(), "--release-id requires --output") {
		t.Fatalf("batch error = %v, want output requirement", err)
	}

	batchFile = ""
	releaseManifest = ""
	if err := copyArgs(nil, []string{"source", "destination"}); err == nil || !strings.Contains(err.Error(), "--release-id requires --batch-file or --release-manifest") {
		t.Fatalf("positional error = %v, want input mode requirement", err)
	}
}

func TestLoadCopyBatchAcceptsSourceAlias(t *testing.T) {
	path := writeBatchFile(t, `
copy:
  - source: registry.example.com/team/config:old
    dest: registry.example.com/team/config:new
`)

	items, err := loadCopyBatch(path)
	if err != nil {
		t.Fatalf("loadCopyBatch: %v", err)
	}
	if items[0].source() != "registry.example.com/team/config:old" {
		t.Fatalf("source alias not used: %q", items[0].source())
	}
}

func TestLoadCopyBatchAcceptsDestTags(t *testing.T) {
	path := writeBatchFile(t, `
copy:
  - src: registry.example.com/team/config:old
    dest: registry.example.com/team/config:new
    dest_tags:
      - latest
      - stable
`)

	items, err := loadCopyBatch(path)
	if err != nil {
		t.Fatalf("loadCopyBatch: %v", err)
	}
	if got := strings.Join(items[0].DestTags, ","); got != "latest,stable" {
		t.Fatalf("DestTags = %q, want latest,stable", got)
	}
}

func TestPrepareBatchReleaseManifestInfersCommonDestinationTag(t *testing.T) {
	batch := []copyBatchItem{
		{Src: "source.example.com/team/api:build", Dest: "target.example.com/releases/api:2.4"},
		{Src: "source.example.com/team/config:build", Dest: "target.example.com/releases/config-form:2.4", ID: "config", AppName: "config", ContainerName: "application", DestTags: []string{"stable"}},
	}
	manifest, err := prepareBatchReleaseManifest(batch, "")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ReleaseID != "2.4" || manifest.RegistryBase != "target.example.com/releases" || len(manifest.Images) != 2 {
		t.Fatalf("manifest = %#v", manifest)
	}
	if got := manifest.Images[0]; got.ID != "api" || got.AppName != "api" || got.ContainerName != "api" || got.Image != "target.example.com/releases/api" || got.Tag != "2.4" {
		t.Fatalf("inferred image = %#v", got)
	}
	if got := manifest.Images[1]; got.ID != "config" || got.AppName != "config" || got.ContainerName != "application" || strings.Join(got.ExtraTags, ",") != "stable" {
		t.Fatalf("explicit image metadata = %#v", got)
	}
}

func TestPrepareBatchReleaseManifestRequiresReleaseIDForMixedTags(t *testing.T) {
	batch := []copyBatchItem{
		{Src: "source.example.com/api:build", Dest: "target.example.com/api:2.4"},
		{Src: "source.example.com/ui:build", Dest: "target.example.com/ui:3.0"},
	}
	if _, err := prepareBatchReleaseManifest(batch, ""); err == nil || !strings.Contains(err.Error(), "--release-id is required") {
		t.Fatalf("error = %v", err)
	}
	manifest, err := prepareBatchReleaseManifest(batch, "RE_2026.08.10.01")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ReleaseID != "RE_2026.08.10.01" {
		t.Fatalf("release ID = %q", manifest.ReleaseID)
	}
}

func TestLoadCopyBatchRejectsDuplicateDestination(t *testing.T) {
	path := writeBatchFile(t, `
copy:
  - src: registry.example.com/team/config:old
    dest: registry.example.com/team/config:new
  - src: registry.example.com/team/ticket:old
    dest: registry.example.com/team/config:new
`)

	_, err := loadCopyBatch(path)
	if err == nil || !strings.Contains(err.Error(), "duplicates destination") {
		t.Fatalf("loadCopyBatch error = %v, want duplicate destination error", err)
	}
}

func TestLoadCopyBatchRejectsDuplicateDestinationFromDestTags(t *testing.T) {
	path := writeBatchFile(t, `
copy:
  - src: registry.example.com/team/config:old
    dest: registry.example.com/team/config:new
    dest_tags:
      - latest
  - src: registry.example.com/team/ticket:old
    dest: registry.example.com/team/config:latest
`)

	_, err := loadCopyBatch(path)
	if err == nil || !strings.Contains(err.Error(), "duplicates destination") {
		t.Fatalf("loadCopyBatch error = %v, want duplicate destination error", err)
	}
}

func TestLoadCopyBatchRejectsEmptyDestTag(t *testing.T) {
	path := writeBatchFile(t, `
copy:
  - src: registry.example.com/team/config:old
    dest: registry.example.com/team/config:new
    dest_tags:
      - ""
`)

	_, err := loadCopyBatch(path)
	if err == nil || !strings.Contains(err.Error(), "dest_tags[0] is empty") {
		t.Fatalf("loadCopyBatch error = %v, want empty dest_tags error", err)
	}
}

func TestLoadCopyBatchRejectsConflictingDestinationAliases(t *testing.T) {
	path := writeBatchFile(t, `
copy:
  - src: registry.example.com/team/config:old
    dest: registry.example.com/team/config:new
    destination: registry.example.com/team/config:other
`)

	_, err := loadCopyBatch(path)
	if err == nil || !strings.Contains(err.Error(), "both dest and destination") {
		t.Fatalf("loadCopyBatch error = %v, want conflicting alias error", err)
	}
}

func TestLoadCopyBatchRejectsConflictingSourceAliases(t *testing.T) {
	path := writeBatchFile(t, `
copy:
  - src: registry.example.com/team/config:old
    source: registry.example.com/team/config:other
    dest: registry.example.com/team/config:new
`)

	_, err := loadCopyBatch(path)
	if err == nil || !strings.Contains(err.Error(), "both src and source") {
		t.Fatalf("loadCopyBatch error = %v, want conflicting alias error", err)
	}
}

func TestLoadReleaseCopyPlanUsesDigestAndBuildsDestinationManifest(t *testing.T) {
	path := writeReleaseFile(t, `
release_id: 2026.06.25.01
created_at: 2026-07-29T05:38:09Z
registry_base: registry.example.com/release
bundle:
  name: stable
  revision: abc123
images:
  - id: api
    app_name: api
    container_name: api
    image: registry.example.com/release/team/api
    tag: latest
    digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
`)

	plan, err := loadReleaseCopyPlan(path, "", "mirror.example.com/archive", "")
	if err != nil {
		t.Fatalf("loadReleaseCopyPlan: %v", err)
	}
	items := plan.Items
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	wantSource := "registry.example.com/release/team/api@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	if items[0].source() != wantSource {
		t.Fatalf("source = %q, want %q", items[0].source(), wantSource)
	}
	wantDestination := "mirror.example.com/archive/team/api:2026.06.25.01"
	if items[0].target() != wantDestination {
		t.Fatalf("destination = %q, want %q", items[0].target(), wantDestination)
	}
	if items[0].ExpectedDigest != "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef" {
		t.Fatalf("expected digest = %q", items[0].ExpectedDigest)
	}
	output := plan.Manifest
	if output.ReleaseID != "2026.06.25.01" ||
		output.RegistryBase != "mirror.example.com/archive" ||
		output.CreatedAt.IsZero() {
		t.Fatalf("output manifest metadata = %#v", output)
	}
	if output.Bundle == nil || output.Bundle.Name != "stable" || output.Bundle.Revision != "abc123" {
		t.Fatalf("output bundle = %#v", output.Bundle)
	}
	if len(output.Images) != 1 {
		t.Fatalf("output images = %#v", output.Images)
	}
	image := output.Images[0]
	if image.Image != "mirror.example.com/archive/team/api" ||
		image.Tag != "2026.06.25.01" ||
		image.Digest != items[0].ExpectedDigest {
		t.Fatalf("output image = %#v", image)
	}
	if image.Source == nil ||
		image.Source.Image != "registry.example.com/release/team/api" ||
		image.Source.Tag != "latest" ||
		image.Source.Digest != items[0].ExpectedDigest {
		t.Fatalf("output source = %#v", image.Source)
	}
	if len(image.ExtraTags) != 0 || len(output.ExtraTags) != 0 {
		t.Fatalf("copied manifest claims aliases that were not copied: %#v", output)
	}
}

func TestLoadReleaseCopyPlanOverridesDestinationReleaseID(t *testing.T) {
	path := writeReleaseFile(t, `
release_id: 2026.06.25.01
registry_base: registry.example.com/release
images:
  - id: api
    app_name: api
    container_name: application
    image: registry.example.com/release/team/api
    tag: original-tag
    digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
`)

	plan, err := loadReleaseCopyPlan(path, "", "mirror.example.com/archive", "RE_2026.09.10.02")
	if err != nil {
		t.Fatalf("loadReleaseCopyPlan: %v", err)
	}
	if got, want := plan.Items[0].source(), "registry.example.com/release/team/api@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"; got != want {
		t.Fatalf("source = %q, want immutable original %q", got, want)
	}
	if got, want := plan.Items[0].target(), "mirror.example.com/archive/team/api:RE_2026.09.10.02"; got != want {
		t.Fatalf("destination = %q, want %q", got, want)
	}
	if got := plan.Manifest.ReleaseID; got != "RE_2026.09.10.02" {
		t.Fatalf("output release ID = %q", got)
	}
	image := plan.Manifest.Images[0]
	if image.Tag != "RE_2026.09.10.02" {
		t.Fatalf("output image tag = %q", image.Tag)
	}
	if image.Source == nil || image.Source.Tag != "original-tag" {
		t.Fatalf("output source metadata = %#v", image.Source)
	}
}

func TestLoadReleaseCopyPlanRejectsInvalidReleaseIDOverride(t *testing.T) {
	path := writeReleaseFile(t, `
release_id: release-1
registry_base: registry.example.com/release
images:
  - image: registry.example.com/release/api
    digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
`)

	_, err := loadReleaseCopyPlan(path, "", "mirror.example.com/release", "invalid release id")
	if err == nil || !strings.Contains(err.Error(), "invalid destination release tag") {
		t.Fatalf("error = %v, want invalid override error", err)
	}
}

func TestLoadReleaseCopyPlanRequiresSourceBaseWithoutRegistryBase(t *testing.T) {
	path := writeReleaseFile(t, `
release_id: release-1
images:
  - app_name: api
    image: registry.example.com/release/api
    digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
`)

	_, err := loadReleaseCopyPlan(path, "", "mirror.example.com/release", "")
	if err == nil || !strings.Contains(err.Error(), "--src-base is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadReleaseCopyPlanRejectsImageOutsideSourceBase(t *testing.T) {
	path := writeReleaseFile(t, `
release_id: release-1
images:
  - app_name: api
    image: other.example.com/release/api
    digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
`)

	_, err := loadReleaseCopyPlan(
		path,
		"https://registry.example.com/release/",
		"docker://mirror.example.com/release/",
		"",
	)
	if err == nil || !strings.Contains(err.Error(), "outside source base") {
		t.Fatalf("error = %v", err)
	}
}

func TestCopyArgsExplainsRenamedBaseFlags(t *testing.T) {
	oldSrc, oldDest := legacySrcPrefix, legacyDestPrefix
	defer func() { legacySrcPrefix, legacyDestPrefix = oldSrc, oldDest }()

	legacySrcPrefix = "registry.example.com/source"
	legacyDestPrefix = ""
	err := copyArgs(nil, nil)
	if err == nil || !strings.Contains(err.Error(), "--src-prefix was renamed to --src-base") {
		t.Fatalf("source rename error = %v", err)
	}

	legacyDestPrefix = "registry.example.com/destination"
	err = copyArgs(nil, nil)
	if err == nil || !strings.Contains(err.Error(), "--src-prefix and --dest-prefix were renamed to --src-base and --dest-base") {
		t.Fatalf("combined rename error = %v", err)
	}
}

func TestLoadReleaseCopyPlanRejectsUnknownFields(t *testing.T) {
	path := writeReleaseFile(t, `
release_id: release-1
unexpected: true
images:
  - app_name: api
    image: registry.example.com/release/api
    digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
`)

	_, err := loadReleaseCopyPlan(path, "registry.example.com/release", "mirror.example.com/release", "")
	if err == nil || !strings.Contains(err.Error(), "field unexpected not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestSafeCloseToleratesClosedChannel(t *testing.T) {
	ch := make(chan v1.Update)
	close(ch)
	safeClose(ch)
}

func writeReleaseFile(t *testing.T, data string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "release.yml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(data)+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func writeBatchFile(t *testing.T, data string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "images.yml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(data)+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestLoadReleaseCopyPlanValidatesOnlyDestinationReleaseID(t *testing.T) {
	path := writeReleaseFile(t, `apiVersion: oci-toolbox/v2
kind: ImageRelease
release_id: customer/batch
repository_prefix: registry.example.com/release
images:
  - app_name: api
    repository: registry.example.com/release/api
    digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
`)
	plan, err := loadReleaseCopyPlan(path, "", "mirror.example.com/archive", "release-2")
	if err != nil {
		t.Fatal(err)
	}
	if got := plan.Items[0].target(); got != "mirror.example.com/archive/api:release-2" {
		t.Fatal(got)
	}
	if _, err := loadReleaseCopyPlan(path, "", "mirror.example.com/archive", ""); err == nil {
		t.Fatal("accepted a logical ID as an invalid destination tag")
	}
}
