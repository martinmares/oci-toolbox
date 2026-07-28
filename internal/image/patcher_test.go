package image_test

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"

	"github.com/martinmares/oci-toolbox/internal/image"
	"github.com/martinmares/oci-toolbox/internal/testutil"
)

func TestAddFileLayer_basic(t *testing.T) {
	img := testutil.NewImage(t)
	content := []byte("hello from oci-toolbox")
	src := testutil.WriteLocalFile(t, content)

	img2, err := image.AddFileLayer(img, src, "/usr/local/bin/tool", "0755", "")
	if err != nil {
		t.Fatalf("AddFileLayer: %v", err)
	}

	tmp := t.TempDir() + "/tool"
	if err := image.ExtractFileToLocal(img2, "/usr/local/bin/tool", tmp); err != nil {
		t.Fatalf("ExtractFileToLocal: %v", err)
	}

	got, _ := os.ReadFile(tmp)
	if string(got) != string(content) {
		t.Errorf("content mismatch: got %q, want %q", got, content)
	}
}

func TestReadFileRejectsDirectory(t *testing.T) {
	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)
	if err := tw.WriteHeader(&tar.Header{Name: "app/", Mode: 0o755, Typeflag: tar.TypeDir}); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	layer, err := tarball.LayerFromReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("LayerFromReader: %v", err)
	}
	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		t.Fatalf("AppendLayers: %v", err)
	}

	_, err = image.ReadFile(img, "/app")
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("ReadFile error = %v, want regular-file error", err)
	}
}

