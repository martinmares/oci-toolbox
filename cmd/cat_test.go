package cmd

import (
	"bytes"
	"testing"

	"github.com/datalite/oci-patch/internal/testutil"
)

func TestCatImageFileWritesFile(t *testing.T) {
	img := testutil.NewImageWithFiles(t, map[string][]byte{
		"app/resources/git.properties": []byte("git.commit.id=abc123\n"),
	})

	var out bytes.Buffer
	if err := catImageFile(&out, img, "/app/resources/git.properties", false); err != nil {
		t.Fatalf("catImageFile: %v", err)
	}
	if out.String() != "git.commit.id=abc123\n" {
		t.Fatalf("output = %q, want file content", out.String())
	}
}

func TestCatImageFileMissingIsSilent(t *testing.T) {
	img := testutil.NewImage(t)

	var out bytes.Buffer
	if err := catImageFile(&out, img, "/app/resources/git.properties", false); err != nil {
		t.Fatalf("catImageFile: %v", err)
	}
	if out.Len() != 0 {
		t.Fatalf("output length = %d, want 0", out.Len())
	}
}
