package snapshot

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

type fakeLister struct{ images []string }

func (f fakeLister) ListImages(context.Context, string) ([]string, error) { return f.images, nil }

type copyCall struct {
	source      string
	destination string
	options     CopyOptions
}

type fakeCopier struct {
	calls             []copyCall
	createDestination bool
	err               error
}

func (f *fakeCopier) Copy(_ context.Context, source, destination string, options CopyOptions) error {
	f.calls = append(f.calls, copyCall{source: source, destination: destination, options: options})
	if f.err != nil {
		return f.err
	}
	if f.createDestination && strings.HasPrefix(destination, "oci-archive://") && strings.HasSuffix(destination, ".part") {
		return os.WriteFile(strings.TrimPrefix(destination, "oci-archive://"), []byte("archive"), 0o644)
	}
	return nil
}

func TestBackupWritesCompleteManifestAndCopiesImages(t *testing.T) {
	copier := &fakeCopier{createDestination: true}
	now := time.Date(2026, 6, 30, 19, 30, 0, 0, time.FixedZone("CEST", 2*60*60))
	manifestPath, err := Backup(context.Background(), fakeLister{images: []string{
		"registry.example.com/team/app:1",
		"registry.example.com/team/app:1",
		"registry.example.com/other/worker:2",
	}}, copier, BackupOptions{
		Namespace: "example", OutputDir: t.TempDir(), SrcInsecure: true, SrcProxy: "http://proxy.example.com:8080",
		Now: func() time.Time { return now }, Stdout: io.Discard, Stderr: io.Discard,
	})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if len(copier.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(copier.calls))
	}
	if !copier.calls[0].options.SrcInsecure || copier.calls[0].options.SrcProxy != "http://proxy.example.com:8080" || !copier.calls[0].options.Progress {
		t.Fatalf("copy options = %#v", copier.calls[0].options)
	}
	if !strings.HasPrefix(copier.calls[0].destination, "oci-archive://") {
		t.Fatalf("destination = %q, want OCI archive", copier.calls[0].destination)
	}

	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := yaml.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != ManifestVersion || !manifest.Complete || len(manifest.Images) != 2 {
		t.Fatalf("manifest = %#v", manifest)
	}
	if !strings.HasSuffix(manifest.Images[0].Archive, ".oci.tgz") {
		t.Fatalf("archive = %q, want .oci.tgz suffix", manifest.Images[0].Archive)
	}
	if manifest.Images[0].SHA256 == "" || manifest.Images[0].Size != int64(len("archive")) {
		t.Fatalf("archive metadata = %#v", manifest.Images[0])
	}
}

func TestBackupFiltersImagesWithSlashCrossingGlob(t *testing.T) {
	copier := &fakeCopier{createDestination: true}
	manifestPath, err := Backup(context.Background(), fakeLister{images: []string{
		"registry.example.com/team/stage/docs-admin:latest",
		"registry.example.com/team/stage/docs-security:latest",
		"registry.example.com/team/release/ticket:release",
	}}, copier, BackupOptions{
		Namespace: "example", OutputDir: t.TempDir(), ImagePattern: "*/stage/docs-*:*",
		Now:    func() time.Time { return time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC) },
		Stdout: io.Discard, Stderr: io.Discard,
	})
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if len(copier.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(copier.calls))
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest Manifest
	if err := yaml.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Filter != "*/stage/docs-*:*" || len(manifest.Images) != 2 {
		t.Fatalf("manifest = %#v", manifest)
	}
}

func TestBackupReturnsErrorWhenFilterMatchesNothing(t *testing.T) {
	_, err := Backup(context.Background(), fakeLister{images: []string{"registry/team/app:1"}}, &fakeCopier{}, BackupOptions{
		Namespace: "example", OutputDir: t.TempDir(), ImagePattern: "*/missing/*",
		Stdout: io.Discard, Stderr: io.Discard,
	})
	if err == nil || !strings.Contains(err.Error(), "match") {
		t.Fatalf("error = %v", err)
	}
}

func TestRestorableDestinationUsesSyntheticTagForDigestReference(t *testing.T) {
	source := "registry.example.com/team/app@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	got := restorableDestination(source)
	want := "registry.example.com/team/app:snapshot-0123456789ab"
	if got != want {
		t.Fatalf("restorableDestination = %q, want %q", got, want)
	}
}

