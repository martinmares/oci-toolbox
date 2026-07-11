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
		"  - src: harbor.cetin/tsm/release/tsm-config:TSM-Core_RE60_BF01_01.01",
		"    dest: harbor.cetin/tsm/release/tsm-ticket:2.2-test-latest",
		"--reduce-changes",
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
