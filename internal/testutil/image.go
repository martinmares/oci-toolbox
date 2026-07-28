package testutil

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"os"
	"testing"
	"time"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

// NewImage returns a minimal empty OCI image.
func NewImage(t *testing.T) v1.Image {
	t.Helper()
	return empty.Image
}

// NewImageWithFiles returns a test image containing the given files (path → content).
func NewImageWithFiles(t *testing.T, files map[string][]byte) v1.Image {
	t.Helper()

	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)

	for p, data := range files {
		hdr := &tar.Header{
			Name:     p,
			Size:     int64(len(data)),
			Mode:     0o644,
			ModTime:  time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC),
			Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}

	layer, err := tarball.LayerFromReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("create layer: %v", err)
	}

	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		t.Fatalf("append layer: %v", err)
	}

	return img
}

// WriteLocalFile writes content to a temp file and returns its path.
func WriteLocalFile(t *testing.T, content []byte) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "oci-toolbox-*")
	if err != nil {
		t.Fatalf("temp file: %v", err)
	}
	if _, err := f.Write(content); err != nil {
		t.Fatalf("write temp: %v", err)
	}
	f.Close()
	return f.Name()
}

// WriteZip creates a zip archive in a temp dir with the given entries and returns its path.
func WriteZip(t *testing.T, entries map[string][]byte) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "oci-toolbox-*.zip")
	if err != nil {
		t.Fatalf("temp zip: %v", err)
	}

	zw := zip.NewWriter(f)
	for name, data := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %q: %v", name, err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("zip write %q: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	f.Close()
	return f.Name()
}
