package springconfig

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/empty"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"gopkg.in/yaml.v3"
)

func TestDiscoverExtractsDirectoryAndJarConfigsInClasspathOrder(t *testing.T) {
	commonsJar := zipBytes(t, []archiveEntry{
		{name: "application-commons.yml", data: "tsm:\n  shared: true\n"},
		{name: "database-copy-defaults.yml", data: "database:\n  copy: true\n"},
		{name: "docs/example.yml", data: "ignored: true\n"},
	})
	featureJar := zipBytes(t, []archiveEntry{
		{name: "config/application.yml", data: "feature: true\n"},
	})
	img := imageWithLayers(t, []layerEntry{
		{name: "app/jib-classpath-file", data: "/app/resources:/app/classes:/app/libs/commons.jar:/app/libs/feature.jar"},
		{name: "app/jib-main-class-file", data: "com.example.Application\n"},
		{name: "app/resources/application.yml", data: "app:\n  name: example\n"},
		{name: "app/classes/application-kubernetes.properties", data: "mode=kubernetes\n"},
		{name: "app/libs/commons.jar", data: string(commonsJar)},
		{name: "app/libs/feature.jar", data: string(featureJar)},
	})

	output := filepath.Join(t.TempDir(), "analysis")
	manifest, err := Discover(img, "docker://registry.example.com/team/app:2.4", output, Options{})
	if err != nil {
		t.Fatal(err)
	}

	if manifest.JIB.MainClass != "com.example.Application" {
		t.Fatalf("main class = %q", manifest.JIB.MainClass)
	}
	if len(manifest.Configs) != 5 {
		t.Fatalf("configs = %d, want 5: %#v", len(manifest.Configs), manifest.Configs)
	}
	wantSources := []string{
		"/app/resources/application.yml",
		"/app/classes/application-kubernetes.properties",
		"/app/libs/commons.jar!/application-commons.yml",
		"/app/libs/commons.jar!/database-copy-defaults.yml",
		"/app/libs/feature.jar!/config/application.yml",
	}
	for i, want := range wantSources {
		if manifest.Configs[i].Source != want {
			t.Errorf("configs[%d].source = %q, want %q", i, manifest.Configs[i].Source, want)
		}
		wantClasspathIndex := []int{0, 1, 2, 2, 3}[i]
		if manifest.Configs[i].ClasspathIndex != wantClasspathIndex {
			t.Errorf("configs[%d].classpath_index = %d, want %d", i, manifest.Configs[i].ClasspathIndex, wantClasspathIndex)
		}
		data, err := os.ReadFile(filepath.Join(output, filepath.FromSlash(manifest.Configs[i].Output)))
		if err != nil {
			t.Errorf("reading extracted config %d: %v", i, err)
		}
		if len(data) == 0 {
			t.Errorf("extracted config %d is empty", i)
		}
	}
	commons := manifest.Configs[2]
	if commons.NameKind != "profile-named" || commons.ProfileHint != "commons" || commons.Activation != "unknown" {
		t.Errorf("unexpected commons classification: %#v", commons)
	}
	if _, err := os.Stat(filepath.Join(output, "sources", "0002-commons.jar", "docs", "example.yml")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("unrelated YAML should not be extracted, stat error = %v", err)
	}

	assertMode(t, output, 0o700)
	assertMode(t, filepath.Join(output, "manifest.yml"), 0o600)
	assertMode(t, filepath.Join(output, "manifest.svg"), 0o600)
	assertMode(t, filepath.Join(output, filepath.FromSlash(commons.Output)), 0o600)

	raw, err := os.ReadFile(filepath.Join(output, "manifest.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var decoded Manifest
	if err := yaml.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("decoding manifest: %v", err)
	}
	if len(decoded.Configs) != len(manifest.Configs) {
		t.Fatalf("manifest.yml configs = %d, want %d", len(decoded.Configs), len(manifest.Configs))
	}

	svg, err := os.ReadFile(filepath.Join(output, "manifest.svg"))
	if err != nil {
		t.Fatal(err)
	}
	decoder := xml.NewDecoder(bytes.NewReader(svg))
	for {
		if _, err := decoder.Token(); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			t.Fatalf("manifest.svg is not valid XML: %v", err)
		}
	}
	for _, want := range []string{
		"CLASSPATH [0000] DIRECTORY",
		"CLASSPATH [0002] JAR",
		"config-0003",
		"/app/libs/commons.jar!/application-commons.yml",
	} {
		if !bytes.Contains(svg, []byte(want)) {
			t.Errorf("manifest.svg does not contain %q", want)
		}
	}
}

