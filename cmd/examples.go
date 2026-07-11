package cmd

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

var examplesCmd = &cobra.Command{
	Use:     "examples",
	Aliases: []string{"example"},
	Short:   "Show common commands and YAML file examples",
	Args:    cobra.NoArgs,
	RunE: func(c *cobra.Command, _ []string) error {
		return writeExamples(c.OutOrStdout(), isTTYWriter(c.OutOrStdout()))
	},
}

func init() {
	rootCmd.AddCommand(examplesCmd)
}

func writeExamples(w io.Writer, color bool) error {
	section(w, "Quick copy", color)
	codeBlock(w, `# Copy an image unchanged.
oci-patch copy docker://registry.example.com/team/app:1.2 docker://registry.example.com/team/app:1.2-copy`, color)

	section(w, "Batch copy", color)
	codeBlock(w, `# Copy every image listed in images.yml. Both registries use an insecure TLS connection.
oci-patch copy --batch-file images.yml --src-insecure --dest-insecure`, color)
	fmt.Fprintln(w)
	codeBlock(w, `# images.yml
copy:
  - src: harbor.cetin/tsm/release/tsm-config:TSM-Core_RE60_BF01_01.01
    dest: harbor.cetin/tsm/release/tsm-config:2.2-test-latest
  - src: harbor.cetin/tsm/release/tsm-ticket:TSM-Core_RE60_BF01_01.01
    dest: harbor.cetin/tsm/release/tsm-ticket:2.2-test-latest`, color)

	section(w, "Patch with a recipe", color)
	codeBlock(w, `# Apply app.yml while copying. Keep base-image layers and create one layer for recipe changes.
oci-patch copy --recipe app.yml --reduce-changes docker://registry.example.com/team/app:1.2 docker://registry.example.com/team/app:patched

# app.yml
steps:
  - action: add-file
    src: ./target/app.jar
    dst: /app/app.jar
  - action: fix-permissions
    path: /app`, color)

	section(w, "Inspect image contents", color)
	codeBlock(w, `oci-patch inspect docker://registry.example.com/team/app:latest
oci-patch tree docker://registry.example.com/team/app:latest /app
oci-patch cat docker://registry.example.com/team/app:latest /app/app.jar`, color)

	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run 'oci-patch <command> --help' for all flags. Full reference: README.md")
	return nil
}

func section(w io.Writer, title string, color bool) {
	if color {
		fmt.Fprintf(w, "%s%s%s\n", ansiBold+ansiCyan, title, ansiReset)
		return
	}
	fmt.Fprintln(w, title)
}

func codeBlock(w io.Writer, block string, color bool) {
	for _, line := range strings.Split(block, "\n") {
		fmt.Fprintln(w, highlightExampleLine(line, color))
	}
}

func highlightExampleLine(line string, color bool) string {
	if !color || line == "" {
		return line
	}
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "#") {
		return ansiBlue + line + ansiReset
	}
	if strings.HasPrefix(trimmed, "oci-patch ") {
		return ansiGreen + line + ansiReset
	}
	if key, value, ok := strings.Cut(trimmed, ": "); ok {
		indent := line[:len(line)-len(trimmed)]
		return indent + ansiYellow + key + ":" + ansiReset + " " + value
	}
	if strings.HasSuffix(trimmed, ":") {
		indent := line[:len(line)-len(trimmed)]
		return indent + ansiYellow + trimmed + ansiReset
	}
	return line
}
