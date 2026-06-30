package cmd

import (
	"path/filepath"
	"testing"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
)

func TestLoadFileImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.tgz")
	tag, err := name.NewTag("registry.example.com/team/app:backup")
	if err != nil {
		t.Fatal(err)
	}
	if err := tarball.WriteToFile(path, tag, empty.Image); err != nil {
		t.Fatalf("WriteToFile: %v", err)
	}

	img, err := loadFileImage("file://" + path)
	if err != nil {
		t.Fatalf("loadFileImage: %v", err)
	}
	got, err := img.Digest()
	if err != nil {
		t.Fatalf("Digest: %v", err)
	}
	want, err := empty.Image.Digest()
	if err != nil {
		t.Fatalf("empty.Image.Digest: %v", err)
	}
	if got != want {
		t.Fatalf("digest = %s, want %s", got, want)
	}
}

func TestLoadFileImageRejectsEmptyPath(t *testing.T) {
	if _, err := loadFileImage("file://"); err == nil {
		t.Fatal("loadFileImage(file://) succeeded, want error")
	}
}

func TestFileDestinationDoesNotEagerPull(t *testing.T) {
	if shouldEagerPull(true, true, false) {
		t.Fatal("file destination with progress would eagerly pull layers")
	}
	if shouldEagerPull(true, false, true) {
		t.Fatal("file destination with JSON progress would eagerly pull layers")
	}
	if shouldEagerPull(true, false, false) {
		t.Fatal("file destination without progress would eagerly pull layers")
	}
	if !shouldEagerPull(false, true, false) {
		t.Fatal("registry destination no longer eagerly pulls for pull progress")
	}
}
