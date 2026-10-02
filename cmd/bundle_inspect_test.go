package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/martinmares/oci-toolbox/internal/bundle"
	"github.com/martinmares/oci-toolbox/internal/testutil"
	"github.com/spf13/cobra"
)

func TestInspectReadsOnlyManifestsAndLocksMovedTag(t *testing.T) {
	handler := registry.New()
	inspecting := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if inspecting && (strings.Contains(r.URL.Path, "/blobs/") || (r.Method != "GET" && r.Method != "HEAD")) {
			t.Errorf("inspection transferred data or modified registry: %s %s", r.Method, r.URL.Path)
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "http://")
	image := testutil.NewImageWithFiles(t, map[string][]byte{"test": []byte("reviewed")})
	cfg, err := image.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	cfg.OS, cfg.Architecture = "linux", "amd64"
	image, err = mutate.ConfigFile(image, cfg)
	if err != nil {
		t.Fatal(err)
	}
	tag, _ := name.NewTag(host+"/source/api:latest", name.Insecure)
	if err := remote.Write(tag, image); err != nil {
		t.Fatal(err)
	}
	digest, _ := image.Digest()
	filename := filepath.Join(t.TempDir(), "bundle.yml")
	content := fmt.Sprintf("name: test\nregistries:\n  source:\n    url: %s\n  target:\n    url: %s\nimages:\n  - id: api\n    source:\n      repository: source/api\n      tag: latest\n    target:\n      repository: target/api\n    deployment:\n      app_name: api\n      container_name: main\n", host, host)
	if err := os.WriteFile(filename, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	restore := saveBundleCommandState()
	defer restore()
	previous := bundleDigestPlanPath
	defer func() { bundleDigestPlanPath = previous }()
	bundleOptions.releaseID = "release-1"
	bundlePublishOptions.srcInsecure, bundlePublishOptions.destInsecure = true, true
	var output bytes.Buffer
	command := &cobra.Command{}
	command.SetOut(&output)
	command.SetContext(context.Background())
	inspecting = true
	err = bundleInspectCmd.RunE(command, []string{filename})
	inspecting = false
	if err != nil {
		t.Fatal(err)
	}
	var plan inspectedBundle
	if err := json.Unmarshal(output.Bytes(), &plan); err != nil {
		t.Fatal(err)
	}
	if len(plan.Images) != 1 || plan.Images[0].Digest != digest.String() {
		t.Fatalf("wrong plan: %+v", plan)
	}
	locked := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(locked, output.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	// Re-point the mutable tag after review. Publishing must retain the old digest.
	changed := testutil.NewImageWithFiles(t, map[string][]byte{"test": []byte("changed")})
	changed, err = mutate.ConfigFile(changed, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(tag, changed); err != nil {
		t.Fatal(err)
	}
	bundleDigestPlanPath = locked
	bundlePublishOptions.output = filepath.Join(t.TempDir(), "release.yml")
	if err := runBundlePublish(command, []string{filename}); err != nil {
		t.Fatal(err)
	}
	manifest, err := bundle.LoadRelease(bundlePublishOptions.output)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Images[0].Digest != digest.String() {
		t.Fatalf("published moved tag: %s", manifest.Images[0].Digest)
	}
	effective, _, err := effectiveBundlePlan(filename)
	if err != nil {
		t.Fatal(err)
	}
	effective.Images[0].TargetRef = "other.example/api:release-1"
	if err := lockBundlePlan(effective, locked); err == nil {
		t.Fatal("accepted changed destination")
	}
}
