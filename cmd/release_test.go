package cmd

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/martinmares/oci-toolbox/internal/bundle"
	"github.com/martinmares/oci-toolbox/internal/kubeimages"
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

func TestLoadReconstructItemsRejectsMixedKubernetesInputs(t *testing.T) {
	_, err := loadReconstructItems(t.Context(), []string{"registry.example.com/release/api:1"}, releaseReconstructOptions{
		namespace: "example",
	})
	if err == nil || !strings.Contains(err.Error(), "--namespace cannot be combined") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadReconstructItemsRequiresNamespaceForKubernetesOptions(t *testing.T) {
	_, err := loadReconstructItems(t.Context(), nil, releaseReconstructOptions{
		imagePrefix: "registry.example.com/team",
	})
	if err == nil || !strings.Contains(err.Error(), "require --namespace") {
		t.Fatalf("error = %v", err)
	}
}

func TestLoadReconstructItemsRequiresNamespaceForExcludeScaledToZero(t *testing.T) {
	_, err := loadReconstructItems(t.Context(), nil, releaseReconstructOptions{excludeScaledToZero: true})
	if err == nil || !strings.Contains(err.Error(), "--exclude-scaled-to-zero require --namespace") {
		t.Fatalf("error = %v", err)
	}
}

func TestFilterDeploymentImagesByRepositoryPrefix(t *testing.T) {
	images := []kubeimages.DeploymentImage{
		{AppName: "api", ContainerName: "api", Image: "registry.example.com/team/api:1"},
		{AppName: "worker", ContainerName: "worker", Image: "registry.example.com/team/worker@sha256:abc"},
		{AppName: "other", ContainerName: "other", Image: "registry.example.com/team-extra/other:1"},
		{AppName: "external", ContainerName: "external", Image: "other.example.com/team/external:1"},
	}
	filtered := filterDeploymentImages(images, normalizeImagePrefix("https://registry.example.com/team/"))
	if len(filtered) != 2 || filtered[0].AppName != "api" || filtered[1].AppName != "worker" {
		t.Fatalf("filtered = %#v", filtered)
	}
}

func TestExcludeScaledToZeroDeploymentImages(t *testing.T) {
	images := []kubeimages.DeploymentImage{
		{AppName: "api", ContainerName: "api", Image: "registry.example.com/api:1"},
		{AppName: "disabled", ContainerName: "application", Image: "registry.example.com/missing:1", ScaledToZero: true},
		{AppName: "worker", ContainerName: "worker", Image: "registry.example.com/worker:1"},
	}
	filtered := excludeScaledToZeroDeploymentImages(images)
	if len(filtered) != 2 || filtered[0].AppName != "api" || filtered[1].AppName != "worker" {
		t.Fatalf("filtered = %#v", filtered)
	}
}

func TestReconstructDeploymentItemsPreservesSelectorsAndRepeatedImages(t *testing.T) {
	const sharedImage = "registry.example.com/team/shared:1"
	items := reconstructDeploymentItems([]kubeimages.DeploymentImage{
		{AppName: "api", ContainerName: "api", Image: sharedImage},
		{AppName: "worker", ContainerName: "metrics", Image: sharedImage},
	})
	want := []bundle.ReconstructImage{
		{Ref: sharedImage, ID: "api", AppName: "api", ContainerName: "api"},
		{Ref: sharedImage, ID: "worker/metrics", AppName: "worker", ContainerName: "metrics"},
	}
	if len(items) != len(want) {
		t.Fatalf("items = %#v", items)
	}
	for i := range want {
		if items[i] != want[i] {
			t.Errorf("items[%d] = %#v, want %#v", i, items[i], want[i])
		}
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

func TestReconstructedTags(t *testing.T) {
	for _, test := range []struct {
		name             string
		explicitTag      string
		releaseID        string
		discovered       []string
		requiredExtra    []string
		discoveryEnabled bool
		wantTag          string
		wantExtra        string
	}{
		{
			name:             "disabled preserves existing behavior",
			explicitTag:      "release-1",
			discovered:       []string{"release-1", "stable"},
			requiredExtra:    []string{"candidate"},
			discoveryEnabled: false,
			wantTag:          "release-1",
			wantExtra:        "candidate",
		},
		{
			name:             "explicit input tag wins",
			explicitTag:      "latest",
			releaseID:        "release-1",
			discovered:       []string{"latest", "release-1", "stable"},
			discoveryEnabled: true,
			wantTag:          "latest",
			wantExtra:        "release-1,stable",
		},
		{
			name:             "release ID wins for digest input",
			releaseID:        "release-1",
			discovered:       []string{"candidate", "release-1", "stable"},
			discoveryEnabled: true,
			wantTag:          "release-1",
			wantExtra:        "candidate,stable",
		},
		{
			name:             "single discovered tag becomes primary",
			releaseID:        "release-1",
			discovered:       []string{"stable"},
			discoveryEnabled: true,
			wantTag:          "stable",
		},
		{
			name:             "ambiguous tags remain aliases",
			releaseID:        "release-1",
			discovered:       []string{"candidate", "stable"},
			requiredExtra:    []string{"supported"},
			discoveryEnabled: true,
			wantExtra:        "supported,candidate,stable",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			tag, extra := reconstructedTags(
				test.explicitTag,
				test.releaseID,
				test.discovered,
				test.requiredExtra,
				test.discoveryEnabled,
			)
			if tag != test.wantTag {
				t.Fatalf("tag = %q, want %q", tag, test.wantTag)
			}
			if got := strings.Join(extra, ","); got != test.wantExtra {
				t.Fatalf("extra tags = %q, want %q", got, test.wantExtra)
			}
		})
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

func TestRunReleaseSelectorsPlain(t *testing.T) {
	manifestPath := writeCompareReleaseManifest(t, "R1", []bundle.ReleaseImage{
		{ID: "worker", AppName: "worker", ContainerName: "application", Image: "registry.example.com/worker", Tag: "R1", Digest: testReleaseDigest("b")},
		{ID: "api", AppName: "api", ContainerName: "application", Image: "registry.example.com/api", Tag: "R1", Digest: testReleaseDigest("a")},
	})
	previous := selectorsOptions
	t.Cleanup(func() { selectorsOptions = previous })
	selectorsOptions.format = "plain"
	cmd := &cobra.Command{}
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)

	if err := runReleaseSelectors(cmd, []string{manifestPath}); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "api/application\nworker/application\n"; got != want {
		t.Fatalf("output = %q, want %q", got, want)
	}
}

func TestRunReleaseCompareJSONChangedOnlyAndFail(t *testing.T) {
	aPath := writeCompareReleaseManifest(t, "R1", []bundle.ReleaseImage{
		{ID: "api", AppName: "api", ContainerName: "application", Image: "registry.example.com/api", Tag: "R1", Digest: testReleaseDigest("a")},
		{ID: "worker", AppName: "worker", ContainerName: "application", Image: "registry.example.com/worker", Tag: "R1", Digest: testReleaseDigest("b")},
	})
	bPath := writeCompareReleaseManifest(t, "R2", []bundle.ReleaseImage{
		{ID: "api", AppName: "api", ContainerName: "application", Image: "registry.example.com/api", Tag: "R2", Digest: testReleaseDigest("c")},
		{ID: "worker", AppName: "worker", ContainerName: "application", Image: "registry.example.com/worker", Tag: "R1", Digest: testReleaseDigest("b")},
	})
	previous := compareOptions
	t.Cleanup(func() { compareOptions = previous })
	compareOptions = releaseCompareOptions{mode: "digest", format: "json", color: "never", changedOnly: true, failOnChange: true}
	cmd := &cobra.Command{}
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)

	err := runReleaseCompare(cmd, []string{aPath, bPath})
	if err == nil || !strings.Contains(err.Error(), "1 changed") {
		t.Fatalf("error = %v", err)
	}
	var report struct {
		Mode      string                    `json:"mode"`
		MatchedBy string                    `json:"matched_by"`
		Match     bool                      `json:"match"`
		Summary   bundle.ReleaseDiffSummary `json:"summary"`
		Images    []bundle.ReleaseDiff      `json:"images"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &report); err != nil {
		t.Fatalf("invalid JSON output: %v\n%s", err, stdout.String())
	}
	if report.Summary.Changed != 1 || report.Summary.Same != 1 {
		t.Fatalf("summary = %#v", report.Summary)
	}
	if report.Mode != "digest" || report.MatchedBy != "app_name/container_name" || report.Match {
		t.Fatalf("comparison metadata = mode %q, matched_by %q, match %v", report.Mode, report.MatchedBy, report.Match)
	}
	if len(report.Images) != 1 || report.Images[0].Selector != "api/application" || report.Images[0].Status != "changed" {
		t.Fatalf("images = %#v", report.Images)
	}
}

func TestRunReleaseCompareSummary(t *testing.T) {
	images := []bundle.ReleaseImage{{
		ID: "api", AppName: "api", ContainerName: "application", Image: "registry.example.com/api", Tag: "R1", Digest: testReleaseDigest("a"),
	}}
	aPath := writeCompareReleaseManifest(t, "R1", images)
	images[0].Tag = "R2"
	bPath := writeCompareReleaseManifest(t, "R2", images)
	previous := compareOptions
	t.Cleanup(func() { compareOptions = previous })
	compareOptions = releaseCompareOptions{mode: "digest", format: "summary", color: "never"}
	cmd := &cobra.Command{}
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)

	if err := runReleaseCompare(cmd, []string{aPath, bPath}); err != nil {
		t.Fatal(err)
	}
	if got, want := stdout.String(), "MATCH: same=1 changed=0 added=0 removed=0\n"; got != want {
		t.Fatalf("summary = %q, want %q", got, want)
	}
}

func TestWriteReleaseComparisonTableColorizesOnlyStatuses(t *testing.T) {
	comparison := bundle.ReleaseComparison{
		Summary: bundle.ReleaseDiffSummary{Changed: 1, Added: 1, Removed: 1},
		Images: []bundle.ReleaseDiff{
			{Status: "added", Selector: "new/app", B: &bundle.ReleaseImageState{Digest: testReleaseDigest("a")}},
			{Status: "changed", Selector: "api/app", A: &bundle.ReleaseImageState{Digest: testReleaseDigest("b")}, B: &bundle.ReleaseImageState{Digest: testReleaseDigest("c")}},
			{Status: "removed", Selector: "old/app", A: &bundle.ReleaseImageState{Digest: testReleaseDigest("d")}},
		},
	}
	cmd := &cobra.Command{}
	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	if err := writeReleaseComparisonTable(cmd, comparison.Images, comparison.Summary, bundle.ReleaseCompareDigest, true); err != nil {
		t.Fatal(err)
	}
	got := stdout.String()
	for _, want := range []string{ansiGreen + "added" + ansiReset, ansiYellow + "changed" + ansiReset, ansiRed + "removed" + ansiReset} {
		if !strings.Contains(got, want) {
			t.Fatalf("colored output does not contain %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, ansiGreen+testReleaseDigest("a")) {
		t.Fatalf("digest must not be colorized:\n%s", got)
	}
}

func writeCompareReleaseManifest(t *testing.T, releaseID string, images []bundle.ReleaseImage) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), releaseID+".yml")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	manifest := &bundle.ReleaseManifest{
		ReleaseID: releaseID,
		CreatedAt: time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC),
		Images:    images,
	}
	if err := bundle.EncodeRelease(f, manifest); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func testReleaseDigest(char string) string {
	return "sha256:" + strings.Repeat(char, 64)
}
