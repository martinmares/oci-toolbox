package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/martinmares/oci-toolbox/internal/registry"
	"github.com/martinmares/oci-toolbox/internal/registryconfig"
)

func TestGitLabCleanupPreservesRetentionAndReportsPartialFailure(t *testing.T) {
	for _, mode := range []string{"dry-run", "apply", "partial-failure", "false-success", "changed-digest"} {
		t.Run(mode, func(t *testing.T) {
			refs := map[string]string{
				"2025.01.01.01": "sha256:protected", "2026.01.01.01": "sha256:protected",
				"2026.02.01.01": "sha256:new",
				"2025.02.01.01": "sha256:old", "2025.02.02.01": "sha256:old",
			}
			var deleted []string
			reads := map[string]int{}
			apiReads, registryDeletes := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasPrefix(r.URL.Path, "/v2/") {
					if r.Method == http.MethodDelete {
						registryDeletes++
						w.WriteHeader(401)
						return
					}
					if strings.HasSuffix(r.URL.Path, "/tags/list") {
						tags := []string{}
						for tag := range refs {
							tags = append(tags, tag)
						}
						json.NewEncoder(w).Encode(map[string]any{"tags": tags})
						return
					}
					ref := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
					reads[ref]++
					digest := refs[ref]
					if mode == "changed-digest" && ref == "2025.02.02.01" && reads[ref] > 1 {
						digest = "sha256:retargeted"
					}
					w.Header().Set("Docker-Content-Digest", digest)
					fmt.Fprintf(w, `{"layers":[{"digest":%q,"size":10}]}`, refs[ref])
					return
				}
				if r.Header.Get("PRIVATE-TOKEN") != "test-token" {
					t.Error("missing GitLab token")
				}
				if strings.HasSuffix(r.URL.Path, "/repositories") {
					apiReads++
					fmt.Fprint(w, `[{"id":42,"path":"team/app"}]`)
					return
				}
				tag := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
				if r.Method == http.MethodGet {
					t.Error("GitLab tag detail GET must not be used")
					w.WriteHeader(404)
					return
				}
				if mode == "partial-failure" && len(deleted) == 1 {
					w.WriteHeader(403)
					return
				}
				deleted = append(deleted, tag)
				if mode != "false-success" {
					delete(refs, tag)
				}
				w.WriteHeader(200)
			}))
			defer server.Close()
			client, err := registry.NewClient(registry.Config{BaseURL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			gitlab, err := registry.NewGitLabClient(registry.Config{BaseURL: server.URL}, "1582", "test-token")
			if err != nil {
				t.Fatal(err)
			}
			cfg := registryconfig.Config{Cleanup: registryconfig.CleanupConfig{
				Mode: "timestamp", KeepCount: 1, ProtectLatest: true,
			}}
			total, kept, removed := map[string]int64{}, map[string]int64{}, map[string]int64{}
			summary, err := cleanupRepository(context.Background(), client, cfg, "team/app", mode == "dry-run",
				[]*regexp.Regexp{regexp.MustCompile(`^2026\.01\.01\.01$`)}, total, kept, removed, gitlab)
			if (err != nil) != (mode == "partial-failure" || mode == "false-success" || mode == "changed-digest") {
				t.Fatalf("error=%v", err)
			}
			if summary.ProtectedManifests != 1 || summary.KeptManifests != 1 || summary.RemoveManifests != 1 || summary.RemoveTags != 2 {
				t.Fatalf("unexpected retention: %+v", summary)
			}
			if registryDeletes != 0 {
				t.Fatal("used Registry DELETE in GitLab mode")
			}
			switch mode {
			case "dry-run":
				if apiReads != 1 || len(deleted) != 0 || summary.RemovedTags != 0 {
					t.Fatal("dry-run did not check GitLab access, or performed deletions")
				}
			case "apply":
				if !reflect.DeepEqual(deleted, []string{"2025.02.02.01", "2025.02.01.01"}) || summary.RemovedTags != 2 || summary.RemovedManifests != 1 {
					t.Fatalf("deleted=%v summary=%+v", deleted, summary)
				}
			case "partial-failure":
				if summary.RemovedTags != 1 || summary.RemovedManifests != 0 || len(removed) != 0 || kept["sha256:old"] != 10 {
					t.Fatalf("partial failure accounting: %+v removed=%v kept=%v", summary, removed, kept)
				}
			case "changed-digest":
				if len(deleted) != 0 || summary.RemovedTags != 0 || !strings.Contains(err.Error(), "changed since planning") {
					t.Fatalf("retargeted tag was deleted: deleted=%v summary=%+v error=%v", deleted, summary, err)
				}
			case "false-success":
				if summary.RemovedTags != 0 || summary.RemovedManifests != 0 || kept["sha256:old"] != 10 || !strings.Contains(err.Error(), "registry still lists 2") {
					t.Fatalf("false success was not detected: summary=%+v error=%v", summary, err)
				}
			}
		})
	}
}

func TestRegistryCleanupReturnsFailureForRepositoryErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "registry.yml")
	config := fmt.Sprintf("registry:\n  url: %s\nrepositories:\n  - name: team/app\n", server.URL)
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	err := runCleanup(context.Background(), rootOptions{configPath: path})
	if err == nil || !strings.Contains(err.Error(), "cleanup failed for 1 repositories") {
		t.Fatalf("repository failure was hidden: %v", err)
	}
}

func TestRegistryCleanupSkipsRepositoryWhenAnyTagCannotBeInspected(t *testing.T) {
	deletes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletes++
			w.WriteHeader(204)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/tags/list") {
			fmt.Fprint(w, `{"tags":["2025.01.01.01","2026.01.01.01","protected"]}`)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/protected") {
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Docker-Content-Digest", "sha256:"+r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
		fmt.Fprint(w, `{}`)
	}))
	defer server.Close()
	client, err := registry.NewClient(registry.Config{BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = cleanupRepository(context.Background(), client, registryconfig.Config{Cleanup: registryconfig.CleanupConfig{Mode: "timestamp", KeepCount: 1}},
		"team/app", false, nil, map[string]int64{}, map[string]int64{}, map[string]int64{}, nil)
	if err == nil || deletes != 0 {
		t.Fatalf("error=%v deletes=%d", err, deletes)
	}
}
