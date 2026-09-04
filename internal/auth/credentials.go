package auth

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"net/url"
	"strings"

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
	option, err := Transport("", true)
	if err != nil {
		panic(err)
	}
	return option
}

// Transport returns a registry transport option with independent proxy and TLS
// settings. An empty proxy preserves ProxyFromEnvironment; "direct" disables
// proxying even when HTTP_PROXY or HTTPS_PROXY is set.
func Transport(proxy string, insecure bool) (remote.Option, error) {
	transport, err := newTransport(proxy, insecure)
	if err != nil {
		return nil, err
	}
	return remote.WithTransport(transport), nil
}

func newTransport(proxy string, insecure bool) (*http.Transport, error) {
	base, ok := remote.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("unsupported registry transport type %T", remote.DefaultTransport)
	}
	transport := base.Clone()

	proxy = strings.TrimSpace(proxy)
	switch {
	case proxy == "":
		// Keep the default ProxyFromEnvironment behavior.
	case strings.EqualFold(proxy, "direct"):
		transport.Proxy = nil
	default:
		proxyURL, err := url.Parse(proxy)
		if err != nil || proxyURL.Scheme == "" || proxyURL.Host == "" {
			return nil, fmt.Errorf("invalid proxy URL: expected http://HOST:PORT, https://HOST:PORT, or direct")
		}
		if proxyURL.Scheme != "http" && proxyURL.Scheme != "https" {
			return nil, fmt.Errorf("invalid proxy URL: scheme must be http or https")
		}
		if (proxyURL.Path != "" && proxyURL.Path != "/") || proxyURL.RawQuery != "" || proxyURL.Fragment != "" {
			return nil, fmt.Errorf("invalid proxy URL: path, query, and fragment are not supported")
		}
		if proxyURL.Scheme == "http" && proxyURL.User != nil {
			return nil, fmt.Errorf("proxy credentials over plain HTTP are not allowed; use an HTTPS proxy or remove credentials")
		}
		transport.Proxy = http.ProxyURL(proxyURL)
	}

	if insecure {
		tlsConfig := transport.TLSClientConfig
		if tlsConfig == nil {
			tlsConfig = &tls.Config{}
		} else {
			tlsConfig = tlsConfig.Clone()
		}
		tlsConfig.InsecureSkipVerify = true //nolint:gosec
		transport.TLSClientConfig = tlsConfig
	}
	return transport, nil
}

// RemoteOptionsWithTransport combines registry authentication, proxy routing,
// and TLS verification without losing go-containerregistry transport defaults.
func RemoteOptionsWithTransport(creds *Options, proxy string, insecure bool) ([]remote.Option, error) {
	opts := RemoteOptions(creds)
	if strings.TrimSpace(proxy) == "" && !insecure {
		return opts, nil
	}
	transport, err := Transport(proxy, insecure)
	if err != nil {
		return nil, err
	}
	return append(opts, transport), nil
}
