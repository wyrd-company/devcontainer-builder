package oci

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/registryauth"
)

// errCredentialSource is the synthetic error of a credential source that
// cannot be read, so that the client falls back to anonymous access.
var errCredentialSource = errors.New("synthetic credential source failure")

// anonymousRegistry challenges a request without a token with Bearer,
// grants "anon-token" without credentials and "user-token" for Basic
// testuser:testpassword, and answers token-bearing requests with
// answer(token, path).
type anonymousRegistry struct {
	srv    *httptest.Server
	answer func(token, path string) int
}

func newAnonymousRegistry(t *testing.T, answer func(token, path string) int) *anonymousRegistry {
	t.Helper()
	reg := &anonymousRegistry{answer: answer}
	reg.srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			token := "anon-token"
			if user, pass, ok := r.BasicAuth(); ok && user == "testuser" && pass == "testpassword" {
				token = "user-token"
			}
			_, _ = io.WriteString(w, `{"token":"`+token+`"}`)
			return
		}
		token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		code := http.StatusUnauthorized
		if token != "" {
			code = reg.answer(token, r.URL.Path)
		}
		if code == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", `Bearer realm="`+reg.srv.URL+`/token",service="sample"`)
		}
		if code == http.StatusOK && strings.Contains(r.URL.Path, "/manifests/") {
			w.Header().Set("Content-Type", mediaTypeOCIManifest)
			_, _ = io.WriteString(w, testManifest)
			return
		}
		w.WriteHeader(code)
	}))
	t.Cleanup(reg.srv.Close)
	return reg
}

func (reg *anonymousRegistry) client(auth CredentialSource) (*Client, Reference) {
	c := NewClient()
	c.HTTP = reg.srv.Client()
	c.Auth = auth
	return c, Reference{Registry: strings.TrimPrefix(reg.srv.URL, "https://"), Repository: "sample/private", Tag: "1"}
}

// A registry that refuses an anonymous retry with any status other than
// 401 keeps the credential source's error in the chain, and the message
// stays the one the status alone gives.
func TestFetchManifest_AnonymousRefusalKeepsTheCredentialError(t *testing.T) {
	for _, code := range []int{http.StatusForbidden, http.StatusNotFound, http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			reg := newAnonymousRegistry(t, func(string, string) int { return code })
			c, ref := reg.client(&staticAuth{err: errCredentialSource})
			_, _, err := c.FetchManifest(ref)
			if want := "manifest " + ref.String() + ": " + statusText(code); err == nil || err.Error() != want {
				t.Fatalf("FetchManifest: err = %v, want %q", err, want)
			}
			if !errors.Is(err, errCredentialSource) {
				t.Fatalf("FetchManifest: err = %v, want it to wrap the credential source's error", err)
			}
		})
	}
}

// The 401 refusal of an anonymous retry keeps its existing message.
func TestFetchManifest_AnonymousUnauthorizedKeepsItsMessage(t *testing.T) {
	reg := newAnonymousRegistry(t, func(string, string) int { return http.StatusUnauthorized })
	c, ref := reg.client(&staticAuth{err: errCredentialSource})
	_, _, err := c.FetchManifest(ref)
	want := "manifest " + ref.String() + ": registry " + ref.Registry + " refused anonymous access (" + errCredentialSource.Error() + ")"
	if err == nil || err.Error() != want || !errors.Is(err, errCredentialSource) {
		t.Fatalf("FetchManifest: err = %v, want %q wrapping the credential source's error", err, want)
	}
}

// A blob refused under the anonymous token that the manifest request
// obtained also keeps the credential source's error.
func TestFetchBlob_AnonymousRefusalKeepsTheCredentialError(t *testing.T) {
	reg := newAnonymousRegistry(t, func(_, path string) int {
		if strings.Contains(path, "/blobs/") {
			return http.StatusForbidden
		}
		return http.StatusOK
	})
	c, ref := reg.client(&staticAuth{err: errCredentialSource})
	m, _, err := c.FetchManifest(ref)
	if err != nil {
		t.Fatalf("FetchManifest: %v", err)
	}
	err = c.FetchBlob(ref, m.Config.Digest, io.Discard)
	if want := "blob of " + ref.String() + ": " + statusText(http.StatusForbidden); err == nil || err.Error() != want {
		t.Fatalf("FetchBlob: err = %v, want %q", err, want)
	}
	if !errors.Is(err, errCredentialSource) {
		t.Fatalf("FetchBlob: err = %v, want it to wrap the credential source's error", err)
	}
}

// A refusal with no credential error is the status alone.
func TestFetchManifest_RefusalWithoutCredentialErrorIsTheStatus(t *testing.T) {
	reg := newAnonymousRegistry(t, func(string, string) int { return http.StatusForbidden })
	c, ref := reg.client(nil)
	_, _, err := c.FetchManifest(ref)
	if want := "manifest " + ref.String() + ": " + statusText(http.StatusForbidden); err == nil || err.Error() != want {
		t.Fatalf("FetchManifest: err = %v, want %q", err, want)
	}
	if _, ok := err.(interface{ Unwrap() []error }); ok || errors.Unwrap(err) != nil {
		t.Fatalf("FetchManifest: err = %#v, want the status error alone", err)
	}
}

// After the credentials can be read again, a refusal under the new token
// no longer carries the earlier credential error.
func TestFetchManifest_RecoveredCredentialsClearTheCredentialError(t *testing.T) {
	var mu sync.Mutex
	anonymous := 0
	reg := newAnonymousRegistry(t, func(token, _ string) int {
		mu.Lock()
		defer mu.Unlock()
		if token == "anon-token" {
			anonymous++
			if anonymous > 1 {
				return http.StatusUnauthorized // challenge again
			}
		}
		return http.StatusForbidden
	})
	auth := &staticAuth{err: errCredentialSource}
	c, ref := reg.client(auth)
	if _, _, err := c.FetchManifest(ref); !errors.Is(err, errCredentialSource) {
		t.Fatalf("first FetchManifest: err = %v, want the credential source's error", err)
	}
	auth.err, auth.creds = nil, registryauth.Credentials{Username: "testuser", Password: "testpassword"}
	_, _, err := c.FetchManifest(ref)
	if want := "manifest " + ref.String() + ": " + statusText(http.StatusForbidden); err == nil || err.Error() != want {
		t.Fatalf("second FetchManifest: err = %v, want %q", err, want)
	}
	if errors.Is(err, errCredentialSource) {
		t.Fatalf("second FetchManifest: err = %v still carries the recovered credential error", err)
	}
}

// A Store that runs no helpers, for a registry that config.json gives to
// a helper: the anonymous Bearer retry is forbidden, and the error still
// matches ErrHelpersDisabled.
func TestFetchManifest_DisabledHelperForbiddenIsTyped(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"credsStore":"sample"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_AUTH_CONFIG", "")
	reg := newAnonymousRegistry(t, func(string, string) int { return http.StatusForbidden })
	c, ref := reg.client(registryauth.NewWithoutHelpers(dir))
	_, _, err := c.FetchManifest(ref)
	if !errors.Is(err, registryauth.ErrHelpersDisabled) {
		t.Fatalf("FetchManifest: err = %v, want ErrHelpersDisabled", err)
	}
}
