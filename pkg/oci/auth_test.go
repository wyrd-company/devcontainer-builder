package oci

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/registryauth"
)

// staticAuth answers every host with the same credentials and records
// the hosts it is asked for.
type staticAuth struct {
	creds registryauth.Credentials
	err   error

	mu    sync.Mutex
	hosts []string
}

func (a *staticAuth) Credentials(host string) (registryauth.Credentials, error) {
	a.mu.Lock()
	a.hosts = append(a.hosts, host)
	a.mu.Unlock()
	return a.creds, a.err
}

const testManifest = `{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.empty.v1+json","digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":2},"layers":[]}`

// authRegistry serves testManifest to requests whose Authorization header
// equals want, and challenges every other request.
type authRegistry struct {
	srv       *httptest.Server
	challenge string
	want      string
	token     func(r *http.Request) (int, string)

	mu       sync.Mutex
	tokenReq []*http.Request
	forms    []string
}

func newAuthRegistry(t *testing.T, challenge func(base string) string, want string, token func(r *http.Request) (int, string)) *authRegistry {
	t.Helper()
	reg := &authRegistry{want: want, token: token}
	reg.srv = httptest.NewTLSServer(http.HandlerFunc(reg.serve))
	t.Cleanup(reg.srv.Close)
	reg.challenge = challenge(reg.srv.URL)
	return reg
}

func (reg *authRegistry) serve(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/token" {
		_ = r.ParseForm()
		reg.mu.Lock()
		reg.tokenReq = append(reg.tokenReq, r)
		reg.forms = append(reg.forms, r.PostForm.Encode())
		reg.mu.Unlock()
		code, body := reg.token(r)
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
		return
	}
	if r.Header.Get("Authorization") != reg.want {
		w.Header().Set("WWW-Authenticate", reg.challenge)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", mediaTypeOCIManifest)
	_, _ = w.Write([]byte(testManifest))
}

func (reg *authRegistry) client(auth CredentialSource) (*Client, Reference) {
	c := NewClient()
	c.HTTP = reg.srv.Client()
	c.Auth = auth
	return c, Reference{Registry: strings.TrimPrefix(reg.srv.URL, "https://"), Repository: "team/feature", Tag: "1"}
}

func basicChallenge(string) string { return `Basic realm="test"` }

func bearerChallenge(base string) string {
	return `Bearer realm="` + base + `/token",service="test-registry",scope="repository:team/feature:pull"`
}

func TestFetchManifest_BasicChallengeSendsCredentials(t *testing.T) {
	reg := newAuthRegistry(t, basicChallenge, "Basic "+basicCredential("testuser", "testpassword"), nil)
	auth := &staticAuth{creds: registryauth.Credentials{Username: "testuser", Password: "testpassword"}}
	c, ref := reg.client(auth)
	if _, _, err := c.FetchManifest(ref); err != nil {
		t.Fatalf("FetchManifest: %v", err)
	}
	if len(auth.hosts) != 1 || auth.hosts[0] != ref.Registry {
		t.Fatalf("credentials requested for %q, want [%s]", auth.hosts, ref.Registry)
	}
	// The header is reused without another challenge.
	if _, _, err := c.FetchManifest(ref); err != nil {
		t.Fatalf("second FetchManifest: %v", err)
	}
	if len(auth.hosts) != 1 {
		t.Fatalf("credentials requested %d times, want 1", len(auth.hosts))
	}
}

func TestFetchManifest_BasicChallengeWithoutCredentialsFails(t *testing.T) {
	reg := newAuthRegistry(t, basicChallenge, "Basic "+basicCredential("testuser", "testpassword"), nil)
	c, ref := reg.client(&staticAuth{})
	_, _, err := c.FetchManifest(ref)
	if err == nil || !strings.Contains(err.Error(), "requires credentials") {
		t.Fatalf("FetchManifest: err = %v, want a missing-credentials error", err)
	}
}

func TestFetchManifest_NilAuthIsAnonymous(t *testing.T) {
	reg := newAuthRegistry(t, bearerChallenge, "Bearer anonymous-token", func(r *http.Request) (int, string) {
		if r.Header.Get("Authorization") != "" {
			return http.StatusForbidden, `{}`
		}
		return http.StatusOK, `{"token":"anonymous-token"}`
	})
	c, ref := reg.client(nil)
	if _, _, err := c.FetchManifest(ref); err != nil {
		t.Fatalf("FetchManifest: %v", err)
	}
}

