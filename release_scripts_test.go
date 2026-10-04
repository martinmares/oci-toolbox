package main

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

func releaseFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, tool := range []string{"bash", "git", "curl"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s required", tool)
		}
	}
	write := func(name, content string) {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"ci_release_common.sh", "ci_release_notes.sh", "ci_publish_github_release.sh", "ci_upload_release_packages.sh", "ci_publish_release.sh"} {
		data, err := os.ReadFile(filepath.Join("scripts", name))
		if err != nil {
			t.Fatal(err)
		}
		write("scripts/"+name, string(data))
	}
	write("VERSION", "1.2.3\n")
	write("dist/release_notes.md", "Release notes\n")
	name := "oci-toolbox-1.2.3-linux-amd64.tar.gz"
	write("dist/release/"+name, "test archive")
	write("dist/release/SHA256SUMS", fmt.Sprintf("%x  %s\n", sha256.Sum256([]byte("test archive")), name))
	write("bin/gh", `#!/usr/bin/env bash
echo "$*" >> "$GH_LOG"
if [[ "$1 $2" == "release view" ]]; then exit "${GH_EXISTS:-1}"; fi
if [[ "$1" == api ]]; then echo "$REMOTE_COMMIT"; fi
`)
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.email", "ci@example.com"}, {"config", "user.name", "Release test"}, {"add", "."}, {"commit", "-qm", "fixture"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %s: %v", out, err)
		}
	}
	return dir
}

func runReleaseScript(dir, name string, env ...string) (string, error) {
	cmd := exec.Command("bash", "scripts/"+name)
	cmd.Dir = dir
	// Do not let CI metadata or publishing credentials leak into fixtures.
	for _, entry := range os.Environ() {
		key := strings.SplitN(entry, "=", 2)[0]
		if strings.HasPrefix(key, "CI_") || strings.HasPrefix(key, "GITHUB_") || strings.HasPrefix(key, "GH_") || strings.HasPrefix(key, "RELEASE_") {
			continue
		}
		cmd.Env = append(cmd.Env, entry)
	}
	cmd.Env = append(cmd.Env, "PATH="+filepath.Join(dir, "bin")+string(os.PathListSeparator)+os.Getenv("PATH"), "GH_TOKEN=test", "GH_REPO=example/tool", "GH_LOG="+filepath.Join(dir, "gh.log"))
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestGitHubReleaseScripts(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(fmt.Sprint(existing), func(t *testing.T) {
			dir := releaseFixture(t)
			cmd := exec.Command("git", "rev-parse", "HEAD")
			cmd.Dir = dir
			sha, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			exists := "1"
			if existing {
				exists = "0"
			}
			out, err := runReleaseScript(dir, "ci_publish_github_release.sh", "GH_EXISTS="+exists, "REMOTE_COMMIT="+strings.TrimSpace(string(sha)))
			if err != nil {
				t.Fatalf("%s: %v", out, err)
			}
			log, _ := os.ReadFile(filepath.Join(dir, "gh.log"))
			if strings.Contains(string(log), "release create") == existing {
				t.Fatal(string(log))
			}
			for _, want := range []string{"release upload", "--clobber", "--draft=false", "--prerelease=false"} {
				if !strings.Contains(string(log), want) {
					t.Fatalf("missing %s: %s", want, log)
				}
			}
		})
	}
}

func TestReleaseRejectsInvalidMetadataAndCorruption(t *testing.T) {
	for _, scenario := range []string{"version", "remote-commit", "checksum"} {
		t.Run(scenario, func(t *testing.T) {
			dir := releaseFixture(t)
			env := []string{}
			switch scenario {
			case "version":
				env = append(env, "RELEASE_VERSION=9.9.9")
			case "remote-commit":
				env = append(env, "GH_EXISTS=0", "REMOTE_COMMIT="+strings.Repeat("0", 40))
			case "checksum":
				if err := os.WriteFile(filepath.Join(dir, "dist/release/oci-toolbox-1.2.3-linux-amd64.tar.gz"), []byte("corrupt"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if out, err := runReleaseScript(dir, "ci_publish_github_release.sh", env...); err == nil {
				t.Fatalf("unexpected success: %s", out)
			}
			log, _ := os.ReadFile(filepath.Join(dir, "gh.log"))
			if strings.Contains(string(log), "release upload") || strings.Contains(string(log), "release create") {
				t.Fatalf("unexpected write: %s", log)
			}
		})
	}
}

func TestGitLabPackageUpload(t *testing.T) {
	dir := releaseFixture(t)
	var uploads []string
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != "PUT" || r.Header.Get("JOB-TOKEN") != "test" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			w.WriteHeader(403)
			return
		}
		uploads = append(uploads, filepath.Base(r.URL.Path))
		w.WriteHeader(201)
	}))
	defer server.Close()
	out, err := runReleaseScript(dir, "ci_upload_release_packages.sh", "CI_API_V4_URL="+server.URL, "CI_PROJECT_ID=1", "CI_JOB_TOKEN=test")
	if err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	list, err := os.ReadFile(filepath.Join(dir, "dist/release_assets.txt"))
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(uploads) != 2 || string(list) != strings.Join(uploads, "\n")+"\n" {
		t.Fatalf("uploads=%v assets=%s", uploads, list)
	}
}

