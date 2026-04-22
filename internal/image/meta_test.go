package image_test

import (
	"testing"

	"github.com/datalite/oci-patch/internal/image"
	"github.com/datalite/oci-patch/internal/testutil"
)

func TestSetEnv_new(t *testing.T) {
	img := testutil.NewImage(t)
	img2, err := image.SetEnv(img, "MY_VAR", "hello")
	if err != nil {
		t.Fatalf("SetEnv: %v", err)
	}
	cfg, _ := img2.ConfigFile()
	for _, e := range cfg.Config.Env {
		if e == "MY_VAR=hello" {
			return
		}
	}
	t.Errorf("MY_VAR=hello not found in env: %v", cfg.Config.Env)
}

func TestSetEnv_overrides(t *testing.T) {
	img := testutil.NewImage(t)
	img2, _ := image.SetEnv(img, "MY_VAR", "first")
	img3, err := image.SetEnv(img2, "MY_VAR", "second")
	if err != nil {
		t.Fatalf("SetEnv override: %v", err)
	}
	cfg, _ := img3.ConfigFile()
	count := 0
	for _, e := range cfg.Config.Env {
		if e == "MY_VAR=second" {
			count++
		}
		if e == "MY_VAR=first" {
			t.Errorf("old value MY_VAR=first still present")
		}
	}
	if count != 1 {
		t.Errorf("expected exactly one MY_VAR=second, got %d", count)
	}
}

func TestSetLabel(t *testing.T) {
	img := testutil.NewImage(t)
	img2, err := image.SetLabel(img, "org.example.version", "1.2.3")
	if err != nil {
		t.Fatalf("SetLabel: %v", err)
	}
	cfg, _ := img2.ConfigFile()
	if cfg.Config.Labels["org.example.version"] != "1.2.3" {
		t.Errorf("label not set, got: %v", cfg.Config.Labels)
	}
}

func TestSetEntrypoint(t *testing.T) {
	img := testutil.NewImage(t)
	want := []string{"/bin/myapp", "--config", "/etc/myapp.conf"}
	img2, err := image.SetEntrypoint(img, want)
	if err != nil {
		t.Fatalf("SetEntrypoint: %v", err)
	}
	cfg, _ := img2.ConfigFile()
	if len(cfg.Config.Entrypoint) != len(want) {
		t.Fatalf("entrypoint length: got %d, want %d", len(cfg.Config.Entrypoint), len(want))
	}
	for i, v := range want {
		if cfg.Config.Entrypoint[i] != v {
			t.Errorf("entrypoint[%d]: got %q, want %q", i, cfg.Config.Entrypoint[i], v)
		}
	}
}

func TestSetCmd(t *testing.T) {
	img := testutil.NewImage(t)
	want := []string{"--port", "8080"}
	img2, err := image.SetCmd(img, want)
	if err != nil {
		t.Fatalf("SetCmd: %v", err)
	}
	cfg, _ := img2.ConfigFile()
	if len(cfg.Config.Cmd) != len(want) {
		t.Fatalf("cmd length: got %d, want %d", len(cfg.Config.Cmd), len(want))
	}
	for i, v := range want {
		if cfg.Config.Cmd[i] != v {
			t.Errorf("cmd[%d]: got %q, want %q", i, cfg.Config.Cmd[i], v)
		}
	}
}

func TestSetUser(t *testing.T) {
	img := testutil.NewImage(t)
	img2, err := image.SetUser(img, "1001:1001")
	if err != nil {
		t.Fatalf("SetUser: %v", err)
	}
	cfg, _ := img2.ConfigFile()
	if cfg.Config.User != "1001:1001" {
		t.Errorf("user: got %q, want %q", cfg.Config.User, "1001:1001")
	}
}

func TestSetWorkdir(t *testing.T) {
	img := testutil.NewImage(t)
	img2, err := image.SetWorkdir(img, "/app")
	if err != nil {
		t.Fatalf("SetWorkdir: %v", err)
	}
	cfg, _ := img2.ConfigFile()
	if cfg.Config.WorkingDir != "/app" {
		t.Errorf("workdir: got %q, want %q", cfg.Config.WorkingDir, "/app")
	}
}
