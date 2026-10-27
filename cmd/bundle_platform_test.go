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
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
	"github.com/google/go-containerregistry/pkg/v1/types"
	"github.com/spf13/cobra"
)

func TestBundlePublishSelectsAMD64FromIndex(t *testing.T) {
	for _, mediaType := range []types.MediaType{types.OCIImageIndex, types.DockerManifestList} {
		t.Run(string(mediaType), func(t *testing.T) {
			handler := registry.New()
			inspecting := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if inspecting && (strings.Contains(r.URL.Path, "/blobs/") || (r.Method != http.MethodGet && r.Method != http.MethodHead)) {
					t.Errorf("inspect transferred blobs or wrote data: %s %s", r.Method, r.URL.Path)
				}
				handler.ServeHTTP(w, r)
			}))
			defer server.Close()
			host := strings.TrimPrefix(server.URL, "http://")
			source, _ := name.NewTag(host+"/source/api:latest", name.Insecure)
			writeTestRegistryIndex(t, source.Name())
			desc, err := remote.Get(source)
			if err != nil {
				t.Fatal(err)
			}
			index, err := desc.ImageIndex()
			if err != nil {
				t.Fatal(err)
			}
			index = mutate.IndexMediaType(index, mediaType)
			if err := remote.WriteIndex(source, index); err != nil {
				t.Fatal(err)
			}
			manifest, err := index.IndexManifest()
			if err != nil {
				t.Fatal(err)
			}
			expected := manifest.Manifests[0].Digest.String()
			parentDigest, err := index.Digest()
			if err != nil {
				t.Fatal(err)
			}
			filename := filepath.Join(t.TempDir(), "bundle.yml")
			body := fmt.Sprintf("name: stable\nregistries:\n  source:\n    url: %s\n  target:\n    url: %s\n    project: release\nimages:\n  - id: api\n    source:\n      repository: source/api\n      tag: latest\n    target:\n      repository: api\n    deployment:\n      app_name: api\n      container_name: application\n", host, host)
			if err := os.WriteFile(filename, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(saveBundleCommandState())
			previousPlan := bundleDigestPlanPath
			t.Cleanup(func() { bundleDigestPlanPath = previousPlan })
			bundleOptions.releaseID = "release-1"
			bundlePublishOptions.srcInsecure, bundlePublishOptions.destInsecure = true, true
			bundlePublishOptions.output = filepath.Join(t.TempDir(), "release.yml")
			bundleDigestPlanPath = ""
			var output bytes.Buffer
			command := &cobra.Command{}
			command.SetContext(context.Background())
			command.SetOut(&output)
			inspecting = true
			err = bundleInspectCmd.RunE(command, []string{filename})
			inspecting = false
			if err != nil {
				t.Fatal(err)
			}
			var inspected inspectedBundle
			if err := json.Unmarshal(output.Bytes(), &inspected); err != nil {
				t.Fatal(err)
			}
			if len(inspected.Images) != 1 || inspected.Images[0].Digest != expected || expected == parentDigest.String() {
				t.Fatalf("inspect did not select amd64 child: %+v", inspected)
			}
			bundleDigestPlanPath = filepath.Join(t.TempDir(), "locked.json")
			if err := os.WriteFile(bundleDigestPlanPath, output.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			if err := runBundlePublish(command, []string{filename}); err != nil {
				t.Fatal(err)
			}
			assertPublishedRelease(t, bundlePublishOptions.output, expected)
			target, _ := name.NewTag(host+"/release/api:release-1", name.Insecure)
			published, err := remote.Get(target)
			if err != nil {
				t.Fatal(err)
			}
			if published.MediaType.IsIndex() || published.Digest.String() != expected {
				t.Fatal("published an index or the wrong architecture")
			}
			image, err := published.Image()
			if err != nil {
				t.Fatal(err)
			}
			config, err := image.ConfigFile()
			if err != nil {
				t.Fatal(err)
			}
			if config.OS != "linux" || config.Architecture != "amd64" {
				t.Fatalf("wrong platform: %s/%s", config.OS, config.Architecture)
			}

			// An index without amd64 must fail during preflight without writing a release.
			armImage, err := index.Image(manifest.Manifests[1].Digest)
			if err != nil {
				t.Fatal(err)
			}
			armIndex := mutate.AppendManifests(empty.Index, mutate.IndexAddendum{Add: armImage, Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "arm64"}}})
			if err := remote.WriteIndex(source, armIndex); err != nil {
				t.Fatal(err)
			}
			bundleDigestPlanPath = ""
			bundleOptions.releaseID = "release-2"
			bundlePublishOptions.output = filepath.Join(t.TempDir(), "missing-amd64.yml")
			if err := runBundlePublish(command, []string{filename}); err == nil || !strings.Contains(err.Error(), "linux/amd64") {
				t.Fatalf("missing amd64 error: %v", err)
			}
			if _, err := os.Stat(bundlePublishOptions.output); !os.IsNotExist(err) {
				t.Fatalf("release was written on failure: %v", err)
			}
		})
	}
}
