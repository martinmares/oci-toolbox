package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/martinmares/oci-toolbox/internal/bundle"
)

func TestReleaseConvertCommandAndProductCopy(t *testing.T) {
	const digest = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	raw := "apiVersion: release.tsm.datalite.cz/v1alpha1\nkind: TsmProductRelease\nmetadata: {name: product-1}\nspec:\n  containers:\n    api:\n      image: registry.example.com/team/api\n      tag: original-1\n      digest: " + digest + "\n"
	input := filepath.Join(t.TempDir(), "product.yml")
	if err := os.WriteFile(input, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	cmd := newReleaseConvertCommand()
	cmd.SetOut(&stdout)
	cmd.SetArgs([]string{input, "--release-id", "RE_2026.10.02.01", "--selector", "api=service/main"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	m, err := bundle.DecodeRelease(strings.NewReader(stdout.String()))
	if err != nil {
		t.Fatal(err)
	}
	if m.ReleaseID != "RE_2026.10.02.01" || m.Images[0].AppName != "service" || m.Images[0].ContainerName != "main" || m.Images[0].Tag != "original-1" {
		t.Fatalf("converted = %#v", m)
	}
	plan, err := loadReleaseCopyPlan(input, "", "mirror.example.com/team", "customer-1")
	if err != nil {
		t.Fatal(err)
	}
	if plan == nil {
		t.Fatal("no copy plan")
	}
	output := filepath.Join(t.TempDir(), "output.yml")
	if err := os.WriteFile(output, []byte("existing"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd = newReleaseConvertCommand()
	cmd.SetArgs([]string{input, "--output", output})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("overwrite error = %v", err)
	}
	unchanged, _ := os.ReadFile(output)
	if string(unchanged) != "existing" {
		t.Fatal("overwrote existing output")
	}
	cmd = newReleaseConvertCommand()
	cmd.SetArgs([]string{input, "--selector", "missing=app/main"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("unknown selector accepted")
	}
}
