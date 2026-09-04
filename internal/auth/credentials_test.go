package auth

import (
	"net/http"
	"strings"
	"testing"
)

func TestTransportAcceptsDirectionalProxyValues(t *testing.T) {
	for _, value := range []string{"", "direct", "DIRECT", "http://proxy.example.com:8080", "https://proxy.example.com:8443"} {
		t.Run(value, func(t *testing.T) {
			if _, err := Transport(value, true); err != nil {
				t.Fatalf("Transport(%q): %v", value, err)
			}
		})
	}
}

func TestNewTransportProxyRouting(t *testing.T) {
	proxied, err := newTransport("http://proxy.example.com:8080", true)
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodGet, "https://registry.example.com/v2/", nil)
	if err != nil {
		t.Fatal(err)
	}
	proxyURL, err := proxied.Proxy(request)
	if err != nil {
		t.Fatal(err)
	}
	if proxyURL == nil || proxyURL.String() != "http://proxy.example.com:8080" {
		t.Fatalf("proxy URL = %v", proxyURL)
	}
	if proxied.TLSClientConfig == nil || !proxied.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("insecure TLS setting was not preserved with proxy")
	}

	direct, err := newTransport("direct", false)
	if err != nil {
		t.Fatal(err)
	}
	if direct.Proxy != nil {
		t.Fatal("direct transport still has a proxy function")
	}
}

func TestTransportRejectsInvalidProxy(t *testing.T) {
	for _, test := range []struct {
		value string
		want  string
	}{
		{"proxy.example.com:8080", "expected http://HOST:PORT"},
		{"ftp://proxy.example.com", "scheme must be http or https"},
		{"http://proxy.example.com/path", "path, query, and fragment"},
		{"http://user:password@proxy.example.com:8080", "credentials over plain HTTP"},
	} {
		if _, err := Transport(test.value, false); err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("Transport(%q) error = %v, want %q", test.value, err, test.want)
		}
	}
}