func TestAddFileLayer_globToDirectory(t *testing.T) {
	img := testutil.NewImage(t)
	dir := t.TempDir()
	src := filepath.Join(dir, "zis_stream_entities_account_ref-0.1.0-SNAPSHOT.jar")
	content := []byte("jar content")
	if err := os.WriteFile(src, content, 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	img2, err := image.AddFileLayer(img, filepath.Join(dir, "*SNAPSHOT.jar"), "/app/", "0644", "")
	if err != nil {
		t.Fatalf("AddFileLayer: %v", err)
	}

	tmp := filepath.Join(t.TempDir(), "app.jar")
	if err := image.ExtractFileToLocal(img2, "/app/zis_stream_entities_account_ref-0.1.0-SNAPSHOT.jar", tmp); err != nil {
		t.Fatalf("ExtractFileToLocal: %v", err)
	}

	got, _ := os.ReadFile(tmp)
	if string(got) != string(content) {
		t.Errorf("content mismatch: got %q, want %q", got, content)
	}
}

func TestAddFileLayer_globNoMatches(t *testing.T) {
	img := testutil.NewImage(t)
	_, err := image.AddFileLayer(img, filepath.Join(t.TempDir(), "*SNAPSHOT.jar"), "/app/", "0644", "")
	if err == nil || !strings.Contains(err.Error(), "matched no files") {
		t.Fatalf("AddFileLayer error = %v, want no-match error", err)
	}
}

func TestAddFileLayer_globMultipleMatches(t *testing.T) {
	img := testutil.NewImage(t)
	dir := t.TempDir()
	for _, name := range []string{"one-SNAPSHOT.jar", "two-SNAPSHOT.jar"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
	}

	_, err := image.AddFileLayer(img, filepath.Join(dir, "*SNAPSHOT.jar"), "/app/", "0644", "")
	if err == nil || !strings.Contains(err.Error(), "matched multiple files") {
		t.Fatalf("AddFileLayer error = %v, want multiple-match error", err)
	}
}

func TestAddFileLayer_overwrites(t *testing.T) {
	original := []byte("original content")
	updated := []byte("updated content")

	img := testutil.NewImageWithFiles(t, map[string][]byte{
		"app/config.txt": original,
	})

	src := testutil.WriteLocalFile(t, updated)
	img2, err := image.AddFileLayer(img, src, "/app/config.txt", "0644", "")
	if err != nil {
		t.Fatalf("AddFileLayer: %v", err)
	}

	tmp := t.TempDir() + "/config.txt"
	if err := image.ExtractFileToLocal(img2, "/app/config.txt", tmp); err != nil {
		t.Fatalf("ExtractFileToLocal: %v", err)
	}

	got, _ := os.ReadFile(tmp)
	if string(got) != string(updated) {
		t.Errorf("expected updated content %q, got %q", updated, got)
	}
}

// condition: if-not-exists → soubor existuje → přeskočit
func TestAddFileLayerConditional_skipsWhenExists(t *testing.T) {
	existing := []byte("do not touch")
	img := testutil.NewImageWithFiles(t, map[string][]byte{
		"usr/local/bin/tool": existing,
	})

	src := testutil.WriteLocalFile(t, []byte("replacement"))
	img2, err := image.AddFileLayerConditional(img, src, "/usr/local/bin/tool", "0755", "", "/usr/local/bin/tool")
	if err != nil {
		t.Fatalf("AddFileLayerConditional: %v", err)
	}

	tmp := t.TempDir() + "/tool"
	if err := image.ExtractFileToLocal(img2, "/usr/local/bin/tool", tmp); err != nil {
		t.Fatalf("ExtractFileToLocal: %v", err)
	}

	got, _ := os.ReadFile(tmp)
	if string(got) != string(existing) {
		t.Errorf("file should NOT be replaced: got %q, want %q", got, existing)
	}
}

// condition: if-not-exists → soubor chybí → přidat
func TestAddFileLayerConditional_addsWhenMissing(t *testing.T) {
	img := testutil.NewImage(t)
	content := []byte("new tool")
	src := testutil.WriteLocalFile(t, content)

	img2, err := image.AddFileLayerConditional(img, src, "/usr/local/bin/tool", "0755", "", "/usr/local/bin/tool")
	if err != nil {
		t.Fatalf("AddFileLayerConditional: %v", err)
	}

	tmp := t.TempDir() + "/tool"
	if err := image.ExtractFileToLocal(img2, "/usr/local/bin/tool", tmp); err != nil {
		t.Fatalf("ExtractFileToLocal: %v", err)
	}

	got, _ := os.ReadFile(tmp)
	if string(got) != string(content) {
		t.Errorf("expected file to be added: got %q, want %q", got, content)
	}
}

// bez condition → vždy přepsat
func TestAddFileLayerConditional_noCondition_alwaysOverwrites(t *testing.T) {
	existing := []byte("original")
	img := testutil.NewImageWithFiles(t, map[string][]byte{
		"app/data.txt": existing,
	})

	newContent := []byte("overwritten")
	src := testutil.WriteLocalFile(t, newContent)

	img2, err := image.AddFileLayerConditional(img, src, "/app/data.txt", "0644", "", "")
	if err != nil {
		t.Fatalf("AddFileLayerConditional: %v", err)
	}

	tmp := t.TempDir() + "/data.txt"
	if err := image.ExtractFileToLocal(img2, "/app/data.txt", tmp); err != nil {
		t.Fatalf("ExtractFileToLocal: %v", err)
	}

	got, _ := os.ReadFile(tmp)
	if string(got) != string(newContent) {
		t.Errorf("expected overwrite: got %q, want %q", got, newContent)
	}
}

func TestRemoveFile_whiteout(t *testing.T) {
	img := testutil.NewImageWithFiles(t, map[string][]byte{
		"tmp/deleteme.txt": []byte("bye"),
	})

	img2, err := image.RemoveFile(img, "/tmp/deleteme.txt")
	if err != nil {
		t.Fatalf("RemoveFile: %v", err)
	}

	err = image.ExtractFileToLocal(img2, "/tmp/deleteme.txt", t.TempDir()+"/out")
	if err == nil {
		t.Error("expected error extracting whiteout-removed file, got nil")
	}
}

func TestAddFileLayerConditionalInDirs_skipsWhenFound(t *testing.T) {
	// binary exists in /bin — searching [/bin, /usr/bin] should skip the add
	existing := []byte("original in /bin")
	img := testutil.NewImageWithFiles(t, map[string][]byte{
		"bin/encjson": existing,
	})

	src := testutil.WriteLocalFile(t, []byte("should NOT appear"))
	img2, err := image.AddFileLayerConditionalInDirs(img, src, "/usr/local/bin/encjson", "0755", "", []string{"/bin", "/usr/bin", "/usr/local/bin"})
	if err != nil {
		t.Fatalf("AddFileLayerConditionalInDirs: %v", err)
	}

	// original must still be reachable
	tmp := t.TempDir() + "/encjson"
	if err := image.ExtractFileToLocal(img2, "/bin/encjson", tmp); err != nil {
		t.Fatalf("original should still exist: %v", err)
	}
	got, _ := os.ReadFile(tmp)
	if string(got) != string(existing) {
		t.Errorf("original changed: got %q", got)
	}

	// new path must NOT have been added
	err = image.ExtractFileToLocal(img2, "/usr/local/bin/encjson", t.TempDir()+"/new")
	if err == nil {
		t.Error("new path should NOT exist — file was already found in /bin")
	}
}

func TestAddFileLayerConditionalInDirs_addsWhenMissing(t *testing.T) {
	// binary is not in any of the searched dirs
	img := testutil.NewImageWithFiles(t, map[string][]byte{
		"opt/vendor/encjson": []byte("somewhere else"),
	})

	content := []byte("new encjson")
	src := testutil.WriteLocalFile(t, content)
	img2, err := image.AddFileLayerConditionalInDirs(img, src, "/usr/local/bin/encjson", "0755", "", []string{"/bin", "/usr/bin", "/usr/local/bin"})
	if err != nil {
		t.Fatalf("AddFileLayerConditionalInDirs: %v", err)
	}

	tmp := t.TempDir() + "/encjson"
	if err := image.ExtractFileToLocal(img2, "/usr/local/bin/encjson", tmp); err != nil {
		t.Fatalf("file should have been added: %v", err)
	}
	got, _ := os.ReadFile(tmp)
	if string(got) != string(content) {
		t.Errorf("got %q, want %q", got, content)
	}
}

func TestReplaceFile_removesAllAndAdds(t *testing.T) {
	// binary exists in two locations — both should be wiped, new one added
	img := testutil.NewImageWithFiles(t, map[string][]byte{
		"bin/encjson":         []byte("old in /bin"),
		"usr/bin/encjson":     []byte("old in /usr/bin"),
		"usr/local/bin/other": []byte("unrelated file"),
	})

	content := []byte("brand new encjson")
	src := testutil.WriteLocalFile(t, content)

	img2, err := image.ReplaceFile(img, src, "/usr/local/bin/encjson", "0755", "", "encjson")
	if err != nil {
		t.Fatalf("ReplaceFile: %v", err)
	}

	// old paths must be gone
	for _, p := range []string{"/bin/encjson", "/usr/bin/encjson"} {
		if err := image.ExtractFileToLocal(img2, p, t.TempDir()+"/out"); err == nil {
			t.Errorf("expected %s to be removed, but it still exists", p)
		}
	}

	// new path must exist with new content
	tmp := t.TempDir() + "/encjson"
	if err := image.ExtractFileToLocal(img2, "/usr/local/bin/encjson", tmp); err != nil {
		t.Fatalf("new file not found: %v", err)
	}
	got, _ := os.ReadFile(tmp)
	if string(got) != string(content) {
		t.Errorf("got %q, want %q", got, content)
	}

	// unrelated file must be untouched
	tmp2 := t.TempDir() + "/other"
	if err := image.ExtractFileToLocal(img2, "/usr/local/bin/other", tmp2); err != nil {
		t.Errorf("unrelated file should still exist: %v", err)
	}
}

func TestReplaceFile_nothingToRemove_justAdds(t *testing.T) {
	// find-all finds nothing — should still add the file
	img := testutil.NewImage(t)
	content := []byte("new tool")
	src := testutil.WriteLocalFile(t, content)

	img2, err := image.ReplaceFile(img, src, "/usr/local/bin/encjson", "0755", "", "encjson")
	if err != nil {
		t.Fatalf("ReplaceFile: %v", err)
	}

	tmp := t.TempDir() + "/encjson"
	if err := image.ExtractFileToLocal(img2, "/usr/local/bin/encjson", tmp); err != nil {
		t.Fatalf("file should exist: %v", err)
	}
	got, _ := os.ReadFile(tmp)
	if string(got) != string(content) {
		t.Errorf("got %q, want %q", got, content)
	}
}

func TestSquash_preservesSymlink(t *testing.T) {
	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)
	if err := tw.WriteHeader(&tar.Header{
		Name:     "usr/lib/libreadline.so.8.3",
		Mode:     0o755,
		Size:     int64(len([]byte("payload"))),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("payload")); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{
		Name:     "usr/lib/libreadline.so.8",
		Mode:     0o777,
		Typeflag: tar.TypeSymlink,
		Linkname: "libreadline.so.8.3",
	}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	layer, err := tarball.LayerFromReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		t.Fatal(err)
	}

	squashed, err := image.Squash(img)
	if err != nil {
		t.Fatalf("Squash: %v", err)
	}

	entries, err := image.CollectTree(squashed, "/usr/lib")
	if err != nil {
		t.Fatalf("CollectTree: %v", err)
	}
	var link *image.TreeEntry
	for i := range entries {
		if entries[i].Path == "/usr/lib/libreadline.so.8" {
			link = &entries[i]
			break
		}
	}
	if link == nil {
		t.Fatal("symlink missing after squash")
	}
	if link.Typeflag != tar.TypeSymlink {
		t.Fatalf("typeflag = %v, want symlink", link.Typeflag)
	}
	if link.Linkname != "libreadline.so.8.3" {
		t.Fatalf("linkname = %q, want libreadline.so.8.3", link.Linkname)
	}
}

