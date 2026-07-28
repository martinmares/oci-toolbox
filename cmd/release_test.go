package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/martinmares/oci-toolbox/internal/bundle"
)

func TestReconstructItemsFromArguments(t *testing.T) {
	items, err := reconstructItems([]string{
		"registry.example.com/release/api:release-1",
		"registry.example.com/release/ui:release-1",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 || items[0].Ref != "registry.example.com/release/api:release-1" {
		t.Fatalf("items = %#v", items)
	}
}

func TestReconstructItemsRejectsMixedInputs(t *testing.T) {
	_, err := reconstructItems([]string{"registry.example.com/release/api:release-1"}, "images.yml")
	if err == nil || !strings.Contains(err.Error(), "cannot be combined") {
		t.Fatalf("error = %v", err)
	}
}

func TestHasExplicitImageIdentifier(t *testing.T) {
	for _, test := range []struct {
		ref  string
		want bool
	}{
		{ref: "registry.example.com:5000/team/api", want: false},
		{ref: "registry.example.com:5000/team/api:release-1", want: true},
		{ref: "registry.example.com/team/api@sha256:0123456789abcdef", want: true},
	} {
		if got := hasExplicitImageIdentifier(test.ref); got != test.want {
			t.Errorf("hasExplicitImageIdentifier(%q) = %v, want %v", test.ref, got, test.want)
		}
	}
}

func TestWriteReleaseManifest(t *testing.T) {
	output := filepath.Join(t.TempDir(), "releases", "release-1.yml")
	manifest := &bundle.ReleaseManifest{
		ReleaseID: "release-1",
		CreatedAt: time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC),
		Images: []bundle.ReleaseImage{
			{
				AppName:       "api",
				ContainerName: "api",
				Image:         "registry.example.com/release/api",
				Tag:           "release-1",
				Digest:        "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			},
		},
	}
	cmd := &cobra.Command{}
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)

	if err := writeReleaseManifest(cmd, output, manifest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "release_id: release-1") {
		t.Fatalf("manifest = %s", data)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(output)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o644 {
			t.Fatalf("mode = %o", got)
		}
	}

	err = writeReleaseManifest(cmd, output, manifest)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("second write error = %v", err)
	}
}
