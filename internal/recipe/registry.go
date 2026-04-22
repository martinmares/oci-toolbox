package recipe

import (
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
)

// stripScheme removes "docker://" prefix that skopeo-style refs use.
func stripScheme(ref string) string {
	ref = strings.TrimPrefix(ref, "docker://")
	ref = strings.TrimPrefix(ref, "oci://")
	return ref
}

// parseRef parses a string into a go-containerregistry Reference.
func parseRef(ref string) (name.Reference, error) {
	return name.ParseReference(ref)
}
