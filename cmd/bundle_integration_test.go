package cmd

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"net/url"
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
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/martinmares/oci-toolbox/internal/bundle"
	"github.com/martinmares/oci-toolbox/internal/testutil"
)

func TestBundlePublishAndReleaseReconstruct(t *testing.T) {
	server := httptest.NewServer(registry.New())
	defer server.Close()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	sourceRef, err := name.NewTag(serverURL.Host+"/source/api:build-1", name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	sourceImage := testutil.NewImageWithFiles(t, map[string][]byte{"app.txt": []byte("hello")})
	sourceConfig, err := sourceImage.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	sourceConfig.OS = "linux"
	sourceConfig.Architecture = "amd64"
	sourceImage, err = mutate.ConfigFile(sourceImage, sourceConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(sourceRef, sourceImage); err != nil {
		t.Fatalf("write source image: %v", err)
	}
	sourceDigest, err := sourceImage.Digest()
	if err != nil {
		t.Fatal(err)
	}

	bundlePath := filepath.Join(t.TempDir(), "bundle.yml")
	bundleYAML := fmt.Sprintf(`name: stable
registries:
  source:
    url: %s
  target:
    url: %s
    project: release
extra_tags: [stable]
images:
  - id: api
    source:
      repository: source/api
      tag: build-1
    target:
      repository: api
    deployment:
      app_name: api
      container_name: application
`, serverURL.Host, serverURL.Host)
	if err := os.WriteFile(bundlePath, []byte(bundleYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	restore := saveBundleCommandState()
	t.Cleanup(restore)
	bundleOptions.releaseID = "release-1"
	bundleOptions.extraTags = nil
	bundlePublishOptions.srcInsecure = true
	bundlePublishOptions.destInsecure = true

	firstOutput := filepath.Join(t.TempDir(), "release-1.yml")
	bundlePublishOptions.output = firstOutput
	cmd := &cobra.Command{}
	if err := runBundlePublish(cmd, []string{bundlePath}); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	assertPublishedRelease(t, firstOutput, sourceDigest.String())

	secondOutput := filepath.Join(t.TempDir(), "release-1-retry.yml")
	bundlePublishOptions.output = secondOutput
	if err := runBundlePublish(cmd, []string{bundlePath}); err != nil {
		t.Fatalf("idempotent publish: %v", err)
	}
	assertPublishedRelease(t, secondOutput, sourceDigest.String())

	targetRef, err := name.NewTag(serverURL.Host+"/release/api:release-1", name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	targetDesc, err := remote.Get(targetRef)
	if err != nil {
		t.Fatal(err)
	}
	if targetDesc.Digest.String() != sourceDigest.String() {
		t.Fatalf("target digest = %s, want %s", targetDesc.Digest, sourceDigest)
	}
	stableRef, err := name.NewTag(serverURL.Host+"/release/api:stable", name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	stableDesc, err := remote.Get(stableRef)
	if err != nil {
		t.Fatal(err)
	}
	if stableDesc.Digest.String() != sourceDigest.String() {
		t.Fatalf("stable digest = %s, want %s", stableDesc.Digest, sourceDigest)
	}

	reconstructOutput := filepath.Join(t.TempDir(), "reconstructed.yml")
	targetDigestRef, err := name.NewDigest(
		serverURL.Host+"/release/api@"+sourceDigest.String(),
		name.Insecure,
	)
	if err != nil {
		t.Fatal(err)
	}
	reconstructOptions = struct {
		file         string
		output       string
		releaseID    string
		creds        string
		insecure     bool
		progress     bool
		discoverTags bool
		extraTags    []string
	}{
		output:       reconstructOutput,
		releaseID:    "release-1",
		insecure:     true,
		discoverTags: true,
	}
	if err := runReleaseReconstruct(cmd, []string{targetDigestRef.String()}); err != nil {
		t.Fatalf("reconstruct: %v", err)
	}
	reconstructed := readReleaseManifest(t, reconstructOutput)
	if reconstructed.ReleaseID != "release-1" {
		t.Fatalf("release ID = %q", reconstructed.ReleaseID)
	}
	if len(reconstructed.Images) != 1 || reconstructed.Images[0].Digest != sourceDigest.String() {
		t.Fatalf("reconstructed images = %#v", reconstructed.Images)
	}
	if reconstructed.Images[0].Tag != "release-1" {
		t.Fatalf("reconstructed tag = %q", reconstructed.Images[0].Tag)
	}
	if got := strings.Join(reconstructed.Images[0].ExtraTags, ","); got != "stable" {
		t.Fatalf("reconstructed extra tags = %q", got)
	}

	armImage := testutil.NewImageWithFiles(t, map[string][]byte{"app.txt": []byte("arm")})
	armConfig, err := armImage.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	armConfig.OS = "linux"
	armConfig.Architecture = "arm64"
	armImage, err = mutate.ConfigFile(armImage, armConfig)
	if err != nil {
		t.Fatal(err)
	}
	index := mutate.AppendManifests(
		empty.Index,
		mutate.IndexAddendum{
			Add: sourceImage,
			Descriptor: v1.Descriptor{
				Platform: &v1.Platform{OS: "linux", Architecture: "amd64"},
			},
		},
		mutate.IndexAddendum{
			Add: armImage,
			Descriptor: v1.Descriptor{
				Platform: &v1.Platform{OS: "linux", Architecture: "arm64"},
			},
		},
	)
	indexRef, err := name.NewTag(serverURL.Host+"/release/multi:latest", name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.WriteIndex(indexRef, index); err != nil {
		t.Fatalf("write index: %v", err)
	}
	indexDigest, err := index.Digest()
	if err != nil {
		t.Fatal(err)
	}

	multiOutput := filepath.Join(t.TempDir(), "multi.yml")
	reconstructOptions = struct {
		file         string
		output       string
		releaseID    string
		creds        string
		insecure     bool
		progress     bool
		discoverTags bool
		extraTags    []string
	}{
		output:   multiOutput,
		insecure: true,
		progress: true,
	}
	var progressOutput bytes.Buffer
	cmd.SetErr(&progressOutput)
	if err := runReleaseReconstruct(cmd, []string{indexRef.String()}); err != nil {
		t.Fatalf("reconstruct index: %v", err)
	}
	multi := readReleaseManifest(t, multiOutput)
	if multi.Platform != "multi-platform" ||
		len(multi.Images) != 1 ||
		multi.Images[0].Platform != "multi-platform" ||
		multi.Images[0].Digest != indexDigest.String() {
		t.Fatalf("multi-platform manifest = %#v", multi)
	}
	if !bytes.Contains(progressOutput.Bytes(), []byte("100%")) {
		t.Fatalf("progress output = %q", progressOutput.String())
	}
}

func assertPublishedRelease(t *testing.T, path, digest string) {
	t.Helper()
	manifest := readReleaseManifest(t, path)
	if manifest.ReleaseID != "release-1" {
		t.Fatalf("release ID = %q", manifest.ReleaseID)
	}
	if manifest.Bundle == nil || manifest.Bundle.Name != "stable" {
		t.Fatalf("bundle = %#v", manifest.Bundle)
	}
	if len(manifest.Images) != 1 {
		t.Fatalf("images = %#v", manifest.Images)
	}
	image := manifest.Images[0]
	if image.Digest != digest || image.Source == nil || image.Source.Digest != digest {
		t.Fatalf("image = %#v", image)
	}
}

func readReleaseManifest(t *testing.T, path string) bundle.ReleaseManifest {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var manifest bundle.ReleaseManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func saveBundleCommandState() func() {
	oldBundleOptions := bundleOptions
	oldBundlePublishOptions := bundlePublishOptions
	oldReconstructOptions := reconstructOptions
	oldRecipePaths := recipePaths
	oldSrcCreds, oldDestCreds := srcCreds, destCreds
	oldDebugMode, oldDestNoReuse := debugMode, destNoReuse
	oldShowProgress, oldShowProgressJSON := showProgress, showProgressJSON
	oldReduceLayers, oldReduceChanges := reducelayers, reduceChanges
	oldWrapEntrypoint, oldNoWrapEntrypoint := wrapEntrypoint, noWrapEntrypoint
	oldSrcInsecure, oldDestInsecure := srcInsecure, destInsecure

	return func() {
		bundleOptions = oldBundleOptions
		bundlePublishOptions = oldBundlePublishOptions
		reconstructOptions = oldReconstructOptions
		recipePaths = oldRecipePaths
		srcCreds, destCreds = oldSrcCreds, oldDestCreds
		debugMode, destNoReuse = oldDebugMode, oldDestNoReuse
		showProgress, showProgressJSON = oldShowProgress, oldShowProgressJSON
		reducelayers, reduceChanges = oldReduceLayers, oldReduceChanges
		wrapEntrypoint, noWrapEntrypoint = oldWrapEntrypoint, oldNoWrapEntrypoint
		srcInsecure, destInsecure = oldSrcInsecure, oldDestInsecure
	}
}