func TestSquash_ordersHardlinkAfterTarget(t *testing.T) {
	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)
	if err := tw.WriteHeader(&tar.Header{
		Name:     "usr/share/zoneinfo/America/Chicago",
		Mode:     0o644,
		Size:     int64(len([]byte("tzdata"))),
		Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("tzdata")); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{
		Name:     "usr/share/zoneinfo/CST6CDT",
		Mode:     0o644,
		Typeflag: tar.TypeLink,
		Linkname: "usr/share/zoneinfo/America/Chicago",
	}); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}

	layer, err := tarball.LayerFromReader(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	img, err := mutate.AppendLayers(empty.Image, layer)
	if err != nil {
		t.Fatal(err)
	}

	squashed, err := image.Squash(img)
	if err != nil {
		t.Fatalf("Squash: %v", err)
	}

	layers, err := squashed.Layers()
	if err != nil {
		t.Fatalf("Layers: %v", err)
	}
	rc, err := layers[0].Uncompressed()
	if err != nil {
		t.Fatalf("Uncompressed: %v", err)
	}
	defer rc.Close()

	tr := tar.NewReader(rc)
	positions := map[string]int{}
	i := 0
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		if hdr.Name == "usr/share/zoneinfo/America/Chicago" || hdr.Name == "usr/share/zoneinfo/CST6CDT" {
			positions[hdr.Name] = i
		}
		i++
	}
	if positions["usr/share/zoneinfo/America/Chicago"] >= positions["usr/share/zoneinfo/CST6CDT"] {
		t.Fatal("hardlink target should be written before hardlink entry")
	}
	entries, err := image.CollectTree(squashed, "/usr/share/zoneinfo")
	if err != nil {
		t.Fatalf("CollectTree: %v", err)
	}
	for _, entry := range entries {
		if entry.Path == "/usr/share/zoneinfo/CST6CDT" {
			if entry.Typeflag != tar.TypeLink {
				t.Fatalf("typeflag = %v, want hardlink", entry.Typeflag)
			}
			if entry.Linkname != "usr/share/zoneinfo/America/Chicago" {
				t.Fatalf("linkname = %q", entry.Linkname)
			}
		}
	}
}

