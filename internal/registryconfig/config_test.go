package registryconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.yml")
	if err := os.WriteFile(path, []byte(`
registry:
  url: https://registry.example.com
unknown: true
`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load accepted an unknown field")
	}
}

func TestLoadAppliesCleanupDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.yml")
	if err := os.WriteFile(path, []byte(`
registry:
  url: https://registry.example.com
repositories:
  - name: team/app
`), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Cleanup.KeepCount != 14 || cfg.Cleanup.Mode != "timestamp" {
		t.Fatalf("cleanup defaults = %#v", cfg.Cleanup)
	}
}
