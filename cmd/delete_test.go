package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const deleteDigestA = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const deleteDigestB = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

type deletionFixture struct {
	tags              map[string]string
	deletes           []string
	unsupported       bool
	retargetOnRecheck bool
	ignoreDelete      bool
	heads             int
	server            *httptest.Server
}

func newDeletionFixture(t *testing.T) *deletionFixture {
	t.Helper()
	f := &deletionFixture{tags: map[string]string{"bad": deleteDigestA, "alias": deleteDigestA, "stable": deleteDigestB}}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v2/" {
			w.WriteHeader(200)
			return
		}
		if strings.Contains(r.URL.Path, "/blobs/") {
			t.Errorf("operation accesses image blobs: %s", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/v2/team/app/tags/list" {
			var tags []string
			for tag := range f.tags {
				tags = append(tags, fmt.Sprintf("%q", tag))
			}
			fmt.Fprintf(w, `{"name":"team/app","tags":[%s]}`, strings.Join(tags, ","))
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/v2/team/app/manifests/")
		if r.Method == "DELETE" {
			f.deletes = append(f.deletes, r.URL.Path)
			if f.ignoreDelete {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			if f.unsupported {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			if r.URL.Path == "/artifactory/docker-local/team/app/bad" {
				delete(f.tags, "bad")
			} else if strings.HasPrefix(id, "sha256:") {
				for tag, digest := range f.tags {
					if digest == id {
						delete(f.tags, tag)
					}
				}
			} else {
				delete(f.tags, id)
			}
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if r.Method == "HEAD" && strings.HasPrefix(r.URL.Path, "/v2/team/app/manifests/") {
			if id == "bad" {
				f.heads++
				if f.retargetOnRecheck && f.heads == 2 {
					f.tags[id] = deleteDigestB
				}
			}
			digest := f.tags[id]
			if strings.HasPrefix(id, "sha256:") {
				for _, d := range f.tags {
					if d == id {
						digest = d
					}
				}
			}
			if digest == "" {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("Docker-Content-Digest", digest)
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			w.Header().Set("Content-Length", "100")
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(404)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *deletionFixture) ref(suffix string) string {
	return strings.TrimPrefix(f.server.URL, "http://") + "/team/app" + suffix
}

func TestDeleteTagDryRunAndApplyPreserveOtherAliases(t *testing.T) {
	f := newDeletionFixture(t)
	var out bytes.Buffer
	command := newDeleteCommand(false)
	command.SetOut(&out)
	command.SetArgs([]string{f.ref(":bad")})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(f.deletes) != 0 || !strings.Contains(out.String(), "Dry-run") {
		t.Fatalf("dry-run deletes=%v output=%s", f.deletes, &out)
	}
	command = newDeleteCommand(false)
	command.SetOut(&out)
	command.SetArgs([]string{f.ref(":bad"), "--apply", "--expect-digest", deleteDigestA})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if len(f.deletes) != 1 || f.deletes[0] != "/v2/team/app/manifests/bad" || f.tags["alias"] != deleteDigestA || f.tags["stable"] != deleteDigestB {
		t.Fatalf("deletes=%v tags=%v", f.deletes, f.tags)
	}
}

func TestDeleteManifestListsAllAliasesAndDeletesOnlyRequestedDigest(t *testing.T) {
	f := newDeletionFixture(t)
	var out bytes.Buffer
	o := deleteOptions{timeout: time.Second}
	if err := runDelete(t.Context(), &out, f.ref("@"+deleteDigestA), true, o); err != nil {
		t.Fatal(err)
	}
	if len(f.deletes) != 0 || !strings.Contains(out.String(), "alias, bad") {
		t.Fatalf("deletes=%v output=%s", f.deletes, &out)
	}
	o.apply = true
	if err := runDelete(t.Context(), &out, f.ref("@"+deleteDigestA), true, o); err != nil {
		t.Fatal(err)
	}
	if len(f.deletes) != 1 || f.deletes[0] != "/v2/team/app/manifests/"+deleteDigestA || len(f.tags) != 1 || f.tags["stable"] != deleteDigestB {
		t.Fatalf("deletes=%v tags=%v", f.deletes, f.tags)
	}
}

func TestUnsupportedTagDeleteNeverFallsBackToDigest(t *testing.T) {
	f := newDeletionFixture(t)
	f.unsupported = true
	err := runDelete(t.Context(), &bytes.Buffer{}, f.ref(":bad"), false, deleteOptions{apply: true, timeout: time.Second})
	if err == nil || !strings.Contains(err.Error(), "no digest fallback") || len(f.deletes) != 1 || f.deletes[0] != "/v2/team/app/manifests/bad" || len(f.tags) != 3 {
		t.Fatalf("error=%v deletes=%v tags=%v", err, f.deletes, f.tags)
	}
}

func TestDeleteTagExpectedDigestMismatchNeverDeletes(t *testing.T) {
	f := newDeletionFixture(t)
	err := runDelete(t.Context(), &bytes.Buffer{}, f.ref(":bad"), false, deleteOptions{apply: true, expectDigest: deleteDigestB, timeout: time.Second})
	if err == nil || len(f.deletes) != 0 {
		t.Fatalf("error=%v deletes=%v", err, f.deletes)
	}
}

func TestArtifactoryTagDeletionUsesExplicitFolderOnly(t *testing.T) {
	f := newDeletionFixture(t)
	o := deleteOptions{apply: true, timeout: time.Second, artifactoryURL: f.server.URL + "/artifactory", artifactoryPath: "docker-local/team/app"}
	var out bytes.Buffer
	if err := runDelete(t.Context(), &out, f.ref(":bad"), false, o); err != nil {
		t.Fatal(err)
	}
	if len(f.deletes) != 1 || f.deletes[0] != "/artifactory/docker-local/team/app/bad" || f.tags["alias"] != deleteDigestA {
		t.Fatalf("deletes=%v tags=%v", f.deletes, f.tags)
	}
}

func TestDeletionRejectsAmbiguousReferencesAndStoragePaths(t *testing.T) {
	f := newDeletionFixture(t)
	for _, tc := range []struct {
		input    string
		manifest bool
		o        deleteOptions
	}{
		{f.ref(""), false, deleteOptions{}},
		{f.ref("@" + deleteDigestA), false, deleteOptions{}},
		{f.ref(":bad"), true, deleteOptions{}},
		{f.ref(":bad"), false, deleteOptions{artifactoryURL: f.server.URL}},
		{f.ref(":bad"), false, deleteOptions{artifactoryURL: f.server.URL, artifactoryPath: "docker-local/../app"}},
		{f.ref(":bad"), false, deleteOptions{artifactoryURL: "https://another.example/artifactory", artifactoryPath: "docker-local/team/app"}},
		{f.ref(":bad"), false, deleteOptions{artifactoryURL: f.server.URL, artifactoryPath: "docker-local/other/app"}},
	} {
		tc.o.timeout = time.Second
		tc.o.apply = true
		if err := runDelete(t.Context(), &bytes.Buffer{}, tc.input, tc.manifest, tc.o); err == nil {
			t.Errorf("accepted invalid input %+v", tc)
		}
	}
	if len(f.deletes) != 0 {
		t.Fatalf("unexpected deletes=%v", f.deletes)
	}
}

func TestDeleteTagRechecksDigestBeforeDeletion(t *testing.T) {
	f := newDeletionFixture(t)
	f.retargetOnRecheck = true
	var out bytes.Buffer
	err := runDelete(t.Context(), &out, f.ref(":bad"), false, deleteOptions{apply: true, timeout: time.Second})
	if err == nil || !strings.Contains(err.Error(), "changed") || len(f.deletes) != 0 {
		t.Fatalf("error=%v deletes=%v output=%s", err, f.deletes, &out)
	}
}

func TestDeleteTagVerifiesAcceptedDeletion(t *testing.T) {
	f := newDeletionFixture(t)
	f.ignoreDelete = true
	var out bytes.Buffer
	err := runDelete(t.Context(), &out, f.ref(":bad"), false, deleteOptions{apply: true, timeout: time.Second})
	if err == nil || !strings.Contains(err.Error(), "still readable") || strings.Contains(out.String(), "Deleted:") {
		t.Fatalf("error=%v output=%s", err, &out)
	}
}
