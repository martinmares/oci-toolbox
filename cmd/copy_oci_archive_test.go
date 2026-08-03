package cmd

import (
	"context"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/registry"
	"github.com/google/go-containerregistry/pkg/v1/random"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/martinmares/oci-toolbox/internal/ociarchive"
)

func TestCopyMultiPlatformIndexThroughOCIArchive(t *testing.T) {
	server := httptest.NewServer(registry.New())
	defer server.Close()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	sourceRef, err := name.NewTag(serverURL.Host+"/source/app:latest", name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	index, err := random.Index(128, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	want, err := index.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if err := remote.WriteIndex(sourceRef, index); err != nil {
		t.Fatalf("write source index: %v", err)
	}

	archive := filepath.Join(t.TempDir(), "app.oci.tgz")
	if err := runCopyOne(context.Background(), "docker://"+sourceRef.String(), "oci-archive://"+archive, "", nil, copyRunOptions{}); err != nil {
		t.Fatalf("registry to OCI archive: %v", err)
	}
	content, err := ociarchive.Open(archive)
	if err != nil {
		t.Fatalf("open OCI archive: %v", err)
	}
	got, err := content.Index.Digest()
	if closeErr := content.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("archive index digest = %s, want %s", got, want)
	}

	destinationRef, err := name.NewTag(serverURL.Host+"/restored/app:latest", name.Insecure)
	if err != nil {
		t.Fatal(err)
	}
	if err := runCopyOne(context.Background(), "oci-archive://"+archive, "docker://"+destinationRef.String(), "", nil, copyRunOptions{}); err != nil {
		t.Fatalf("OCI archive to registry: %v", err)
	}
	restored, err := remote.Get(destinationRef)
	if err != nil {
		t.Fatalf("get restored index: %v", err)
	}
	if restored.Digest != want {
		t.Fatalf("restored index digest = %s, want %s", restored.Digest, want)
	}
}
