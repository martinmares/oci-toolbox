package recipe_test

import (
	"os"
	"path/filepath"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"

	"github.com/martinmares/oci-patch/internal/image"
	"github.com/martinmares/oci-patch/internal/recipe"
	"github.com/martinmares/oci-patch/internal/testutil"
)

// writeRecipe writes a YAML string to a temp file and returns its path.
func writeRecipe(t *testing.T, content string) string {
	t.Helper()
	f := filepath.Join(t.TempDir(), "recipe.yml")
	if err := os.WriteFile(f, []byte(content), 0o644); err != nil {
		t.Fatalf("write recipe: %v", err)
	}
	return f
}

// emptyCtx returns an empty recipe context (no named sources).
func emptyCtx() *recipe.Context {
	return &recipe.Context{Sources: map[string]v1.Image{}}
}

// extractFile is a test helper that reads a file from an image.
func extractFile(t *testing.T, img v1.Image, path string) []byte {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "extracted")
	if err := image.ExtractFileToLocal(img, path, dst); err != nil {
		t.Fatalf("extract %s: %v", path, err)
	}
	data, _ := os.ReadFile(dst)
	return data
}

// --- tests ---

func TestRecipe_addFile(t *testing.T) {
	content := []byte("injected binary")
	srcFile := testutil.WriteLocalFile(t, content)

	r, err := recipe.Load(writeRecipe(t, `
name: "test-add-file"
steps:
  - action: add-file
    src: `+srcFile+`
    dst: /usr/local/bin/tool
    mode: "0755"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	result, err := recipe.Apply(testutil.NewImage(t), r, emptyCtx())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got := extractFile(t, result, "/usr/local/bin/tool")
	if string(got) != string(content) {
		t.Errorf("got %q, want %q", got, content)
	}
}

// condition: if-not-exists → soubor již existuje → přeskočit
func TestRecipe_addFile_ifNotExists_skips(t *testing.T) {
	existing := []byte("original binary — must not change")
	img := testutil.NewImageWithFiles(t, map[string][]byte{
		"usr/local/bin/tool": existing,
	})

	replacement := testutil.WriteLocalFile(t, []byte("should NOT appear"))

	r, err := recipe.Load(writeRecipe(t, `
name: "test-if-not-exists-skip"
steps:
  - action: add-file
    src: `+replacement+`
    dst: /usr/local/bin/tool
    mode: "0755"
    condition:
      if-not-exists: /usr/local/bin/tool
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	result, err := recipe.Apply(img, r, emptyCtx())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got := extractFile(t, result, "/usr/local/bin/tool")
	if string(got) != string(existing) {
		t.Errorf("file should be unchanged: got %q, want %q", got, existing)
	}
}

// condition: if-not-exists → soubor chybí → přidat
func TestRecipe_addFile_ifNotExists_adds(t *testing.T) {
	content := []byte("new tool content")
	srcFile := testutil.WriteLocalFile(t, content)

	r, err := recipe.Load(writeRecipe(t, `
name: "test-if-not-exists-add"
steps:
  - action: add-file
    src: `+srcFile+`
    dst: /usr/local/bin/tool
    mode: "0755"
    condition:
      if-not-exists: /usr/local/bin/tool
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	result, err := recipe.Apply(testutil.NewImage(t), r, emptyCtx())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got := extractFile(t, result, "/usr/local/bin/tool")
	if string(got) != string(content) {
		t.Errorf("got %q, want %q", got, content)
	}
}

// bez condition → vždy přepsat, i když soubor existuje
func TestRecipe_addFile_noCondition_alwaysOverwrites(t *testing.T) {
	existing := []byte("old content")
	img := testutil.NewImageWithFiles(t, map[string][]byte{
		"app/config.txt": existing,
	})

	newContent := []byte("new content")
	srcFile := testutil.WriteLocalFile(t, newContent)

	r, err := recipe.Load(writeRecipe(t, `
name: "test-overwrite"
steps:
  - action: add-file
    src: `+srcFile+`
    dst: /app/config.txt
    mode: "0644"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	result, err := recipe.Apply(img, r, emptyCtx())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got := extractFile(t, result, "/app/config.txt")
	if string(got) != string(newContent) {
		t.Errorf("expected overwrite: got %q, want %q", got, newContent)
	}
}

func TestRecipe_removeFile(t *testing.T) {
	img := testutil.NewImageWithFiles(t, map[string][]byte{
		"tmp/secret.txt": []byte("remove me"),
	})

	r, err := recipe.Load(writeRecipe(t, `
name: "test-remove"
steps:
  - action: remove-file
    dst: /tmp/secret.txt
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	result, err := recipe.Apply(img, r, emptyCtx())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	err = image.ExtractFileToLocal(result, "/tmp/secret.txt", t.TempDir()+"/out")
	if err == nil {
		t.Error("expected file to be removed (whiteout), but ExtractFileToLocal succeeded")
	}
}

func TestRecipe_removeDir(t *testing.T) {
	img := testutil.NewImageWithFiles(t, map[string][]byte{
		"usr/share/java/maven-3/lib/plexus-utils-3.6.0.jar": []byte("jar"),
		"usr/share/java/maven-3/bin/mvn":                    []byte("script"),
		"usr/share/java/keep.txt":                           []byte("keep"),
	})

	r, err := recipe.Load(writeRecipe(t, `
name: "test-remove-dir"
steps:
  - action: remove-dir
    dst: /usr/share/java/maven-3
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	result, err := recipe.Apply(img, r, emptyCtx())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	err = image.ExtractFileToLocal(result, "/usr/share/java/maven-3/lib/plexus-utils-3.6.0.jar", t.TempDir()+"/removed")
	if err == nil {
		t.Fatal("expected file inside removed directory to be gone")
	}

	got := extractFile(t, result, "/usr/share/java/keep.txt")
	if string(got) != "keep" {
		t.Fatalf("unexpected sibling file contents: %q", got)
	}
}

func TestRecipe_setMetadata(t *testing.T) {
	r, err := recipe.Load(writeRecipe(t, `
name: "test-metadata"
steps:
  - action: set-env
    key: APP_PORT
    value: "9090"
  - action: set-label
    key: org.example.team
    value: platform
  - action: set-user
    value: "1001:1001"
  - action: set-workdir
    value: /app
  - action: set-entrypoint
    value_list: ["/bin/myapp", "--prod"]
  - action: set-cmd
    value_list: ["--port", "8080"]
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	result, err := recipe.Apply(testutil.NewImage(t), r, emptyCtx())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	cfg, _ := result.ConfigFile()

	foundEnv := false
	for _, e := range cfg.Config.Env {
		if e == "APP_PORT=9090" {
			foundEnv = true
		}
	}
	if !foundEnv {
		t.Errorf("APP_PORT=9090 missing from env: %v", cfg.Config.Env)
	}

	if cfg.Config.Labels["org.example.team"] != "platform" {
		t.Errorf("label: got %v", cfg.Config.Labels)
	}
	if cfg.Config.User != "1001:1001" {
		t.Errorf("user: got %q", cfg.Config.User)
	}
	if cfg.Config.WorkingDir != "/app" {
		t.Errorf("workdir: got %q", cfg.Config.WorkingDir)
	}
	if len(cfg.Config.Entrypoint) != 2 || cfg.Config.Entrypoint[0] != "/bin/myapp" {
		t.Errorf("entrypoint: %v", cfg.Config.Entrypoint)
	}
	if len(cfg.Config.Cmd) != 2 || cfg.Config.Cmd[0] != "--port" {
		t.Errorf("cmd: %v", cfg.Config.Cmd)
	}
}

func TestRecipe_copyFileFrom(t *testing.T) {
	srcContent := []byte("app.jar content")
	srcImg := testutil.NewImageWithFiles(t, map[string][]byte{
		"app/dist/app.jar": srcContent,
	})
	dstImg := testutil.NewImage(t)

	r, err := recipe.Load(writeRecipe(t, `
name: "test-copy-from"
sources:
  - id: source
    ref: "{{.SourceImage}}"
steps:
  - action: copy-file-from
    from: source
    src: /app/dist/app.jar
    dst: /app/dist/app.jar
    mode: "0644"
    owner: "1001:1001"
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	ctx, err := recipe.ResolveContextSources(r, "fake://source", srcImg)
	if err != nil {
		t.Fatalf("ResolveContextSources: %v", err)
	}

	result, err := recipe.Apply(dstImg, r, ctx)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got := extractFile(t, result, "/app/dist/app.jar")
	if string(got) != string(srcContent) {
		t.Errorf("got %q, want %q", got, srcContent)
	}
}

func TestRecipe_unzipTo(t *testing.T) {
	zipPath := testutil.WriteZip(t, map[string][]byte{
		"config.json": []byte(`{"key":"value"}`),
		"README.txt":  []byte("hello"),
	})
	zipData, _ := os.ReadFile(zipPath)

	img := testutil.NewImageWithFiles(t, map[string][]byte{
		"app/bundle.zip": zipData,
	})

	r, err := recipe.Load(writeRecipe(t, `
name: "test-unzip"
steps:
  - action: unzip-to
    src: /app/bundle.zip
    dst: /app/extracted/
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	result, err := recipe.Apply(img, r, emptyCtx())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	for path, want := range map[string]string{
		"/app/extracted/config.json": `{"key":"value"}`,
		"/app/extracted/README.txt":  "hello",
	} {
		got := extractFile(t, result, path)
		if string(got) != want {
			t.Errorf("%s: got %q, want %q", path, got, want)
		}
	}
}

func TestRecipe_multiStep(t *testing.T) {
	tool1 := testutil.WriteLocalFile(t, []byte("tool1-content"))
	tool2 := testutil.WriteLocalFile(t, []byte("tool2-content"))

	r, err := recipe.Load(writeRecipe(t, `
name: "test-multi-step"
steps:
  - action: add-file
    src: `+tool1+`
    dst: /usr/local/bin/tool1
    mode: "0755"
  - action: add-file
    src: `+tool2+`
    dst: /usr/local/bin/tool2
    mode: "0755"
  - action: set-env
    key: TOOLS_READY
    value: "true"
  - action: set-label
    key: patched-by
    value: oci-patch
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	result, err := recipe.Apply(testutil.NewImage(t), r, emptyCtx())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	for _, tool := range []string{"tool1", "tool2"} {
		extractFile(t, result, "/usr/local/bin/"+tool) // panics on error via t.Fatal
	}

	cfg, _ := result.ConfigFile()
	foundEnv := false
	for _, e := range cfg.Config.Env {
		if e == "TOOLS_READY=true" {
			foundEnv = true
		}
	}
	if !foundEnv {
		t.Error("TOOLS_READY=true not in env")
	}
	if cfg.Config.Labels["patched-by"] != "oci-patch" {
		t.Errorf("label: %v", cfg.Config.Labels)
	}
}

func TestRecipe_invalidAction_rejected(t *testing.T) {
	_, err := recipe.Load(writeRecipe(t, `
name: "test-invalid"
steps:
  - action: run
    commands: ["apk upgrade"]
`))
	if err == nil {
		t.Error("expected validation error for unknown action 'run', got nil")
	}
}

func TestRecipe_extractFile(t *testing.T) {
	content := []byte("jar bytes")
	srcImg := testutil.NewImageWithFiles(t, map[string][]byte{
		"app/dist/app.jar": content,
	})

	dstLocal := filepath.Join(t.TempDir(), "app.jar")

	r, err := recipe.Load(writeRecipe(t, `
name: "test-extract"
sources:
  - id: source
    ref: "{{.SourceImage}}"
steps:
  - action: extract-file
    from: source
    src: /app/dist/app.jar
    dst-local: `+dstLocal+`
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	ctx, err := recipe.ResolveContextSources(r, "fake://source", srcImg)
	if err != nil {
		t.Fatalf("ResolveContextSources: %v", err)
	}

	_, err = recipe.Apply(testutil.NewImage(t), r, ctx)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got, err := os.ReadFile(dstLocal)
	if err != nil {
		t.Fatalf("reading extracted file: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("got %q, want %q", got, content)
	}
}

func TestRecipe_addFile_ifNotExistsIn_skips(t *testing.T) {
	// binary already in /bin — condition covers /bin → skip
	img := testutil.NewImageWithFiles(t, map[string][]byte{
		"bin/encjson": []byte("original"),
	})
	src := testutil.WriteLocalFile(t, []byte("should NOT appear"))

	r, err := recipe.Load(writeRecipe(t, `
name: "test-if-not-exists-in-skip"
steps:
  - action: add-file
    src: `+src+`
    dst: /usr/local/bin/encjson
    mode: "0755"
    condition:
      if-not-exists-in: [/bin, /usr/bin, /usr/local/bin]
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	result, err := recipe.Apply(img, r, emptyCtx())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	err = image.ExtractFileToLocal(result, "/usr/local/bin/encjson", t.TempDir()+"/out")
	if err == nil {
		t.Error("file should not have been added — basename already found in /bin")
	}
}

func TestRecipe_addFile_ifNotExistsIn_adds(t *testing.T) {
	img := testutil.NewImageWithFiles(t, map[string][]byte{
		"opt/vendor/encjson": []byte("somewhere else"),
	})
	content := []byte("new encjson")
	src := testutil.WriteLocalFile(t, content)

	r, err := recipe.Load(writeRecipe(t, `
name: "test-if-not-exists-in-add"
steps:
  - action: add-file
    src: `+src+`
    dst: /usr/local/bin/encjson
    mode: "0755"
    condition:
      if-not-exists-in: [/bin, /usr/bin, /usr/local/bin]
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	result, err := recipe.Apply(img, r, emptyCtx())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got := extractFile(t, result, "/usr/local/bin/encjson")
	if string(got) != string(content) {
		t.Errorf("got %q, want %q", got, content)
	}
}

func TestRecipe_replaceFile_removesAllAndAdds(t *testing.T) {
	img := testutil.NewImageWithFiles(t, map[string][]byte{
		"bin/encjson":     []byte("old /bin"),
		"usr/bin/encjson": []byte("old /usr/bin"),
		"usr/bin/other":   []byte("unrelated"),
	})
	content := []byte("brand new encjson")
	src := testutil.WriteLocalFile(t, content)

	r, err := recipe.Load(writeRecipe(t, `
name: "test-replace-file"
steps:
  - action: replace-file
    src: `+src+`
    dst: /usr/local/bin/encjson
    mode: "0755"
    find-all: encjson
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	result, err := recipe.Apply(img, r, emptyCtx())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	for _, p := range []string{"/bin/encjson", "/usr/bin/encjson"} {
		if err := image.ExtractFileToLocal(result, p, t.TempDir()+"/out"); err == nil {
			t.Errorf("%s should have been removed", p)
		}
	}

	got := extractFile(t, result, "/usr/local/bin/encjson")
	if string(got) != string(content) {
		t.Errorf("got %q, want %q", got, content)
	}

	if err := image.ExtractFileToLocal(result, "/usr/bin/other", t.TempDir()+"/other"); err != nil {
		t.Errorf("unrelated file should still exist: %v", err)
	}
}

func TestRecipe_replaceFile_nothingFound_justAdds(t *testing.T) {
	img := testutil.NewImage(t)
	content := []byte("fresh tool")
	src := testutil.WriteLocalFile(t, content)

	r, err := recipe.Load(writeRecipe(t, `
name: "test-replace-nothing"
steps:
  - action: replace-file
    src: `+src+`
    dst: /usr/local/bin/encjson
    mode: "0755"
    find-all: encjson
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	result, err := recipe.Apply(img, r, emptyCtx())
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	got := extractFile(t, result, "/usr/local/bin/encjson")
	if string(got) != string(content) {
		t.Errorf("got %q, want %q", got, content)
	}
}

func TestRecipe_shadowFromAndFixPermissions(t *testing.T) {
	srcImg := testutil.NewImageWithFiles(t, map[string][]byte{
		"app/bin/start.sh": []byte("#!/bin/sh\necho ok\n"),
		"app/conf/app.yml": []byte("name: gateway\n"),
	})
	baseImg := testutil.NewImageWithFiles(t, map[string][]byte{
		"etc/os-release": []byte("ID=test\n"),
		"app/old.txt":    []byte("should disappear"),
	})

	r, err := recipe.Load(writeRecipe(t, `
name: "test-shadow-from"
sources:
  - id: src
    ref: "{{.SourceImage}}"
  - id: base
    ref: docker://example.com/base:latest
steps:
  - action: use-base
    from: base
  - action: shadow
    from: src
    path: /app
    extract-to: /app
    output: /app/app.self
  - action: fix-permissions
    path: /app
    owner: "1001:0"
    mode: g=u
    setgid-dirs: true
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	ctx := &recipe.Context{Sources: map[string]v1.Image{
		"src":  srcImg,
		"base": baseImg,
	}}

	result, err := recipe.Apply(testutil.NewImage(t), r, ctx)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	if err := image.ExtractFileToLocal(result, "/etc/os-release", filepath.Join(t.TempDir(), "os-release")); err != nil {
		t.Fatalf("base image file should remain: %v", err)
	}
	if err := image.ExtractFileToLocal(result, "/app/old.txt", filepath.Join(t.TempDir(), "old")); err == nil {
		t.Fatal("/app/old.txt should have been removed by shadow replace")
	}

	entries, err := image.CollectTree(result, "/app")
	if err != nil {
		t.Fatalf("CollectTree(/app): %v", err)
	}

	gotPaths := map[string]bool{}
	var appDir, appSelf *image.TreeEntry
	for i := range entries {
		entry := &entries[i]
		gotPaths[entry.Path] = true
		switch entry.Path {
		case "/app":
			appDir = entry
		case "/app/app.self":
			appSelf = entry
		}
	}

	if len(gotPaths) != 2 || !gotPaths["/app"] || !gotPaths["/app/app.self"] {
		t.Fatalf("unexpected /app tree: %+v", gotPaths)
	}
	if appDir == nil || appSelf == nil {
		t.Fatalf("missing bootstrap entries: dir=%v file=%v", appDir, appSelf)
	}
	if appDir.UID != 1001 || appDir.GID != 0 {
		t.Fatalf("/app ownership = %d:%d, want 1001:0", appDir.UID, appDir.GID)
	}
	if appSelf.UID != 1001 || appSelf.GID != 0 {
		t.Fatalf("/app/app.self ownership = %d:%d, want 1001:0", appSelf.UID, appSelf.GID)
	}
	if appDir.Mode.Perm() != 0o775 {
		t.Fatalf("/app mode = %#o, want 0775", appDir.Mode.Perm())
	}
	if appDir.Mode&os.ModeSetgid == 0 {
		t.Fatal("/app should have setgid bit")
	}
	if appSelf.Mode.Perm() != 0o775 {
		t.Fatalf("/app/app.self mode = %#o, want 0775", appSelf.Mode.Perm())
	}
}

func TestShadowReduceKeepsOnlyBootstrap(t *testing.T) {
	srcImg := testutil.NewImageWithFiles(t, map[string][]byte{
		"app/bin/start.sh": []byte("#!/bin/sh\necho ok\n"),
		"app/conf/app.yml": []byte("name: gateway\n"),
	})
	baseImg := testutil.NewImageWithFiles(t, map[string][]byte{
		"etc/os-release": []byte("ID=test\n"),
		"app/old.txt":    []byte("should disappear"),
	})

	r, err := recipe.Load(writeRecipe(t, `
name: "test-shadow-reduce"
steps:
  - action: shadow
    from: src
    path: /app
    extract-to: /app
    output: /app/app.self
  - action: fix-permissions
    path: /app
    owner: "1001:0"
    mode: g=u
    setgid-dirs: true
`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	ctx := &recipe.Context{Sources: map[string]v1.Image{
		"src": srcImg,
	}}

	patched, err := recipe.Apply(baseImg, r, ctx)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	reduced, err := image.Squash(patched)
	if err != nil {
		t.Fatalf("Squash: %v", err)
	}

	entries, err := image.CollectTree(reduced, "/app")
	if err != nil {
		t.Fatalf("CollectTree(/app): %v", err)
	}
	gotPaths := map[string]bool{}
	for _, entry := range entries {
		gotPaths[entry.Path] = true
	}
	if len(gotPaths) != 2 || !gotPaths["/app"] || !gotPaths["/app/app.self"] {
		t.Fatalf("unexpected reduced /app tree: %+v", gotPaths)
	}
}
