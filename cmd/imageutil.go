package cmd

import (
	"fmt"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"

	"github.com/martinmares/oci-toolbox/internal/auth"
)

func loadImage(refArg, creds string, insecure bool, os, arch, variant string) (v1.Image, error) {
	baseOpts := auth.RemoteOptions(parseCreds(creds))
	if insecure {
		baseOpts = append(baseOpts, auth.InsecureTransport())
	}

	rawRef := stripScheme(refArg)
	ref, err := name.ParseReference(rawRef)
	if err != nil {
		return nil, fmt.Errorf("invalid image reference: %w", err)
	}

	opts := baseOpts
	if os != "" || arch != "" || variant != "" {
		opts = append(opts, platformOption(os, arch, variant))
	}

	img, err := remote.Image(ref, opts...)
	if err != nil {
		return nil, fmt.Errorf("fetching image: %w", err)
	}
	return img, nil
}
