package shadow

import (
	"bytes"
	"testing"
)

func TestCompileExtractorSupportsReleasePlatforms(t *testing.T) {
	tests := []struct {
		os     string
		arch   string
		prefix []byte
	}{
		{os: "linux", arch: "amd64", prefix: []byte{'\x7f', 'E', 'L', 'F'}},
		{os: "linux", arch: "arm64", prefix: []byte{'\x7f', 'E', 'L', 'F'}},
		{os: "darwin", arch: "amd64", prefix: []byte{'\xcf', '\xfa', '\xed', '\xfe'}},
		{os: "darwin", arch: "arm64", prefix: []byte{'\xcf', '\xfa', '\xed', '\xfe'}},
		{os: "windows", arch: "amd64", prefix: []byte{'M', 'Z'}},
		{os: "windows", arch: "arm64", prefix: []byte{'M', 'Z'}},
	}

	for _, tt := range tests {
		t.Run(tt.os+"/"+tt.arch, func(t *testing.T) {
			binary, err := compileExtractor(tt.os, tt.arch)
			if err != nil {
				t.Fatalf("compileExtractor(%q, %q): %v", tt.os, tt.arch, err)
			}
			if !bytes.HasPrefix(binary, tt.prefix) {
				t.Fatalf("extractor prefix = %x, want %x", binary[:min(len(binary), len(tt.prefix))], tt.prefix)
			}
		})
	}
}

func TestCompileExtractorRejectsUnsupportedPlatform(t *testing.T) {
	if _, err := compileExtractor("freebsd", "amd64"); err == nil {
		t.Fatal("compileExtractor() succeeded for an unsupported platform")
	}
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
