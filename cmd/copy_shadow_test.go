package cmd

import (
	"testing"

	"github.com/martinmares/oci-patch/internal/recipe"
)

func TestDisableShadowEntrypointWrapping(t *testing.T) {
	enabled := true
	r := &recipe.Recipe{Steps: []recipe.Step{
		{Action: "add-file"},
		{Action: "shadow", ShadowWrapEntrypoint: &enabled},
		{Action: "shadow"},
	}}

	disableShadowEntrypointWrapping(r)

	if r.Steps[0].ShadowWrapEntrypoint != nil {
		t.Fatal("non-shadow step was modified")
	}
	for _, index := range []int{1, 2} {
		if r.Steps[index].ShadowWrapEntrypoint == nil || *r.Steps[index].ShadowWrapEntrypoint {
			t.Fatalf("shadow step %d did not disable entrypoint wrapping", index)
		}
	}
}
