package cmd

import (
	"strings"
	"testing"

	"github.com/martinmares/oci-toolbox/internal/recipe"
)

func TestSetShadowEntrypointWrapping(t *testing.T) {
	disabled := false
	r := &recipe.Recipe{Steps: []recipe.Step{
		{Action: "add-file"},
		{Action: "shadow", ShadowWrapEntrypoint: &disabled},
		{Action: "shadow"},
	}}

	setShadowEntrypointWrapping(r, true)

	if r.Steps[0].ShadowWrapEntrypoint != nil {
		t.Fatal("non-shadow step was modified")
	}
	for _, index := range []int{1, 2} {
		if r.Steps[index].ShadowWrapEntrypoint == nil || !*r.Steps[index].ShadowWrapEntrypoint {
			t.Fatalf("shadow step %d did not enable entrypoint wrapping", index)
		}
	}
}

func TestValidateShadowEntrypointFlags(t *testing.T) {
	if err := validateShadowEntrypointFlags(true, false); err != nil {
		t.Fatalf("wrap only: %v", err)
	}
	if err := validateShadowEntrypointFlags(false, true); err != nil {
		t.Fatalf("no-wrap only: %v", err)
	}
	if err := validateShadowEntrypointFlags(true, true); err == nil || !strings.Contains(err.Error(), "cannot be used together") {
		t.Fatalf("both flags error = %v", err)
	}
}