func TestReduceToBase_preservesBaseLayersAndAddsDelta(t *testing.T) {
	base := testutil.NewImageWithFiles(t, map[string][]byte{
		"etc/os-release": []byte("ID=test\n"),
		"app/old.txt":    []byte("old"),
	})

	replaced, err := image.RemoveFile(base, "/app")
	if err != nil {
		t.Fatalf("RemoveFile: %v", err)
	}
	selfPath := testutil.WriteLocalFile(t, []byte("bootstrap"))
	replaced, err = image.AddFileLayer(replaced, selfPath, "/app/app.self", "0755", "1001:0")
	if err != nil {
		t.Fatalf("AddFileLayer: %v", err)
	}
	replaced, err = image.FixPermissions(replaced, "/app", "1001:0", "g=u", true)
	if err != nil {
		t.Fatalf("FixPermissions: %v", err)
	}

	reduced, err := image.ReduceToBase(base, replaced)
	if err != nil {
		t.Fatalf("ReduceToBase: %v", err)
	}

	baseLayers, err := base.Layers()
	if err != nil {
		t.Fatalf("base Layers: %v", err)
	}
	reducedLayers, err := reduced.Layers()
	if err != nil {
		t.Fatalf("reduced Layers: %v", err)
	}
	if len(reducedLayers) != len(baseLayers)+1 {
		t.Fatalf("layer count = %d, want %d", len(reducedLayers), len(baseLayers)+1)
	}

	if err := image.ExtractFileToLocal(reduced, "/etc/os-release", t.TempDir()+"/os-release"); err != nil {
		t.Fatalf("base file should still exist: %v", err)
	}
	if err := image.ExtractFileToLocal(reduced, "/app/old.txt", t.TempDir()+"/old"); err == nil {
		t.Fatal("/app/old.txt should have been removed")
	}
	got := t.TempDir() + "/app.self"
	if err := image.ExtractFileToLocal(reduced, "/app/app.self", got); err != nil {
		t.Fatalf("/app/app.self missing: %v", err)
	}
}

