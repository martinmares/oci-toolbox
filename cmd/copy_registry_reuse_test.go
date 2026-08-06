package cmd

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/martinmares/oci-toolbox/internal/testutil"
)

type blobGetCounter struct {
	next http.Handler
	gets atomic.Int64
}

func (h *blobGetCounter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/blobs/") {
		h.gets.Add(1)
	}
	h.next.ServeHTTP(w, r)
}

func TestRegistryCopyReusesDestinationDigestWithoutPullingLayers(t *testing.T) {
	sourceHandler := &blobGetCounter{next: registry.New()}
	destinationHandler := &blobGetCounter{next: registry.New()}
	sourceServer := httptest.NewServer(sourceHandler)
	destinationServer := httptest.NewServer(destinationHandler)
	defer sourceServer.Close()
	defer destinationServer.Close()

	sourceURL, err := url.Parse(sourceServer.URL)
	if err != nil {
		t.Fatal(err)
	}
	destinationURL, err := url.Parse(destinationServer.URL)
	if err != nil {
		t.Fatal(err)
	}

	img := testutil.NewImageWithFiles(t, map[string][]byte{"app.bin": []byte("same-content")})
	cfg, err := img.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	cfg.OS, cfg.Architecture = "linux", "amd64"
	img, err = mutate.ConfigFile(img, cfg)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := img.Digest()
	if err != nil {
		t.Fatal(err)
	}

	sourceRef, err := name.NewTag(sourceURL.Host+"/team/app:build", name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	existingDestinationRef, err := name.NewTag(destinationURL.Host+"/archive/app:existing", name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(sourceRef, img); err != nil {
		t.Fatal(err)
	}
	if err := remote.Write(existingDestinationRef, img); err != nil {
		t.Fatal(err)
	}
	sourceHandler.gets.Store(0)
	destinationHandler.gets.Store(0)

	destination := destinationURL.Host + "/archive/app:2.4"
	if err := runCopyOne(t.Context(), sourceURL.Host+"/team/app:build", destination, "copy 1/1", []string{"stable"}, copyRunOptions{
		SrcInsecure: true, DestInsecure: true, Progress: true,
	}); err != nil {
		t.Fatal(err)
	}
	if got := sourceHandler.gets.Load(); got != 0 {
		t.Fatalf("source layer GET requests = %d, want 0", got)
	}
	if got := destinationHandler.gets.Load(); got != 0 {
		t.Fatalf("destination layer GET requests = %d, want 0", got)
	}
	for _, refValue := range []string{destination, destinationURL.Host + "/archive/app:stable"} {
		ref, err := name.NewTag(refValue, name.Insecure)
		if err != nil {
			t.Fatal(err)
		}
		desc, err := remote.Get(ref)
		if err != nil {
			t.Fatal(err)
		}
		if desc.Digest.String() != digest.String() {
			t.Fatalf("%s digest = %s, want %s", ref, desc.Digest, digest)
		}
	}
}

func TestRegistryDigestReuseIsDisabledForTransformations(t *testing.T) {
	if !shouldTryRegistryDigestReuse(false, false, false, false, copyRunOptions{}) {
		t.Fatal("plain registry copy should attempt digest reuse")
	}
	for name, opts := range map[string]copyRunOptions{
		"recipe":         {RecipePaths: []string{"recipe.yml"}},
		"reduce":         {Reduce: true},
		"reduce changes": {ReduceChanges: true},
		"wrap":           {WrapEntrypoint: true},
		"no wrap":        {NoWrapEntrypoint: true},
		"no reuse":       {DestNoReuse: true},
	} {
		if shouldTryRegistryDigestReuse(false, false, false, false, opts) {
			t.Fatalf("%s unexpectedly allows digest reuse", name)
		}
	}
}
