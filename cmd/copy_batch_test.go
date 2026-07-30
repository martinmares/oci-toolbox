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

func TestLoadReleaseCopyBatchUsesDigestAndReleaseID(t *testing.T) {
	path := writeReleaseFile(t, `
release_id: 2026.06.25.01
created_at: 2026-07-29T05:38:09Z
registry_base: registry.example.com/release
images:
  - id: api
    app_name: api
    container_name: api
    image: registry.example.com/release/team/api
    tag: latest
    digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
`)

	items, err := loadReleaseCopyBatch(path, "", "mirror.example.com/archive")
	if err != nil {
		t.Fatalf("loadReleaseCopyBatch: %v", err)
	}
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
}

func TestLoadReleaseCopyBatchRequiresSourcePrefixWithoutRegistryBase(t *testing.T) {
	path := writeReleaseFile(t, `
release_id: release-1
images:
  - app_name: api
    image: registry.example.com/release/api
    digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
`)

	_, err := loadReleaseCopyBatch(path, "", "mirror.example.com/release")
	if err == nil || !strings.Contains(err.Error(), "--src-prefix is required") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadReleaseCopyBatchRejectsImageOutsideSourcePrefix(t *testing.T) {
	path := writeReleaseFile(t, `
release_id: release-1
images:
  - app_name: api
    image: other.example.com/release/api
    digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
`)

	_, err := loadReleaseCopyBatch(
		path,
		"https://registry.example.com/release/",
		"docker://mirror.example.com/release/",
	)
	if err == nil || !strings.Contains(err.Error(), "outside source prefix") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadReleaseCopyBatchRejectsUnknownFields(t *testing.T) {
	path := writeReleaseFile(t, `
release_id: release-1
unexpected: true
images:
  - app_name: api
    image: registry.example.com/release/api
    digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef
`)

	_, err := loadReleaseCopyBatch(path, "registry.example.com/release", "mirror.example.com/release")
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
