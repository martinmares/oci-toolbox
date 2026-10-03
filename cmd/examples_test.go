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
		"OCI-TOOLBOX COMMAND EXAMPLES",
		"Commands are marked SHELL; external files are marked FILE.",
		"## COPY",
		"## DELETE-TAG",
		"## DELETE-MANIFEST",
		"--expect-digest sha256:",
		"--artifactory-path docker-local/team/app",
		"  › Batch copy",
		"    FILE · images.yml (YAML)",
		"        copy:",
		"    SHELL",
		"      oci-toolbox copy --batch-file images.yml \\",
		"      --release-id RE_2026.09.10.02 \\",
		"## BUNDLE VALIDATE",
		"      oci-toolbox bundle validate bundle.yml",
		"## BUNDLE PLAN",
		"## BUNDLE PUBLISH",
		"      --preserve-base-only \\",
		"## RELEASE RECONSTRUCT",
		"## RELEASE TO-BUNDLE",
		"      --source-mode tag \\",
		"## RELEASE SELECTORS",
		"## RELEASE COMPARE",
		"## RELEASE VERIFY",
		"## REGISTRY INFO",
		"    FILE · oci-toolbox-registry.yml (YAML)",
		"## REGISTRY COPY-PLAN",
		"## REGISTRY LIST-REPOSITORIES",
		"## REGISTRY CLEANUP",
		"## SNAPSHOT CREATE",
		"## SNAPSHOT RESTORE",
		"## SPRING-CONFIG DISCOVER",
		"manifest.svg",
		"## TAG-EXISTING",
		"## INSPECT",
		"## TREE",
		"## LS",
		"## CAT",
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

	fileLabel := strings.Index(got, "FILE · images.yml (YAML)")
	fileContent := strings.Index(got, "        copy:")
	shellLabel := strings.Index(got[fileContent:], "SHELL")
	if fileLabel < 0 || fileContent < fileLabel || shellLabel < 0 {
		t.Fatalf("batch example does not clearly separate file and shell blocks")
	}
}

func TestWriteExamplesColor(t *testing.T) {
	var output bytes.Buffer
	if err := writeExamples(&output, true); err != nil {
		t.Fatalf("writeExamples() error = %v", err)
	}

	got := output.String()
	for _, want := range []string{
		ansiBold + ansiCyan + "OCI-TOOLBOX COMMAND EXAMPLES" + ansiReset,
		ansiBold + ansiCyan + "## COPY" + ansiReset,
		ansiBold + ansiYellow + "› Batch copy" + ansiReset,
		ansiBold + ansiGreen + "SHELL" + ansiReset,
		ansiGreen + "      oci-toolbox copy --batch-file images.yml \\" + ansiReset,
		ansiBold + ansiBlue + "FILE · images.yml (YAML)" + ansiReset,
		ansiBlue + "        copy:" + ansiReset,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("color output does not contain %q", want)
		}
	}
}

func TestExamplesCatalogHasUniqueCommandsAndCompleteEntries(t *testing.T) {
	seen := make(map[string]struct{}, len(examplesCatalog))
	for _, command := range examplesCatalog {
		if command.Command == "" || command.Description == "" || len(command.Examples) == 0 {
			t.Fatalf("incomplete command section: %#v", command)
		}
		if _, exists := seen[command.Command]; exists {
			t.Fatalf("duplicate command section %q", command.Command)
		}
		seen[command.Command] = struct{}{}
		for _, example := range command.Examples {
			if example.Title == "" || (example.Shell == "" && len(example.Files) == 0) {
				t.Fatalf("incomplete example in %q: %#v", command.Command, example)
			}
		}
	}
}
