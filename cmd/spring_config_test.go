package cmd

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunSpringConfigDiscoverRequiresOutput(t *testing.T) {
	err := runSpringConfigDiscover(
		springConfigCmd,
		"docker://registry.example.com/team/app:latest",
		springConfigDiscoverOptions{},
	)
	if err == nil || !strings.Contains(err.Error(), "--output is required") {
		t.Fatalf("error = %v, want required output", err)
	}
}

func TestSpringConfigDiscoverHelp(t *testing.T) {
	command, _, err := rootCmd.Find([]string{"spring-config", "discover"})
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	command.SetOut(&output)
	if err := command.Help(); err != nil {
		t.Fatal(err)
	}
	got := output.String()
	for _, want := range []string{
		"spring-config discover IMAGE",
		"--classpath-file",
		"--main-class-file",
		"--include",
		"--output",
		"--progress",
		"--src-insecure",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("help does not contain %q:\n%s", want, got)
		}
	}
}
