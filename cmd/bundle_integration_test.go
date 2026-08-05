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
	"github.com/martinmares/oci-toolbox/internal/progress"
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
	reconstructOptions = releaseReconstructOptions{
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
	published, err := bundle.LoadRelease(firstOutput)
	if err != nil {
		t.Fatal(err)
	}
	verifyReport, err := verifyReleaseRegistry(t.Context(), firstOutput, published, registryOptions("", true), nil)
	if err != nil {
		t.Fatal(err)
	}
	if verifyReport.Summary != (releaseVerifySummary{Verified: 1}) {
		t.Fatalf("registry verification = %#v", verifyReport)
	}
	var verifyProgress bytes.Buffer
	verifyCounter := progress.NewCounter(len(published.Images), "verify registry images", &verifyProgress)
	verifyReport, err = verifyReleaseRegistry(t.Context(), firstOutput, published, registryOptions("", true), verifyCounter)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyCounter.Finish(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"verify tag 1/1: api/application", "1/1", "100%"} {
		if !strings.Contains(verifyProgress.String(), want) {
			t.Fatalf("verification progress does not contain %q: %q", want, verifyProgress.String())
		}
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
	reconstructOptions = releaseReconstructOptions{
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

	indexCopyRef, err := name.NewTag(serverURL.Host+"/release/multi-copy:release-1", name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	if err := runCopyOne(
		cmd.Context(),
		indexRef.String(),
		indexCopyRef.String(),
		"",
		nil,
		copyRunOptions{
			SrcInsecure:    true,
			DestInsecure:   true,
			ExpectedDigest: indexDigest.String(),
		},
	); err != nil {
		t.Fatalf("copy image index: %v", err)
	}
	copiedIndex, err := remote.Get(indexCopyRef)
	if err != nil {
		t.Fatalf("inspect copied image index: %v", err)
	}
	if !copiedIndex.MediaType.IsIndex() || copiedIndex.Digest.String() != indexDigest.String() {
		t.Fatalf("copied index descriptor = %#v, want digest %s", copiedIndex.Descriptor, indexDigest)
	}
}

func TestSelectiveBundlePublishUpdatesOnlySelectedDeploymentTargets(t *testing.T) {
	server := httptest.NewServer(registry.New())
	defer server.Close()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	apiV1 := writeTestRegistryImage(t, serverURL.Host+"/source/api:build-1", "api-v1")
	workerV1 := writeTestRegistryImage(t, serverURL.Host+"/source/worker:build-1", "worker-v1")
	tempDir := t.TempDir()
	bundlePath := filepath.Join(tempDir, "bundle.yml")
	writeSelectiveBundle := func(apiTag, workerTag string) {
		t.Helper()
		data := fmt.Sprintf(`name: selective
registries:
  source:
    url: %s
  target:
    url: %s
    project: release
images:
  - id: api
    source: {repository: source/api, tag: %s}
    target: {repository: api}
    deployment: {app_name: api, container_name: application}
  - id: worker
    source: {repository: source/worker, tag: %s}
    target: {repository: worker}
    deployment: {app_name: worker, container_name: application}
`, serverURL.Host, serverURL.Host, apiTag, workerTag)
		if err := os.WriteFile(bundlePath, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	restore := saveBundleCommandState()
	t.Cleanup(restore)
	bundlePublishOptions.srcInsecure = true
	bundlePublishOptions.destInsecure = true
	cmd := &cobra.Command{}

	writeSelectiveBundle("build-1", "build-1")
	bundleOptions.releaseID = "release-1"
	baseOutput := filepath.Join(tempDir, "release-1.yml")
	bundlePublishOptions.output = baseOutput
	if err := runBundlePublish(cmd, []string{bundlePath}); err != nil {
		t.Fatalf("base publish: %v", err)
	}
	baseOnlyDigest := writeTestRegistryIndex(t, serverURL.Host+"/release/sidecar:release-1")
	baseManifest := readReleaseManifest(t, baseOutput)
	baseManifest.Images = append(baseManifest.Images, bundle.ReleaseImage{
		ID: "sidecar", AppName: "platform", ContainerName: "sidecar",
		Image: serverURL.Host + "/release/sidecar", Tag: "release-1", Digest: baseOnlyDigest, Platform: "multi-platform",
	})
	overwriteReleaseManifest(t, baseOutput, &baseManifest)

	apiV2 := writeTestRegistryImage(t, serverURL.Host+"/source/api:build-2", "api-v2")
	writeSelectiveBundle("build-2", "does-not-exist")
	bundleOptions.releaseID = "release-2"
	bundlePublishOptions.baseRelease = baseOutput
	bundlePublishOptions.selectors = []string{"api/application"}
	bundlePublishOptions.preserveBaseOnly = true
	bundlePublishOptions.progress = true
	selectiveOutput := filepath.Join(tempDir, "release-2.yml")
	bundlePublishOptions.output = selectiveOutput
	var selectiveProgress bytes.Buffer
	cmd.SetErr(&selectiveProgress)
	if err := runBundlePublish(cmd, []string{bundlePath}); err != nil {
		t.Fatalf("selective publish: %v", err)
	}
	for _, want := range []string{"preflight target 3/3: platform/sidecar", "3/3", "100%"} {
		if !strings.Contains(selectiveProgress.String(), want) {
			t.Fatalf("preflight progress does not contain %q: %q", want, selectiveProgress.String())
		}
	}

	manifest := readReleaseManifest(t, selectiveOutput)
	if manifest.ReleaseID != "release-2" || len(manifest.Images) != 3 || manifest.Platform != "" {
		t.Fatalf("manifest = %#v", manifest)
	}
	bySelector := make(map[string]bundle.ReleaseImage, len(manifest.Images))
	for _, item := range manifest.Images {
		bySelector[item.AppName+"/"+item.ContainerName] = item
		if item.Tag != "release-2" {
			t.Errorf("%s/%s tag = %q", item.AppName, item.ContainerName, item.Tag)
		}
	}
	if got := bySelector["api/application"]; got.Digest != apiV2 || got.Digest == apiV1 || got.Source == nil || got.Source.Image != serverURL.Host+"/source/api" {
		t.Fatalf("selected api = %#v", got)
	}
	if got := bySelector["worker/application"]; got.Digest != workerV1 || got.Source == nil || got.Source.Image != serverURL.Host+"/release/worker" || got.Source.Digest != workerV1 {
		t.Fatalf("reused worker = %#v", got)
	}
	if got := bySelector["platform/sidecar"]; got.Digest != baseOnlyDigest || got.Source == nil || got.Source.Image != serverURL.Host+"/release/sidecar" || got.Source.Digest != baseOnlyDigest || got.Platform != "multi-platform" {
		t.Fatalf("preserved base-only sidecar = %#v", got)
	}
	for _, test := range []struct {
		ref    string
		digest string
	}{
		{serverURL.Host + "/release/api:release-2", apiV2},
		{serverURL.Host + "/release/worker:release-2", workerV1},
		{serverURL.Host + "/release/sidecar:release-2", baseOnlyDigest},
	} {
		ref, err := name.NewTag(test.ref, name.Insecure)
		if err != nil {
			t.Fatal(err)
		}
		desc, err := remote.Get(ref)
		if err != nil {
			t.Fatalf("get %s: %v", test.ref, err)
		}
		if desc.Digest.String() != test.digest {
			t.Errorf("%s digest = %s, want %s", test.ref, desc.Digest, test.digest)
		}
	}
}

func TestSameImageRepository(t *testing.T) {
	if !sameImageRepository("docker://registry.example.com/team/api", "registry.example.com/team/api/") {
		t.Fatal("equivalent repositories were not recognized")
	}
	if sameImageRepository("registry.example.com/team/api", "registry.example.com/team/worker") {
		t.Fatal("different repositories were recognized as equal")
	}
}

func writeTestRegistryIndex(t *testing.T, refValue string) string {
	t.Helper()
	images := make([]v1.Image, 0, 2)
	for _, architecture := range []string{"amd64", "arm64"} {
		image := testutil.NewImageWithFiles(t, map[string][]byte{"architecture.txt": []byte(architecture)})
		config, err := image.ConfigFile()
		if err != nil {
			t.Fatal(err)
		}
		config.OS, config.Architecture = "linux", architecture
		image, err = mutate.ConfigFile(image, config)
		if err != nil {
			t.Fatal(err)
		}
		images = append(images, image)
	}
	index := mutate.AppendManifests(
		empty.Index,
		mutate.IndexAddendum{Add: images[0], Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "amd64"}}},
		mutate.IndexAddendum{Add: images[1], Descriptor: v1.Descriptor{Platform: &v1.Platform{OS: "linux", Architecture: "arm64"}}},
	)
	ref, err := name.NewTag(refValue, name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.WriteIndex(ref, index); err != nil {
		t.Fatalf("write %s: %v", refValue, err)
	}
	digest, err := index.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return digest.String()
}

func overwriteReleaseManifest(t *testing.T, path string, manifest *bundle.ReleaseManifest) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := bundle.EncodeRelease(f, manifest); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeTestRegistryImage(t *testing.T, refValue, content string) string {
	t.Helper()
	ref, err := name.NewTag(refValue, name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	image := testutil.NewImageWithFiles(t, map[string][]byte{"app.txt": []byte(content)})
	config, err := image.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	config.OS = "linux"
	config.Architecture = "amd64"
	image, err = mutate.ConfigFile(image, config)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(ref, image); err != nil {
		t.Fatalf("write %s: %v", refValue, err)
	}
	digest, err := image.Digest()
	if err != nil {
		t.Fatal(err)
	}
	return digest.String()
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
