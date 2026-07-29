package registry

import (
	"errors"
	"testing"
)

func TestParseBearerChallenge(t *testing.T) {
	challenge, ok := parseBearerChallenge(`Bearer realm="https://auth.example.com/token",service="registry.example.com",scope="repository:team/app:pull"`)
	if !ok {
		t.Fatal("challenge was not parsed")
	}
	if challenge.realm != "https://auth.example.com/token" ||
		challenge.service != "registry.example.com" ||
		challenge.scope != "repository:team/app:pull" {
		t.Fatalf("challenge = %#v", challenge)
	}
}

func TestNextLinkPathPreservesPaginationQuery(t *testing.T) {
	got := nextLinkPath(`</v2/_catalog?n=1000&last=team%2Fapp>; rel="next"`)
	if got != "/v2/_catalog?n=1000&last=team%2Fapp" {
		t.Fatalf("nextLinkPath = %q", got)
	}
}

func TestIsNameUnknown(t *testing.T) {
	err := NameUnknownError{Repository: "missing", Status: "404 Not Found"}
	if !IsNameUnknown(fmtWrapped(err)) {
		t.Fatal("wrapped NameUnknownError was not recognized")
	}
	if IsNameUnknown(errors.New("other")) {
		t.Fatal("unrelated error was recognized as NameUnknownError")
	}
}

func fmtWrapped(err error) error {
	return &wrappedError{err: err}
}

type wrappedError struct{ err error }

func (e *wrappedError) Error() string { return "wrapped: " + e.err.Error() }
func (e *wrappedError) Unwrap() error { return e.err }
