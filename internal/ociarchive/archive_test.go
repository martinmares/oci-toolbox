package ociarchive

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/random"
)

func TestImageRoundTrip(t *testing.T) {
	image, err := random.Image(128, 2)
	if err != nil {
		t.Fatal(err)
	}
	want, err := image.Digest()
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "image.oci.tgz")
	if err := WriteImage(archive, image); err != nil {
		t.Fatalf("WriteImage: %v", err)
	}
	content, err := Open(archive)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer content.Close()
	if content.Image == nil || content.Index != nil {
		t.Fatalf("content = %#v, want image", content)
	}
	got, err := content.Image.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("digest = %s, want %s", got, want)
	}
}

func TestIndexRoundTrip(t *testing.T) {
	index, err := random.Index(128, 2, 2)
	if err != nil {
		t.Fatal(err)
	}
	want, err := index.Digest()
	if err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(t.TempDir(), "index.oci.tgz")
	if err := WriteIndex(archive, index); err != nil {
		t.Fatalf("WriteIndex: %v", err)
	}
	content, err := Open(archive)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer content.Close()
	if content.Index == nil || content.Image != nil {
		t.Fatalf("content = %#v, want image index", content)
	}
	got, err := content.Index.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("digest = %s, want %s", got, want)
	}
}

func TestOpenRejectsPathTraversal(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "unsafe.oci.tgz")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gzw := gzip.NewWriter(file)
	tw := tar.NewWriter(gzw)
	payload := []byte("unsafe")
	if err := tw.WriteHeader(&tar.Header{Name: "../outside", Mode: 0o644, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(archive); err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("Open error = %v, want unsafe path", err)
	}
}

func TestOpenRejectsWindowsPathTraversal(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "unsafe-windows.oci.tgz")
	file, err := os.Create(archive)
	if err != nil {
		t.Fatal(err)
	}
	gzw := gzip.NewWriter(file)
	tw := tar.NewWriter(gzw)
	payload := []byte("unsafe")
	if err := tw.WriteHeader(&tar.Header{Name: `..\outside`, Mode: 0o644, Size: int64(len(payload))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(archive); err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Fatalf("Open error = %v, want unsafe path", err)
	}
}
