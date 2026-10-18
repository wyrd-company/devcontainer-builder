package oci

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/registryauth"
)

// recorder serves fn and records whether any request carried
// credentials, in an Authorization header or a refresh_token form field.
type recorder struct {
	mu       sync.Mutex
	requests int
	leaked   bool
}

func (r *recorder) serve(fn http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		_ = req.ParseForm()
		r.mu.Lock()
		r.requests++
		if req.Header.Get("Authorization") != "" || req.PostForm.Get("refresh_token") != "" {
			r.leaked = true
		}
		r.mu.Unlock()
		fn(w, req)
	}
}

func refOf(srv *httptest.Server) Reference {
	return Reference{Registry: strings.TrimPrefix(srv.URL, "https://"), Repository: "team/feature", Tag: "1"}
}

// Basic credentials do not follow a redirect to another port over
// plain HTTP.
func TestFetchManifest_BasicCredentialsDoNotFollowRedirectToAnotherOrigin(t *testing.T) {
	other := &recorder{}
	plain := httptest.NewServer(other.serve(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, testManifest) }))
	defer plain.Close()
	reg := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, plain.URL+"/artifact", http.StatusFound)
	}))
	defer reg.Close()
	c := NewClient()
	c.HTTP = reg.Client()
	c.Auth = &staticAuth{creds: registryauth.Credentials{Username: "testuser", Password: "testpassword"}}
	_, _, err := c.FetchManifest(refOf(reg))
	if other.leaked {
		t.Fatal("Basic credentials reached another origin over plain HTTP")
	}
	if err == nil || !strings.Contains(err.Error(), "non-HTTPS") {
		t.Fatalf("FetchManifest: err = %v, want a refused HTTPS downgrade", err)
	}
}

// A refresh token is not replayed to another origin by a 307 from the
// token realm.
func TestFetchManifest_RefreshTokenDoesNotFollowRedirectToAnotherOrigin(t *testing.T) {
	for _, tc := range []struct {
		name  string
		other func(http.Handler) *httptest.Server
	}{
		{"plain HTTP", httptest.NewServer},
		{"other HTTPS origin", httptest.NewTLSServer},
	} {
		t.Run(tc.name, func(t *testing.T) {
			other := &recorder{}
			elsewhere := tc.other(other.serve(func(w http.ResponseWriter, _ *http.Request) { fmt.Fprint(w, `{"token":"access-token"}`) }))
			defer elsewhere.Close()
			reg := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					http.Redirect(w, r, elsewhere.URL+"/token", http.StatusTemporaryRedirect)
					return
				}
				if r.Header.Get("Authorization") != "" {
					fmt.Fprint(w, testManifest)
					return
				}
				w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+r.Host+`/token"`)
				w.WriteHeader(http.StatusUnauthorized)
			}))
			defer reg.Close()
			c := NewClient()
			c.HTTP = reg.Client()
			c.Auth = &staticAuth{creds: registryauth.Credentials{IdentityToken: "synthetic-refresh-token"}}
			_, _, err := c.FetchManifest(refOf(reg))
			if other.leaked || other.requests > 0 {
				t.Fatal("the refresh-token request followed a redirect to another origin")
			}
			if err == nil {
				t.Fatal("FetchManifest succeeded through a redirected token request")
			}
		})
	}
}

// A redirect within the registry's origin keeps the credentials.
func TestFetchManifest_SameOriginRedirectKeepsCredentials(t *testing.T) {
	want := "Basic " + basicCredential("testuser", "testpassword")
	reg := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != want {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/moved" {
			http.Redirect(w, r, "/moved", http.StatusTemporaryRedirect)
			return
		}
		w.Header().Set("Content-Type", mediaTypeOCIManifest)
		fmt.Fprint(w, testManifest)
	}))
	defer reg.Close()
	c := NewClient()
	c.HTTP = reg.Client()
	c.Auth = &staticAuth{creds: registryauth.Credentials{Username: "testuser", Password: "testpassword"}}
	if _, _, err := c.FetchManifest(refOf(reg)); err != nil {
		t.Fatalf("FetchManifest: %v", err)
	}
}

