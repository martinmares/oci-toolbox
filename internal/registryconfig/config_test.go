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

func TestLoadGitLabTokenFromEnvironment(t *testing.T) {
	t.Setenv("GITLAB_TEST_TOKEN", "test-generated-token")
	path := filepath.Join(t.TempDir(), "registry.yml")
	if err := os.WriteFile(path, []byte(`
registry:
  url: https://gitlab.example.com:5050
gitlab:
  url: https://gitlab.example.com
  project: "1582"
  token: ${GITLAB_TEST_TOKEN}
cleanup:
  delete_via: gitlab
`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Cleanup.DeleteVia != "gitlab" || cfg.GitLab.Project != "1582" || cfg.GitLab.Token != "test-generated-token" {
		t.Fatal("GitLab configuration or token environment expansion was not loaded")
	}
}
