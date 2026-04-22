package auth

import (
	"crypto/tls"
	"net/http"

	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// Options holds credential configuration for a registry.
type Options struct {
	Username string
	Password string
	AuthFile string // path to docker config.json
}

// RemoteOptions returns remote.Option slice for go-containerregistry.
func RemoteOptions(creds *Options) []remote.Option {
	if creds != nil && creds.Username != "" && creds.Password != "" {
		auth := &authn.Basic{
			Username: creds.Username,
			Password: creds.Password,
		}
		return []remote.Option{remote.WithAuth(auth)}
	}

	// Fall back to ~/.docker/config.json and credential helpers.
	return []remote.Option{remote.WithAuthFromKeychain(authn.DefaultKeychain)}
}

// InsecureTransport returns a remote.Option that skips TLS certificate verification.
func InsecureTransport() remote.Option {
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
	}
	return remote.WithTransport(transport)
}