func TestGitLabRejectsUnauthorizedAPI(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq required")
	}
	dir := releaseFixture(t)
	if err := os.WriteFile(filepath.Join(dir, "dist/release_assets.txt"), []byte("SHA256SUMS\n"), 0644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("unexpected write: %s", r.Method)
		}
		w.WriteHeader(403)
	}))
	defer server.Close()
	out, err := runReleaseScript(dir, "ci_publish_release.sh", "CI_API_V4_URL="+server.URL, "CI_PROJECT_ID=1", "CI_JOB_TOKEN=test")
	if err == nil || !strings.Contains(out, "HTTP 403") {
		t.Fatalf("out=%s err=%v", out, err)
	}
}

func TestGitLabReleaseCreationAndRetry(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq required")
	}
	dir := releaseFixture(t)
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	sha, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	commit := strings.TrimSpace(string(sha))
	if err := os.WriteFile(filepath.Join(dir, "dist/release_assets.txt"), []byte("SHA256SUMS\n"), 0644); err != nil {
		t.Fatal(err)
	}
	created, linked := false, false
	posts, updates := 0, 0
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("JOB-TOKEN") != "test" {
			t.Error("missing token")
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/assets/links"):
			if linked {
				fmt.Fprint(w, `[{"id":1,"name":"SHA256SUMS"}]`)
			} else {
				fmt.Fprint(w, `[]`)
			}
		case r.Method == "GET":
			if !created {
				w.WriteHeader(404)
			} else {
				fmt.Fprintf(w, `{"commit":{"id":%q}}`, commit)
			}
		case r.Method == "POST":
			posts++
			if strings.Contains(r.URL.Path, "/assets/links") {
				linked = true
			} else {
				created = true
			}
			fmt.Fprint(w, `{}`)
		case r.Method == "PUT":
			updates++
			fmt.Fprint(w, `{}`)
		default:
			t.Errorf("unexpected method %s", r.Method)
			w.WriteHeader(405)
		}
	}))
	defer server.Close()
	for i := 0; i < 2; i++ {
		out, err := runReleaseScript(dir, "ci_publish_release.sh", "CI_API_V4_URL="+server.URL, "CI_PROJECT_ID=1", "CI_JOB_TOKEN=test")
		if err != nil {
			t.Fatalf("%s: %v", out, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if posts != 2 || updates != 2 {
		t.Fatalf("posts=%d updates=%d", posts, updates)
	}
}

func TestReleaseNotesMetadata(t *testing.T) {
	dir := releaseFixture(t)
	if out, err := runReleaseScript(dir, "ci_release_notes.sh"); err != nil {
		t.Fatalf("%s: %v", out, err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "dist/release.env"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"RELEASE_VERSION=1.2.3", "RELEASE_TAG=v1.2.3", "RELEASE_COMMIT="} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("missing %s: %s", want, data)
		}
	}
}

func TestReleasePipelineConfiguration(t *testing.T) {
	configs := map[string]map[string]interface{}{}
	for _, name := range []string{".gitlab-ci.yml", ".github/workflows/release.yml"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		var config map[string]interface{}
		if err := yaml.Unmarshal(data, &config); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		configs[name] = config
	}
	gitlab := configs[".gitlab-ci.yml"]
	job := gitlab["release_binaries"].(map[string]interface{})
	artifacts := job["artifacts"].(map[string]interface{})["paths"].([]interface{})
	if len(artifacts) != 1 || artifacts[0] != "dist/release_assets.txt" {
		t.Fatalf("binary archives must not be job artifacts: %v", artifacts)
	}
	github := configs[".github/workflows/release.yml"]
	on := github["on"].(map[string]interface{})
	if _, ok := on["workflow_dispatch"]; !ok {
		t.Fatal("manual GitHub release missing")
	}
	paths := on["push"].(map[string]interface{})["paths"].([]interface{})
	if len(paths) != 1 || paths[0] != "VERSION" {
		t.Fatalf("unexpected release trigger: %v", paths)
	}
}
