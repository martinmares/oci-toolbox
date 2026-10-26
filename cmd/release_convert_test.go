package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/martinmares/oci-toolbox/internal/bundle"
	"github.com/spf13/cobra"
)

func TestReleaseConvertOfflineAndDoesNotOverwrite(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "v1.yml")
	original := []byte("apiVersion: oci-toolbox/v1\nkind: ImageRelease\nrelease_id: test\nimages:\n  - app_name: api\n    image: example.invalid/team/api\n    digest: sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef\n")
	if err := os.WriteFile(source, original, 0600); err != nil {
		t.Fatal(err)
	}
	previous := releaseConvertOutput
	t.Cleanup(func() { releaseConvertOutput = previous })
	output := filepath.Join(dir, "v2.yml")
	releaseConvertOutput = output
	command := &cobra.Command{}
	if err := releaseConvertCmd.RunE(command, []string{source}); err != nil {
		t.Fatal(err)
	}
	result, err := bundle.LoadRelease(output)
	if err != nil || result.APIVersion != bundle.ReleaseAPIVersion || result.Images[0].Image != "example.invalid/team/api" {
		t.Fatalf("result: %#v, %v", result, err)
	}
	if err := releaseConvertCmd.RunE(command, []string{source}); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("overwrite error = %v", err)
	}
	current, err := os.ReadFile(source)
	if err != nil || !bytes.Equal(current, original) {
		t.Fatal("source was changed")
	}
	releaseConvertOutput = "-"
	var stdout bytes.Buffer
	command.SetOut(&stdout)
	if err := releaseConvertCmd.RunE(command, []string{output}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(stdout.String(), "apiVersion: oci-toolbox/v2\n") {
		t.Fatal(stdout.String())
	}
}