func TestRestoreVerifiesArchiveAndCopiesImages(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "20260630-193000")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "app.tgz")
	if err := os.WriteFile(archive, []byte("archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest, size, err := fileMetadata(archive)
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{Version: 1, Namespace: "example", Complete: true, Images: []ManifestImage{{
		Archive: "20260630-193000/app.tgz", Destination: "registry.example.com/team/app:1",
		SHA256: digest, Size: size,
	}}}
	manifestPath := filepath.Join(root, "20260630-193000.yml")
	if err := writeManifest(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	copier := &fakeCopier{}
	var output bytes.Buffer
	if err := Restore(context.Background(), copier, RestoreOptions{
		ManifestPath: manifestPath, Namespace: "example", DestInsecure: true, DestProxy: "direct",
		Stdout: &output, Stderr: io.Discard,
	}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(copier.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(copier.calls))
	}
	if !copier.calls[0].options.DestInsecure || copier.calls[0].options.DestProxy != "direct" || !copier.calls[0].options.Progress {
		t.Fatalf("copy options = %#v", copier.calls[0].options)
	}
	if !strings.HasPrefix(copier.calls[0].source, "file://") {
		t.Fatalf("source = %q, want legacy Docker archive", copier.calls[0].source)
	}
}

func TestRestoreVersionTwoUsesOCIArchive(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "20260630-193000")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "app.oci.tgz")
	if err := os.WriteFile(archive, []byte("archive"), 0o644); err != nil {
		t.Fatal(err)
	}
	digest, size, err := fileMetadata(archive)
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{Version: ManifestVersion, Namespace: "example", Complete: true, Images: []ManifestImage{{
		Archive: "20260630-193000/app.oci.tgz", Destination: "registry.example.com/team/app:1",
		SHA256: digest, Size: size,
	}}}
	manifestPath := filepath.Join(root, "20260630-193000.yml")
	if err := writeManifest(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	copier := &fakeCopier{}
	if err := Restore(context.Background(), copier, RestoreOptions{
		ManifestPath: manifestPath, Namespace: "example", Stdout: io.Discard, Stderr: io.Discard,
	}); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if len(copier.calls) != 1 || !strings.HasPrefix(copier.calls[0].source, "oci-archive://") {
		t.Fatalf("calls = %#v, want OCI archive source", copier.calls)
	}
}

func TestRestoreRejectsIncompleteManifest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.yml")
	if err := writeManifest(path, Manifest{Version: 1, Namespace: "example", Complete: false}); err != nil {
		t.Fatal(err)
	}
	err := Restore(context.Background(), &fakeCopier{}, RestoreOptions{ManifestPath: path, Namespace: "example"})
	if err == nil || !strings.Contains(err.Error(), "incomplete") {
		t.Fatalf("error = %v", err)
	}
}

func TestRestoreRejectsUnknownManifestFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.yml")
	if err := os.WriteFile(path, []byte(`
version: 1
namespace: example
complete: true
images: []
unexpected: true
`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := Restore(context.Background(), &fakeCopier{}, RestoreOptions{ManifestPath: path, Namespace: "example"})
	if err == nil || !strings.Contains(err.Error(), "field unexpected not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestRestoreRejectsDifferentNamespace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.yml")
	if err := writeManifest(path, Manifest{Version: 1, Namespace: "example", Complete: true}); err != nil {
		t.Fatal(err)
	}
	err := Restore(context.Background(), &fakeCopier{}, RestoreOptions{ManifestPath: path, Namespace: "other"})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("error = %v", err)
	}
}

func TestBackupFailureLeavesIncompleteManifest(t *testing.T) {
	copier := &fakeCopier{err: context.Canceled}
	root := t.TempDir()
	manifestPath, err := Backup(context.Background(), fakeLister{images: []string{"registry/app:1"}}, copier, BackupOptions{
		Namespace: "example", OutputDir: root,
		Now:    func() time.Time { return time.Date(2026, 6, 30, 19, 30, 0, 0, time.UTC) },
		Stdout: io.Discard, Stderr: io.Discard,
	})
	if err == nil {
		t.Fatal("Backup succeeded, want error")
	}
	raw, readErr := os.ReadFile(manifestPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	var manifest Manifest
	if unmarshalErr := yaml.Unmarshal(raw, &manifest); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	if manifest.Complete {
		t.Fatal("failed backup manifest is complete")
	}
}

func TestSafeArchivePathRejectsTraversal(t *testing.T) {
	if _, err := safeArchivePath(t.TempDir(), "../secret"); err == nil {
		t.Fatal("expected traversal error")
	}
}
