package progress

import (
	"bytes"
	"strings"
	"testing"
)

func TestCounterShowsCountAndCompletes(t *testing.T) {
	var output bytes.Buffer
	counter := NewCounter(2, "inspect images", &output)
	counter.Describe("inspect 1/2: registry.example.com/team/api:latest")
	if err := counter.Advance(); err != nil {
		t.Fatal(err)
	}
	if err := counter.Finish(); err != nil {
		t.Fatal(err)
	}

	got := output.String()
	for _, expected := range []string{"100%", "2/2", "images"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("output does not contain %q: %q", expected, got)
		}
	}
}
