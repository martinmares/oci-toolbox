package cmd

import "testing"

func TestTarballTagAcceptsDigestSource(t *testing.T) {
	tag, err := tarballTag("registry.example.com/team/app@sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("tarballTag: %v", err)
	}
	if got, want := tag.String(), "registry.example.com/team/app:snapshot"; got != want {
		t.Fatalf("tag = %q, want %q", got, want)
	}
}