func TestReplaceFile_noFindAll_behavesLikeAddFile(t *testing.T) {
	// find-all="" → plain overwrite at dst, nothing else touched
	existing := []byte("old")
	img := testutil.NewImageWithFiles(t, map[string][]byte{
		"usr/local/bin/encjson": existing,
	})
	newContent := []byte("new")
	src := testutil.WriteLocalFile(t, newContent)

	img2, err := image.ReplaceFile(img, src, "/usr/local/bin/encjson", "0755", "", "")
	if err != nil {
		t.Fatalf("ReplaceFile: %v", err)
	}

	tmp := t.TempDir() + "/encjson"
	if err := image.ExtractFileToLocal(img2, "/usr/local/bin/encjson", tmp); err != nil {
		t.Fatalf("extract: %v", err)
	}
	got, _ := os.ReadFile(tmp)
	if string(got) != string(newContent) {
		t.Errorf("got %q, want %q", got, newContent)
	}
}

func TestCopyFileFrom(t *testing.T) {
	srcContent := []byte("binary from source image")
	srcImg := testutil.NewImageWithFiles(t, map[string][]byte{
		"app/binary": srcContent,
	})
	dstImg := testutil.NewImage(t)

	result, err := image.CopyFileFrom(dstImg, srcImg, "/app/binary", "/usr/bin/binary", "0755", "1001:1001")
	if err != nil {
		t.Fatalf("CopyFileFrom: %v", err)
	}

	tmp := t.TempDir() + "/binary"
	if err := image.ExtractFileToLocal(result, "/usr/bin/binary", tmp); err != nil {
		t.Fatalf("ExtractFileToLocal: %v", err)
	}

	got, _ := os.ReadFile(tmp)
	if string(got) != string(srcContent) {
		t.Errorf("content mismatch: got %q, want %q", got, srcContent)
	}
}

func TestExtractFileToLocal_notFound(t *testing.T) {
	img := testutil.NewImage(t)
	err := image.ExtractFileToLocal(img, "/nonexistent/path", t.TempDir()+"/out")
	if err == nil {
		t.Error("expected error for missing file, got nil")
	}
}

