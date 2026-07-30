package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteExamplesPlain(t *testing.T) {
	var output bytes.Buffer
	if err := writeExamples(&output, false); err != nil {
		t.Fatalf("writeExamples() error = %v", err)
	}

	got := output.String()
	for _, want := range []string{
		"Batch copy",
		"oci-toolbox copy --batch-file images.yml --src-insecure --dest-insecure",
		"# images.yml",
		"  - src: registry.example.com/team/config:release-1.0",
		"    dest: registry.example.com/team/ticket:test-latest",
		"Copy a release manifest",
		"--release-manifest releases/2026.06.25.01.yml",
		"--dest-prefix artifactory.example.com/nac-test",
		"Publish an image bundle",
		"oci-toolbox bundle publish bundle.yml",
		"Reconstruct a release manifest",
		"oci-toolbox release reconstruct",
		"Inspect and clean a registry",
		"oci-toolbox registry cleanup",
		"Snapshot Kubernetes deployment images",
		"oci-toolbox snapshot create",
		"--reduce-changes",
		"action: use-base",
		"action: shadow",
		"action: replace-file",
		"action: extract-file",
		"action: unzip-to",
		"action: set-cmd",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output does not contain %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b[") {
		t.Errorf("plain output contains ANSI escape sequence: %q", got)
	}
}

func TestWriteExamplesColor(t *testing.T) {
	var output bytes.Buffer
	if err := writeExamples(&output, true); err != nil {
		t.Fatalf("writeExamples() error = %v", err)
	}

	got := output.String()
	for _, want := range []string{ansiBold + ansiCyan + "Quick copy", ansiGreen + "oci-toolbox copy", ansiYellow + "src:"} {
		if !strings.Contains(got, want) {
			t.Errorf("color output does not contain %q", want)
		}
	}
	for _, want := range []string{
		ansiGreen + "  --release-id RE_2026.07.28.01 \\" + ansiReset,
		ansiGreen + "  ./snapshots/example/20260728-120000.yml" + ansiReset,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("continued command line is not green: missing %q", want)
		}
	}
}
