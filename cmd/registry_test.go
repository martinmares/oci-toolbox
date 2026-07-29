package cmd

import (
	"strings"
	"testing"
)

func TestRenderRegistryCopyPlanUsesToolboxCommand(t *testing.T) {
	output, err := renderCopyPlan(copyBatch{Copy: []copyBatchItem{{
		Src:      "source.example.com/team/app:1",
		Dest:     "target.example.com/team/app:1",
		DestTags: []string{"latest"},
	}}}, copyPlanOptions{Format: "commands"})
	if err != nil {
		t.Fatalf("renderCopyPlan: %v", err)
	}
	for _, expected := range []string{
		"oci-toolbox copy",
		"--dest-tag latest",
		"source.example.com/team/app:1",
		"target.example.com/team/app:1",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("output does not contain %q: %s", expected, output)
		}
	}
}

func TestRegistryCleanupRequiresExplicitApply(t *testing.T) {
	registryCmd := newRegistryCommand()
	cleanupCmd, _, err := registryCmd.Find([]string{"cleanup"})
	if err != nil {
		t.Fatalf("find cleanup: %v", err)
	}
	apply := cleanupCmd.Flags().Lookup("apply")
	if apply == nil {
		t.Fatal("cleanup has no --apply flag")
	}
	if apply.DefValue != "false" {
		t.Fatalf("--apply default = %q, want false", apply.DefValue)
	}
	if cleanupCmd.Flags().Lookup("no-dry-run") != nil {
		t.Fatal("legacy --no-dry-run flag is still exposed")
	}
}