func TestReadFile(t *testing.T) {
	img := testutil.NewImageWithFiles(t, map[string][]byte{
		"app/resources/git.properties": []byte("git.commit.id=abc123\n"),
	})

	data, err := image.ReadFile(img, "/app/resources/git.properties")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "git.commit.id=abc123\n" {
		t.Fatalf("ReadFile = %q, want git.properties content", data)
	}
}

func TestReadFileNotFound(t *testing.T) {
	img := testutil.NewImage(t)

	_, err := image.ReadFile(img, "/app/resources/git.properties")
	if !errors.Is(err, image.ErrFileNotFound) {
		t.Fatalf("ReadFile error = %v, want ErrFileNotFound", err)
	}
}

func TestListEntriesFile(t *testing.T) {
	img := testutil.NewImageWithFiles(t, map[string][]byte{
		"app/resources/git.properties": []byte("git.commit.id=abc123\n"),
	})

	entries, err := image.ListEntries(img, "/app/resources/git.properties")
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries len = %d, want 1", len(entries))
	}
	if entries[0].Path != "/app/resources/git.properties" {
		t.Fatalf("entry path = %q, want file path", entries[0].Path)
	}
}

func TestListEntriesDir(t *testing.T) {
	img := testutil.NewImageWithFiles(t, map[string][]byte{
		"app/resources/git.properties": []byte("git.commit.id=abc123\n"),
		"app/other.txt":                []byte("hello"),
	})

	entries, err := image.ListEntries(img, "/app")
	if err != nil {
		t.Fatalf("ListEntries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries len = %d, want 2", len(entries))
	}
	if entries[0].Path != "/app/other.txt" && entries[1].Path != "/app/other.txt" {
		t.Fatalf("missing /app/other.txt in %v", entries)
	}
	if entries[0].Path != "/app/resources" && entries[1].Path != "/app/resources" {
		t.Fatalf("missing /app/resources in %v", entries)
	}
}

func TestListEntriesNotFound(t *testing.T) {
	img := testutil.NewImage(t)

	_, err := image.ListEntries(img, "/missing")
	if !errors.Is(err, image.ErrPathNotFound) {
		t.Fatalf("ListEntries error = %v, want ErrPathNotFound", err)
	}
}

func TestUnzipTo(t *testing.T) {
	zipPath := testutil.WriteZip(t, map[string][]byte{
		"config.json": []byte(`{"key":"value"}`),
		"README.txt":  []byte("hello"),
	})
	zipData, _ := os.ReadFile(zipPath)

	img := testutil.NewImageWithFiles(t, map[string][]byte{
		"app/bundle.zip": zipData,
	})

	img2, err := image.UnzipTo(img, "/app/bundle.zip", "/app/extracted/", "")
	if err != nil {
		t.Fatalf("UnzipTo: %v", err)
	}

	for path, wantContent := range map[string]string{
		"/app/extracted/config.json": `{"key":"value"}`,
		"/app/extracted/README.txt":  "hello",
	} {
		tmp := t.TempDir() + "/out"
		if err := image.ExtractFileToLocal(img2, path, tmp); err != nil {
			t.Errorf("extract %s: %v", path, err)
			continue
		}
		got, _ := os.ReadFile(tmp)
		if string(got) != wantContent {
			t.Errorf("%s: got %q, want %q", path, got, wantContent)
		}
	}
}

func TestAddDirLayer(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(dir+"/a.txt", []byte("file a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/b.txt", []byte("file b"), 0o644); err != nil {
		t.Fatal(err)
	}

	img := testutil.NewImage(t)
	img2, err := image.AddDirLayer(img, dir, "/data", "0644", "")
	if err != nil {
		t.Fatalf("AddDirLayer: %v", err)
	}

	for name, wantContent := range map[string]string{
		"a.txt": "file a",
		"b.txt": "file b",
	} {
		tmp := t.TempDir() + "/" + name
		if err := image.ExtractFileToLocal(img2, "/data/"+name, tmp); err != nil {
			t.Errorf("missing %s: %v", name, err)
			continue
		}
		got, _ := os.ReadFile(tmp)
		if string(got) != wantContent {
			t.Errorf("%s: got %q, want %q", name, got, wantContent)
		}
	}
}