// A blob redirect to another HTTPS origin (a CDN) is followed without
// the registry's credentials.
func TestFetchBlob_RedirectToAnotherOriginDropsCredentials(t *testing.T) {
	blob := []byte("blob-content")
	cdn := &recorder{}
	store := httptest.NewTLSServer(cdn.serve(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(blob) }))
	defer store.Close()
	want := "Basic " + basicCredential("testuser", "testpassword")
	reg := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != want {
			w.Header().Set("WWW-Authenticate", `Basic realm="test"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, store.URL+"/signed-blob", http.StatusTemporaryRedirect)
	}))
	defer reg.Close()
	c := NewClient()
	c.HTTP = reg.Client()
	c.Auth = &staticAuth{creds: registryauth.Credentials{Username: "testuser", Password: "testpassword"}}
	var out strings.Builder
	if err := c.FetchBlob(refOf(reg), digestOf(blob), &out); err != nil {
		t.Fatalf("FetchBlob: %v", err)
	}
	if out.String() != string(blob) || cdn.requests != 1 {
		t.Fatalf("blob = %q after %d CDN requests, want the content from one", out.String(), cdn.requests)
	}
	if cdn.leaked {
		t.Fatal("registry credentials reached the CDN")
	}
}

// An anonymous request is not downgraded from HTTPS to HTTP either.
func TestFetchBlob_RedirectToPlainHTTPIsRefused(t *testing.T) {
	blob := []byte("blob-content")
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(blob) }))
	defer plain.Close()
	reg := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/blob?signature=synthetic-signature", http.StatusTemporaryRedirect)
	}))
	defer reg.Close()
	c := NewClient()
	c.HTTP = reg.Client()
	c.Auth = nil
	var out strings.Builder
	err := c.FetchBlob(refOf(reg), digestOf(blob), &out)
	if err == nil || !strings.Contains(err.Error(), "non-HTTPS") {
		t.Fatalf("FetchBlob: err = %v, want a refused HTTPS downgrade", err)
	}
	// The error names the request without its query.
	if strings.Contains(err.Error(), "synthetic-signature") {
		t.Fatalf("error carries the redirect URL's query: %v", err)
	}
}

// Response bodies never reach errors, so a server that echoes the
// credentials it received cannot put them into a log.
func TestFetchManifest_ErrorsOmitResponseBodies(t *testing.T) {
	echo := func(w http.ResponseWriter, r *http.Request, status int) {
		_ = r.ParseForm()
		w.WriteHeader(status)
		fmt.Fprint(w, r.Header.Get("Authorization")+" "+r.PostForm.Get("refresh_token"))
	}
	for _, tc := range []struct {
		name   string
		creds  registryauth.Credentials
		secret string
		serve  func(w http.ResponseWriter, r *http.Request)
	}{
		{"Basic credentials echoed by the token endpoint", registryauth.Credentials{Username: "testuser", Password: "testpassword"},
			basicCredential("testuser", "testpassword"),
			func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					echo(w, r, http.StatusUnauthorized)
					return
				}
				w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+r.Host+`/token"`)
				w.WriteHeader(http.StatusUnauthorized)
			}},
		{"refresh token echoed by the token endpoint", registryauth.Credentials{IdentityToken: "synthetic-refresh-token"},
			"synthetic-refresh-token",
			func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/token" {
					echo(w, r, http.StatusBadRequest)
					return
				}
				w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+r.Host+`/token"`)
				w.WriteHeader(http.StatusUnauthorized)
			}},
		{"bearer token echoed by a manifest failure", registryauth.Credentials{Username: "testuser", Password: "testpassword"},
			"synthetic-access-token",
			func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/token":
					fmt.Fprint(w, `{"token":"synthetic-access-token"}`)
				case r.Header.Get("Authorization") == "":
					w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+r.Host+`/token"`)
					w.WriteHeader(http.StatusUnauthorized)
				default:
					echo(w, r, http.StatusForbidden)
				}
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := httptest.NewTLSServer(http.HandlerFunc(tc.serve))
			defer reg.Close()
			c := NewClient()
			c.HTTP = reg.Client()
			c.Auth = &staticAuth{creds: tc.creds}
			ref := refOf(reg)
			_, _, err := c.FetchManifest(ref)
			if err == nil {
				t.Fatal("FetchManifest succeeded")
			}
			if strings.Contains(err.Error(), tc.secret) {
				t.Fatalf("error carries a credential: %v", err)
			}
			if !strings.Contains(err.Error(), ref.Registry) {
				t.Fatalf("error = %v, want the registry host", err)
			}
		})
	}
}

func TestFetchBlob_ErrorsOmitResponseBodies(t *testing.T) {
	reg := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, "synthetic-body-marker")
	}))
	defer reg.Close()
	c := NewClient()
	c.HTTP = reg.Client()
	c.Auth = nil
	var out strings.Builder
	err := c.FetchBlob(refOf(reg), digestOf([]byte("x")), &out)
	if err == nil || strings.Contains(err.Error(), "synthetic-body-marker") || !strings.Contains(err.Error(), "404") {
		t.Fatalf("FetchBlob: err = %v, want the status without the body", err)
	}
}