func TestFetchManifest_BearerTokenRequestCarriesBasicCredentials(t *testing.T) {
	reg := newAuthRegistry(t, bearerChallenge, "Bearer user-token", func(r *http.Request) (int, string) {
		user, pass, ok := r.BasicAuth()
		if r.Method != http.MethodGet || !ok || user != "testuser" || pass != "testpassword" {
			return http.StatusUnauthorized, `{"errors":[{"code":"UNAUTHORIZED"}]}`
		}
		if r.URL.Query().Get("service") != "test-registry" || r.URL.Query().Get("scope") != "repository:team/feature:pull" {
			return http.StatusBadRequest, `{}`
		}
		return http.StatusOK, `{"token":"user-token"}`
	})
	c, ref := reg.client(&staticAuth{creds: registryauth.Credentials{Username: "testuser", Password: "testpassword"}})
	if _, _, err := c.FetchManifest(ref); err != nil {
		t.Fatalf("FetchManifest: %v", err)
	}
}

func TestFetchManifest_BearerIdentityTokenUsesRefreshGrant(t *testing.T) {
	reg := newAuthRegistry(t, bearerChallenge, "Bearer access-token", func(r *http.Request) (int, string) {
		if r.Method != http.MethodPost || r.PostForm.Get("grant_type") != "refresh_token" ||
			r.PostForm.Get("refresh_token") != "test-refresh-token" ||
			r.PostForm.Get("service") != "test-registry" || r.PostForm.Get("scope") != "repository:team/feature:pull" {
			return http.StatusUnauthorized, `{}`
		}
		if _, _, ok := r.BasicAuth(); ok {
			return http.StatusBadRequest, `{}`
		}
		return http.StatusOK, `{"access_token":"access-token"}`
	})
	c, ref := reg.client(&staticAuth{creds: registryauth.Credentials{IdentityToken: "test-refresh-token"}})
	if _, _, err := c.FetchManifest(ref); err != nil {
		t.Fatalf("FetchManifest: %v", err)
	}
}

func TestFetchManifest_RegistryTokenIsSentAsBearer(t *testing.T) {
	reg := newAuthRegistry(t, bearerChallenge, "Bearer static-registry-token", func(*http.Request) (int, string) {
		return http.StatusInternalServerError, `{}`
	})
	c, ref := reg.client(&staticAuth{creds: registryauth.Credentials{RegistryToken: "static-registry-token"}})
	if _, _, err := c.FetchManifest(ref); err != nil {
		t.Fatalf("FetchManifest: %v", err)
	}
	if len(reg.tokenReq) != 0 {
		t.Fatalf("token endpoint called %d times, want 0", len(reg.tokenReq))
	}
}

// A failing credential source does not stop anonymous access, as in
// upstream's OCI client.
func TestFetchManifest_CredentialErrorFallsBackToAnonymous(t *testing.T) {
	reg := newAuthRegistry(t, bearerChallenge, "Bearer anonymous-token", func(r *http.Request) (int, string) {
		if r.Header.Get("Authorization") != "" {
			return http.StatusForbidden, `{}`
		}
		return http.StatusOK, `{"token":"anonymous-token"}`
	})
	c, ref := reg.client(&staticAuth{err: errors.New("credential helper failed")})
	if _, _, err := c.FetchManifest(ref); err != nil {
		t.Fatalf("FetchManifest: %v", err)
	}
}

// When anonymous access is then refused, the error reports the
// credential failure.
func TestFetchManifest_CredentialErrorIsReportedWhenAnonymousIsRefused(t *testing.T) {
	t.Run("token refused", func(t *testing.T) {
		reg := newAuthRegistry(t, bearerChallenge, "Bearer user-token", func(*http.Request) (int, string) {
			return http.StatusUnauthorized, `{}`
		})
		c, ref := reg.client(&staticAuth{err: errors.New("credential helper failed")})
		_, _, err := c.FetchManifest(ref)
		if err == nil || !strings.Contains(err.Error(), "credential helper failed") {
			t.Fatalf("FetchManifest: err = %v, want the credential error", err)
		}
	})
	t.Run("anonymous token refused by the registry", func(t *testing.T) {
		reg := newAuthRegistry(t, bearerChallenge, "Bearer user-token", func(*http.Request) (int, string) {
			return http.StatusOK, `{"token":"anonymous-token"}`
		})
		c, ref := reg.client(&staticAuth{err: errors.New("credential helper failed")})
		_, _, err := c.FetchManifest(ref)
		if err == nil || !strings.Contains(err.Error(), "refused anonymous access") || !strings.Contains(err.Error(), "credential helper failed") {
			t.Fatalf("FetchManifest: err = %v, want the refusal and the credential error", err)
		}
	})
	t.Run("basic challenge", func(t *testing.T) {
		reg := newAuthRegistry(t, basicChallenge, "Basic "+basicCredential("testuser", "testpassword"), nil)
		c, ref := reg.client(&staticAuth{err: errors.New("credential helper failed")})
		_, _, err := c.FetchManifest(ref)
		if err == nil || !strings.Contains(err.Error(), "requires credentials") || !strings.Contains(err.Error(), "credential helper failed") {
			t.Fatalf("FetchManifest: err = %v, want the missing credentials and the credential error", err)
		}
	})
}

