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
		"oci-patch copy --batch-file images.yml --src-insecure --dest-insecure",
		"# images.yml",
		"  - src: registry.example.com/team/config:release-1.0",
		"    dest: registry.example.com/team/ticket:test-latest",
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
	for _, want := range []string{ansiBold + ansiCyan + "Quick copy", ansiGreen + "oci-patch copy", ansiYellow + "src:"} {
		if !strings.Contains(got, want) {
			t.Errorf("color output does not contain %q", want)
		}
	}
}
