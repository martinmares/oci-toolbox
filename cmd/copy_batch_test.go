package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadCopyBatch(t *testing.T) {
	path := writeBatchFile(t, `
copy:
  - src: harbor.cetin/tsm/release/tsm-config:old
    dest: harbor.cetin/tsm/release/tsm-config:new
  - src: harbor.cetin/tsm/release/tsm-ticket:old
    destination: harbor.cetin/tsm/release/tsm-ticket:new
`)

	items, err := loadCopyBatch(path)
	if err != nil {
		t.Fatalf("loadCopyBatch: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items = %d, want 2", len(items))
	}
	if items[1].target() != "harbor.cetin/tsm/release/tsm-ticket:new" {
		t.Fatalf("destination alias not used: %q", items[1].target())
	}
	if items[0].source() != "harbor.cetin/tsm/release/tsm-config:old" {
		t.Fatalf("src not used: %q", items[0].source())
	}
}

func TestLoadCopyBatchAcceptsSourceAlias(t *testing.T) {
	path := writeBatchFile(t, `
copy:
  - source: harbor.cetin/tsm/release/tsm-config:old
    dest: harbor.cetin/tsm/release/tsm-config:new
`)

	items, err := loadCopyBatch(path)
	if err != nil {
		t.Fatalf("loadCopyBatch: %v", err)
	}
	if items[0].source() != "harbor.cetin/tsm/release/tsm-config:old" {
		t.Fatalf("source alias not used: %q", items[0].source())
	}
}

func TestLoadCopyBatchRejectsDuplicateDestination(t *testing.T) {
	path := writeBatchFile(t, `
copy:
  - src: harbor.cetin/tsm/release/tsm-config:old
    dest: harbor.cetin/tsm/release/tsm-config:new
  - src: harbor.cetin/tsm/release/tsm-ticket:old
    dest: harbor.cetin/tsm/release/tsm-config:new
`)

	_, err := loadCopyBatch(path)
	if err == nil || !strings.Contains(err.Error(), "duplicates destination") {
		t.Fatalf("loadCopyBatch error = %v, want duplicate destination error", err)
	}
}

func TestLoadCopyBatchRejectsConflictingDestinationAliases(t *testing.T) {
	path := writeBatchFile(t, `
copy:
  - src: harbor.cetin/tsm/release/tsm-config:old
    dest: harbor.cetin/tsm/release/tsm-config:new
    destination: harbor.cetin/tsm/release/tsm-config:other
`)

	_, err := loadCopyBatch(path)
	if err == nil || !strings.Contains(err.Error(), "both dest and destination") {
		t.Fatalf("loadCopyBatch error = %v, want conflicting alias error", err)
	}
}

func TestLoadCopyBatchRejectsConflictingSourceAliases(t *testing.T) {
	path := writeBatchFile(t, `
copy:
  - src: harbor.cetin/tsm/release/tsm-config:old
    source: harbor.cetin/tsm/release/tsm-config:other
    dest: harbor.cetin/tsm/release/tsm-config:new
`)

	_, err := loadCopyBatch(path)
	if err == nil || !strings.Contains(err.Error(), "both src and source") {
		t.Fatalf("loadCopyBatch error = %v, want conflicting alias error", err)
	}
}

func writeBatchFile(t *testing.T, data string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "images.yml")
	if err := os.WriteFile(path, []byte(strings.TrimSpace(data)+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}