func TestDiscoverKeepsEqualNamesFromDifferentClasspathEntries(t *testing.T) {
	first := zipBytes(t, []archiveEntry{{name: "application.yml", data: "source: first\n"}})
	second := zipBytes(t, []archiveEntry{{name: "application.yml", data: "source: second\n"}})
	img := imageWithLayers(t, []layerEntry{
		{name: "app/jib-classpath-file", data: "/app/libs/first.jar:/app/libs/second.jar"},
		{name: "app/jib-main-class-file", data: "com.example.Application"},
		{name: "app/libs/first.jar", data: string(first)},
		{name: "app/libs/second.jar", data: string(second)},
	})

	output := filepath.Join(t.TempDir(), "analysis")
	manifest, err := Discover(img, "example:latest", output, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Configs) != 2 {
		t.Fatalf("configs = %d, want 2", len(manifest.Configs))
	}
	if manifest.Configs[0].Output == manifest.Configs[1].Output {
		t.Fatalf("duplicate config outputs collide: %q", manifest.Configs[0].Output)
	}
	firstData, _ := os.ReadFile(filepath.Join(output, filepath.FromSlash(manifest.Configs[0].Output)))
	secondData, _ := os.ReadFile(filepath.Join(output, filepath.FromSlash(manifest.Configs[1].Output)))
	if string(firstData) != "source: first\n" || string(secondData) != "source: second\n" {
		t.Fatalf("unexpected extracted content: %q / %q", firstData, secondData)
	}
}