func TestFetchManifest_RejectedCredentialsDoNotAppearInErrors(t *testing.T) {
	reg := newAuthRegistry(t, bearerChallenge, "Bearer user-token", func(*http.Request) (int, string) {
		return http.StatusUnauthorized, `{"errors":[{"code":"UNAUTHORIZED","message":"authentication required"}]}`
	})
	c, ref := reg.client(&staticAuth{creds: registryauth.Credentials{Username: "testuser", Password: "wrong-test-password"}})
	_, _, err := c.FetchManifest(ref)
	if err == nil {
		t.Fatal("FetchManifest with rejected credentials succeeded")
	}
	if strings.Contains(err.Error(), "wrong-test-password") || strings.Contains(err.Error(), basicCredential("testuser", "wrong-test-password")) {
		t.Fatalf("error carries the credentials: %v", err)
	}
}

func TestFetchManifest_CredentialsAreNotSentToPlainHTTPRealm(t *testing.T) {
	var mu sync.Mutex
	var calls int
	realm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		_, _ = w.Write([]byte(`{"token":"user-token"}`))
	}))
	defer realm.Close()
	reg := newAuthRegistry(t, func(string) string {
		return `Bearer realm="` + realm.URL + `/token",service="test-registry"`
	}, "Bearer user-token", nil)
	c, ref := reg.client(&staticAuth{creds: registryauth.Credentials{Username: "testuser", Password: "testpassword"}})
	_, _, err := c.FetchManifest(ref)
	if err == nil || !strings.Contains(err.Error(), "not HTTPS") {
		t.Fatalf("FetchManifest: err = %v, want a refused plain-HTTP realm", err)
	}
	if calls != 0 {
		t.Fatalf("plain-HTTP realm called %d times, want 0", calls)
	}
}

// The realm must be HTTPS, as the registry is, even for an anonymous
// token request.
func TestFetchManifest_PlainHTTPRealmIsRefusedWhenAnonymous(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	realm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls++
		mu.Unlock()
		_, _ = w.Write([]byte(`{"token":"anonymous-token"}`))
	}))
	defer realm.Close()
	reg := newAuthRegistry(t, func(string) string {
		return `Bearer realm="` + realm.URL + `/token",service="test-registry"`
	}, "Bearer anonymous-token", nil)
	c, ref := reg.client(&staticAuth{})
	if _, _, err := c.FetchManifest(ref); err == nil || !strings.Contains(err.Error(), "not HTTPS") {
		t.Fatalf("FetchManifest: err = %v, want a refused plain-HTTP realm", err)
	}
	if calls != 0 {
		t.Fatalf("plain-HTTP realm called %d times, want 0", calls)
	}
}

func TestWithAuth_UsesTheNewSourceWithAFreshAuthorization(t *testing.T) {
	first := "Basic " + basicCredential("firstuser", "firstpassword")
	second := "Basic " + basicCredential("seconduser", "secondpassword")
	var mu sync.Mutex
	want := first
	var seen []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		ok := r.Header.Get("Authorization") == want
		mu.Unlock()
		if !ok {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", mediaTypeOCIManifest)
		_, _ = w.Write([]byte(testManifest))
	}))
	t.Cleanup(srv.Close)
	ref := Reference{Registry: strings.TrimPrefix(srv.URL, "https://"), Repository: "team/feature", Tag: "1"}

	base := NewClient()
	base.HTTP = srv.Client()
	base.Auth = &staticAuth{creds: registryauth.Credentials{Username: "firstuser", Password: "firstpassword"}}
	if _, _, err := base.FetchManifest(ref); err != nil {
		t.Fatalf("base FetchManifest: %v", err)
	}

	mu.Lock()
	want, seen = second, nil
	mu.Unlock()
	auth := &staticAuth{creds: registryauth.Credentials{Username: "seconduser", Password: "secondpassword"}}
	bound := base.WithAuth(auth)
	if bound.HTTP != base.HTTP {
		t.Fatal("WithAuth did not share the HTTP client")
	}
	if _, _, err := bound.FetchManifest(ref); err != nil {
		t.Fatalf("bound FetchManifest: %v", err)
	}
	if len(auth.hosts) != 1 {
		t.Fatalf("bound source asked for %v, want one request", auth.hosts)
	}
	for _, h := range seen {
		if h == first {
			t.Fatal("bound client sent the base client's authorization")
		}
	}
	if base.Auth == auth {
		t.Fatal("WithAuth changed the base client's source")
	}
}

func TestWithAuth_NilClientUsesDefaults(t *testing.T) {
	auth := &staticAuth{}
	c := (*Client)(nil).WithAuth(auth)
	if c.HTTP == nil || c.Auth != auth {
		t.Fatalf("WithAuth on nil = %+v, want default HTTP and the source", c)
	}
}
