package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/martinmares/oci-toolbox/internal/bundle"
	"github.com/spf13/cobra"
)

func TestCompareReleaseToDeployments(t *testing.T) {
	manifest := &bundle.ReleaseManifest{ReleaseID: "R2", Images: []bundle.ReleaseImage{
		{ID: "api", AppName: "api", ContainerName: "application", Image: "registry.example.com/api", Tag: "R2", Digest: verifyDigest("a"), Platform: "linux/amd64"},
		{ID: "worker", AppName: "worker", ContainerName: "application", Image: "registry.example.com/worker", Tag: "R2", Digest: verifyDigest("b"), Platform: "linux/amd64"},
		{ID: "missing", AppName: "missing", ContainerName: "application", Image: "registry.example.com/missing", Tag: "R2", Digest: verifyDigest("c")},
		{ID: "broken", AppName: "broken", ContainerName: "application", Image: "registry.example.com/broken", Tag: "R2", Digest: verifyDigest("f")},
	}}
	actual := map[string]resolvedDeploymentImage{
		"api\x00application":    {state: &bundle.ReleaseImageState{Image: "registry.example.com/api", Tag: "other-tag", Digest: verifyDigest("a"), Platform: "linux/amd64"}},
		"worker\x00application": {state: &bundle.ReleaseImageState{Image: "registry.example.com/worker", Tag: "R2", Digest: verifyDigest("d"), Platform: "linux/amd64"}},
		"broken\x00application": {err: errors.New("registry unavailable")},
		"extra\x00sidecar":      {state: &bundle.ReleaseImageState{Image: "registry.example.com/extra", Digest: verifyDigest("e")}},
	}

	report := compareReleaseToDeployments("release.yml", "example", manifest, actual)
	if report.Summary != (releaseVerifySummary{Verified: 1, Drift: 3, Errors: 1}) {
		t.Fatalf("summary = %#v", report.Summary)
	}
	bySelector := make(map[string]releaseVerifyItem, len(report.Images))
	for _, item := range report.Images {
		bySelector[item.Selector] = item
	}
	if item := bySelector["api/application"]; item.Status != "verified" || !item.TagChanged {
		t.Fatalf("tag-only change should verify content: %#v", item)
	}
	if item := bySelector["worker/application"]; item.Status != "drift" || strings.Join(item.ChangedFields, ",") != "digest" {
		t.Fatalf("digest drift = %#v", item)
	}
	if item := bySelector["missing/application"]; item.Status != "drift" || strings.Join(item.ChangedFields, ",") != "missing_selector" {
		t.Fatalf("missing selector = %#v", item)
	}
	if item := bySelector["extra/sidecar"]; item.Status != "drift" || strings.Join(item.ChangedFields, ",") != "unexpected_selector" {
		t.Fatalf("unexpected selector = %#v", item)
	}
	if item := bySelector["broken/application"]; item.Status != "error" || !strings.Contains(item.Error, "registry unavailable") {
		t.Fatalf("resolution error = %#v", item)
	}
}

func TestVerifyChangedFieldsIgnoresUnspecifiedExpectedPlatform(t *testing.T) {
	expected := &bundle.ReleaseImageState{Image: "registry/api", Digest: verifyDigest("a")}
	actual := &bundle.ReleaseImageState{Image: "registry/api", Digest: verifyDigest("a"), Platform: "linux/arm64"}
	if fields := verifyChangedFields(expected, actual); len(fields) != 0 {
		t.Fatalf("fields = %#v", fields)
	}
}

func TestReleaseVerifyExposesProgressFlag(t *testing.T) {
	if releaseVerifyCmd.Flags().Lookup("progress") == nil {
		t.Fatal("release verify does not expose --progress")
	}
}

func TestWriteReleaseVerifyTableUsesCompactDetails(t *testing.T) {
	report := releaseVerifyReport{
		Summary: releaseVerifySummary{Verified: 1, Drift: 1, Errors: 1},
		Images: []releaseVerifyItem{
			{Status: "verified", Selector: "api/application", Expected: &bundle.ReleaseImageState{Image: "very-long.example.com/team/api", Digest: verifyDigest("a")}, Actual: &bundle.ReleaseImageState{Image: "very-long.example.com/team/api", Digest: verifyDigest("a")}},
			{Status: "drift", Selector: "worker/application", ChangedFields: []string{"digest"}},
			{Status: "error", Selector: "broken/application", Error: "manifest not found"},
		},
	}
	var output bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&output)
	if err := writeReleaseVerifyTable(cmd, report); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, want := range []string{"STATUS", "SELECTOR", "DETAILS", "worker/application", "digest", "manifest not found", "verified=1 drift=1 errors=1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("table does not contain %q:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"EXPECTED", "ACTUAL", "very-long.example.com", verifyDigest("a")} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("compact table unexpectedly contains %q:\n%s", unwanted, got)
		}
	}
}

func verifyDigest(char string) string {
	return "sha256:" + strings.Repeat(char, 64)
}
