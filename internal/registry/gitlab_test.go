package registry

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGitLabDeleteTagPaginationAndStatus(t *testing.T) {
	for _, status := range []int{200, 204, 404, 401, 403, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			pages, deletes := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("PRIVATE-TOKEN") != "test-token" || r.Header.Get("Authorization") != "" {
					t.Error("incorrect GitLab authentication")
				}
				if !strings.Contains(r.RequestURI, "/projects/team%2Fproject/") {
					t.Errorf("project path was not escaped: %s", r.RequestURI)
				}
				if strings.HasSuffix(r.URL.Path, "/repositories") {
					pages++
					if r.URL.Query().Get("page") == "1" {
						w.Header().Set("X-Next-Page", "2")
						fmt.Fprint(w, `[{"id":1,"path":"other/app"}]`)
					} else {
						fmt.Fprint(w, `[{"id":42,"path":"team/app"}]`)
					}
					return
				}
				if !strings.HasSuffix(r.URL.Path, "/repositories/42/tags/release-1") {
					t.Errorf("unexpected tag endpoint: %s", r.URL.Path)
				}
				if r.Method == http.MethodGet {
					t.Error("unexpected GitLab tag detail GET; it can return a false 404")
					w.WriteHeader(404)
					return
				}
				deletes++
				w.WriteHeader(status)
			}))
			defer server.Close()
			client, err := NewGitLabClient(Config{BaseURL: server.URL}, "team/project", "test-token")
			if err != nil {
				t.Fatal(err)
			}
			err = client.DeleteTag(context.Background(), "team/app", "release-1")
			wantError := status != 200 && status != 204 && status != 404
			if (err != nil) != wantError {
				t.Fatalf("error = %v", err)
			}
			if pages != 2 || deletes != 1 {
				t.Fatalf("pages=%d deletes=%d", pages, deletes)
			}
		})
	}
}

func TestGitLabRefusesMissingRepository(t *testing.T) {
	deletes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletes++
			w.WriteHeader(204)
			return
		}
		fmt.Fprint(w, `[{"id":42,"path":"team/app"}]`)
	}))
	defer server.Close()
	client, err := NewGitLabClient(Config{BaseURL: server.URL}, "1582", "test-token")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteTag(context.Background(), "other/app", "release-1"); err == nil {
		t.Fatal("accepted repository outside project")
	}
	if deletes != 0 {
		t.Fatalf("unexpected deletes: %d", deletes)
	}
}

func TestGitLabDoesNotForwardTokenOnRedirect(t *testing.T) {
	forwarded := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded = true }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer server.Close()
	client, err := NewGitLabClient(Config{BaseURL: server.URL}, "1582", "secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteTag(context.Background(), "team/app", "release-1"); err == nil {
		t.Fatal("redirect was accepted")
	}
	if forwarded {
		t.Fatal("request was forwarded to redirect target")
	}
}