func TestDiscoverReportsProgress(t *testing.T) {
	img := imageWithLayers(t, []layerEntry{
		{name: "app/jib-classpath-file", data: "/app/resources"},
		{name: "app/jib-main-class-file", data: "com.example.Application"},
		{name: "app/resources/application.yml", data: "app: example\n"},
	})
	var messages []string
	_, err := Discover(img, "example:latest", filepath.Join(t.TempDir(), "analysis"), Options{
		Reporter: func(message string) {
			messages = append(messages, message)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(messages, "\n")
	for _, want := range []string{
		"reading JIB metadata",
		"scanning image layer 1/1",
		"examining 1 classpath entries",
		"writing 1 discovered configuration files",
		"writing YAML manifest and SVG map",
		"discovery complete",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("progress does not contain %q:\n%s", want, got)
		}
	}
}

func TestDiscoverUsesEffectiveFilesystemAcrossLayers(t *testing.T) {
	img := imageWithLayers(t, []layerEntry{
		{name: "app/jib-classpath-file", data: "/app/resources"},
		{name: "app/jib-main-class-file", data: "com.example.Application"},
		{name: "app/resources/application.yml", data: "value: old\n"},
		{name: "app/resources/application-removed.yml", data: "removed: true\n"},
	})
	img = appendLayer(t, img, []layerEntry{
		{name: "app/resources/.wh.application-removed.yml", typeflag: tar.TypeReg},
		{name: "app/resources/application.yml", data: "value: new\n"},
	})

	output := filepath.Join(t.TempDir(), "analysis")
	manifest, err := Discover(img, "example:latest", output, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Configs) != 1 {
		t.Fatalf("configs = %d, want 1: %#v", len(manifest.Configs), manifest.Configs)
	}
	data, err := os.ReadFile(filepath.Join(output, filepath.FromSlash(manifest.Configs[0].Output)))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "value: new\n" {
		t.Fatalf("effective application.yml = %q", data)
	}
}

func TestDiscoverHonorsOpaqueDirectory(t *testing.T) {
	img := imageWithLayers(t, []layerEntry{
		{name: "app/jib-classpath-file", data: "/app/resources"},
		{name: "app/jib-main-class-file", data: "com.example.Application"},
		{name: "app/resources/application-old.yml", data: "old: true\n"},
	})
	img = appendLayer(t, img, []layerEntry{
		{name: "app/resources/.wh..wh..opq", typeflag: tar.TypeReg},
		{name: "app/resources/application-new.yml", data: "new: true\n"},
	})

	output := filepath.Join(t.TempDir(), "analysis")
	manifest, err := Discover(img, "example:latest", output, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Configs) != 1 || !strings.HasSuffix(manifest.Configs[0].Source, "application-new.yml") {
		t.Fatalf("unexpected configs after opaque directory: %#v", manifest.Configs)
	}
}

func TestDiscoverAdditionalIncludeAndRelativeClasspath(t *testing.T) {
	img := imageWithLayers(t, []layerEntry{
		{name: "app/jib-classpath-file", data: "resources"},
		{name: "app/jib-main-class-file", data: "com.example.Application"},
		{name: "app/resources/custom/defaults.yml", data: "custom: true\n"},
	})
	output := filepath.Join(t.TempDir(), "analysis")
	manifest, err := Discover(img, "example:latest", output, Options{Include: []string{"custom/*.yml"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Classpath) != 1 || manifest.Classpath[0].Path != "/app/resources" {
		t.Fatalf("relative classpath not resolved against workdir: %#v", manifest.Classpath)
	}
	if len(manifest.Configs) != 1 || manifest.Configs[0].NameKind != "other" {
		t.Fatalf("custom include not extracted: %#v", manifest.Configs)
	}
}

func TestDiscoverRejectsUnsafeZipEntry(t *testing.T) {
	archive := zipBytes(t, []archiveEntry{{name: "../application.yml", data: "unsafe: true\n"}})
	img := imageWithLayers(t, []layerEntry{
		{name: "app/jib-classpath-file", data: "/app/libs/unsafe.jar"},
		{name: "app/jib-main-class-file", data: "com.example.Application"},
		{name: "app/libs/unsafe.jar", data: string(archive)},
	})
	_, err := Discover(img, "example:latest", filepath.Join(t.TempDir(), "analysis"), Options{})
	if err == nil || !strings.Contains(err.Error(), "path escapes archive root") {
		t.Fatalf("error = %v, want unsafe archive path", err)
	}
}

func TestDiscoverDoesNotOverwriteOutput(t *testing.T) {
	output := filepath.Join(t.TempDir(), "analysis")
	if err := os.Mkdir(output, 0o700); err != nil {
		t.Fatal(err)
	}
	_, err := Discover(empty.Image, "example:latest", output, Options{})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("error = %v, want existing output error", err)
	}
}

func TestDiscoverExpandsWildcardDeterministically(t *testing.T) {
	a := zipBytes(t, []archiveEntry{{name: "application-a.yml", data: "a: true\n"}})
	b := zipBytes(t, []archiveEntry{{name: "application-b.yml", data: "b: true\n"}})
	img := imageWithLayers(t, []layerEntry{
		{name: "app/jib-classpath-file", data: "/app/libs/*:/app/resources"},
		{name: "app/jib-main-class-file", data: "com.example.Application"},
		{name: "app/libs/b.jar", data: string(b)},
		{name: "app/libs/a.jar", data: string(a)},
		{name: "app/resources/application.yml", data: "root: true\n"},
	})
	manifest, err := Discover(img, "example:latest", filepath.Join(t.TempDir(), "analysis"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/app/libs/a.jar", "/app/libs/b.jar", "/app/resources"}
	if len(manifest.Classpath) != len(want) {
		t.Fatalf("classpath = %#v", manifest.Classpath)
	}
	for i := range want {
		if manifest.Classpath[i].Path != want[i] {
			t.Errorf("classpath[%d] = %q, want %q", i, manifest.Classpath[i].Path, want[i])
		}
	}
	if len(manifest.Warnings) == 0 || !strings.Contains(manifest.Warnings[0], "lexical order") {
		t.Fatalf("missing wildcard ordering warning: %#v", manifest.Warnings)
	}
}

type layerEntry struct {
	name     string
	data     string
	typeflag byte
}

type archiveEntry struct {
	name string
	data string
}

func imageWithLayers(t *testing.T, entries []layerEntry) v1.Image {
	t.Helper()
	img := appendLayer(t, empty.Image, entries)
	config, err := img.ConfigFile()
	if err != nil {
		t.Fatal(err)
	}
	config.OS = "linux"
	config.Architecture = "amd64"
	config.Config.WorkingDir = "/app"
	img, err = mutate.ConfigFile(img, config)
	if err != nil {
		t.Fatal(err)
	}
	return img
}

func appendLayer(t *testing.T, img v1.Image, entries []layerEntry) v1.Image {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, entry := range entries {
		typeflag := entry.typeflag
		if typeflag == 0 {
			typeflag = tar.TypeReg
		}
		header := &tar.Header{
			Name:     entry.name,
			Mode:     0o644,
			Size:     int64(len(entry.data)),
			Typeflag: typeflag,
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if len(entry.data) > 0 {
			if _, err := writer.Write([]byte(entry.data)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	layer, err := tarball.LayerFromReader(bytes.NewReader(buffer.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	result, err := mutate.AppendLayers(img, layer)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func zipBytes(t *testing.T, entries []archiveEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range entries {
		target, err := writer.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Write([]byte(entry.data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func assertMode(t *testing.T, target string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Errorf("%s mode = %#o, want %#o", target, got, want)
	}
}
