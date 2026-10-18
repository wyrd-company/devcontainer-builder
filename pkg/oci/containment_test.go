package oci

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/registryauth"
)

// The synthetic credentials a hostile server echoes back.
const (
	secretUser     = "testuser"
	secretPassword = "synthetic-secret-password"
	secretToken    = "synthetic-secret-refresh-token"
)

// secretForms are the spellings in which an echoed credential could
// appear in an error.
func secretForms() []string {
	basic := base64.StdEncoding.EncodeToString([]byte(secretUser + ":" + secretPassword))
	return []string{secretPassword, secretToken, basic, url.QueryEscape(basic), url.PathEscape(basic)}
}

func assertNoSecret(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("the request succeeded, want an error")
	}
	for _, form := range secretForms() {
		if strings.Contains(err.Error(), form) {
			t.Fatalf("error carries a credential (%q): %v", form, err)
		}
	}
}

// boundAuth answers only for one host, as a Docker configuration with a
// single registry entry does.
type boundAuth struct {
	host  string
	creds registryauth.Credentials
}

func (a boundAuth) Credentials(host string) (registryauth.Credentials, error) {
	if host == a.host {
		return a.creds, nil
	}
	return registryauth.Credentials{}, nil
}

func basicCreds() registryauth.Credentials {
	return registryauth.Credentials{Username: secretUser, Password: secretPassword}
}

func tokenCreds() registryauth.Credentials {
	return registryauth.Credentials{IdentityToken: secretToken}
}

// rawResponse writes resp verbatim on a hijacked connection.
func rawResponse(w http.ResponseWriter, resp string) {
	conn, rw, err := w.(http.Hijacker).Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = rw.WriteString(resp)
	_ = rw.Flush()
}

func bearerTo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+r.Host+`/token",service="test-registry"`)
	w.WriteHeader(http.StatusUnauthorized)
}

// echoed is the credential a request carried, for a hostile server to
// echo: its Authorization header, its refresh_token, or a fixed marker.
func echoed(r *http.Request) string {
	_ = r.ParseForm()
	if v := r.Header.Get("Authorization"); v != "" {
		return v
	}
	if v := r.PostForm.Get("refresh_token"); v != "" {
		return v
	}
	return secretPassword
}

// Every error path keeps credentials out of the error, whatever a hostile
// registry or token service puts in its status line, headers, URLs or
// bodies.
func TestErrors_NeverCarryCredentials(t *testing.T) {
	type fetch func(c *Client, ref Reference) error
	manifest := func(c *Client, ref Reference) error { _, _, err := c.FetchManifest(ref); return err }
	blob := func(digest string) fetch {
		return func(c *Client, ref Reference) error {
			var out strings.Builder
			return c.FetchBlob(ref, digest, &out)
		}
	}
	imageConfig := func(c *Client, ref Reference) error {
		_, err := c.FetchImageConfig(ref, Platform{OS: "linux", Architecture: "amd64"})
		return err
	}
	goodDigest := digestOf([]byte("blob-content"))
	cases := []struct {
		name  string
		serve func(w http.ResponseWriter, r *http.Request)
		fetch fetch
	}{
		{"token status reason echoes Basic credentials", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/token" {
				rawResponse(w, "HTTP/1.1 401 "+echoed(r)+"\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
				return
			}
			bearerTo(w, r)
		}, manifest},
		{"token body echoes credentials", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/token" {
				w.WriteHeader(http.StatusUnauthorized)
				fmt.Fprint(w, echoed(r))
				return
			}
			bearerTo(w, r)
		}, manifest},
		{"token response is malformed JSON with credentials", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/token" {
				fmt.Fprint(w, `{"token":`+echoed(r))
				return
			}
			bearerTo(w, r)
		}, manifest},
		{"token redirect Location is malformed and carries credentials", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/token" {
				w.Header().Set("Location", "https://example.test/%zz?credential="+url.QueryEscape(echoed(r)))
				w.WriteHeader(http.StatusFound)
				return
			}
			bearerTo(w, r)
		}, manifest},
		{"token redirect to another origin carries credentials", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/token" {
				w.Header().Set("Location", "https://other.example.test/"+url.PathEscape(echoed(r)))
				w.WriteHeader(http.StatusTemporaryRedirect)
				return
			}
			bearerTo(w, r)
		}, manifest},
		{"unsupported challenge carries credentials", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("WWW-Authenticate", "Custom "+secretPassword)
			w.WriteHeader(http.StatusUnauthorized)
		}, manifest},
		{"malformed realm carries credentials", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="https://%zz/`+secretPassword+`"`)
			w.WriteHeader(http.StatusUnauthorized)
		}, manifest},
		{"plain HTTP realm carries credentials", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="http://`+r.Host+`/`+secretPassword+`"`)
			w.WriteHeader(http.StatusUnauthorized)
		}, manifest},
		{"manifest status reason echoes the bearer token", func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/token":
				fmt.Fprint(w, `{"token":"`+secretPassword+`"}`)
			case r.Header.Get("Authorization") == "":
				bearerTo(w, r)
			default:
				rawResponse(w, "HTTP/1.1 403 "+echoed(r)+"\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
			}
		}, manifest},
		{"manifest body is malformed JSON with credentials", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, `{"mediaType":"`+secretPassword)
		}, manifest},
		{"malformed HTTP response carries credentials", func(w http.ResponseWriter, r *http.Request) {
			rawResponse(w, secretPassword+" garbage\r\n\r\n")
		}, manifest},
		{"registry redirect Location is malformed and carries credentials", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", "https://example.test/%zz?credential="+secretPassword)
			w.WriteHeader(http.StatusFound)
		}, manifest},
		{"registry redirect to plain HTTP carries credentials", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "http://"+r.Host+"/blob?credential="+secretPassword, http.StatusTemporaryRedirect)
		}, manifest},
		{"index child digest carries credentials", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", mediaTypeOCIIndex)
			fmt.Fprint(w, `{"schemaVersion":2,"mediaType":"`+mediaTypeOCIIndex+`","manifests":[{"digest":"sha256:`+secretPassword+`","platform":{"os":"linux","architecture":"amd64"}}]}`)
		}, manifest},
		{"manifest config digest carries credentials", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", mediaTypeOCIManifest)
			fmt.Fprint(w, `{"schemaVersion":2,"mediaType":"`+mediaTypeOCIManifest+`","config":{"digest":"sha256:`+secretPassword+`"},"layers":[]}`)
		}, imageConfig},
		{"blob status reason echoes credentials", func(w http.ResponseWriter, r *http.Request) {
			rawResponse(w, "HTTP/1.1 404 "+secretPassword+"\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
		}, blob(goodDigest)},
		{"blob does not match a credential-bearing digest", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, "other-content")
		}, blob("sha256:" + secretPassword)},
		{"blob does not match its digest", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprint(w, secretPassword)
		}, blob(goodDigest)},
	}
	for _, tc := range cases {
		for _, creds := range []struct {
			name  string
			value registryauth.Credentials
		}{{"Basic", basicCreds()}, {"refresh token", tokenCreds()}} {
			t.Run(tc.name+"/"+creds.name, func(t *testing.T) {
				reg := httptest.NewTLSServer(http.HandlerFunc(tc.serve))
				defer reg.Close()
				c := NewClient()
				c.HTTP = reg.Client()
				ref := refOf(reg)
				c.Auth = boundAuth{host: ref.Registry, creds: creds.value}
				assertNoSecret(t, tc.fetch(c, ref))
			})
		}
	}
}

// A challenge from the target of a redirect to another origin does not
// get the original registry's credentials.
func TestFetchManifest_RedirectedChallengeCannotReadOriginalCredentials(t *testing.T) {
	for _, creds := range []struct {
		name  string
		value registryauth.Credentials
	}{{"Basic", basicCreds()}, {"refresh token", tokenCreds()}} {
		for _, scheme := range []string{"Bearer", "Basic"} {
			t.Run(creds.name+"/"+scheme, func(t *testing.T) {
				var mu sync.Mutex
				leaked := false
				cdn := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_ = r.ParseForm()
					mu.Lock()
					if r.Header.Get("Authorization") != "" || r.PostForm.Get("refresh_token") != "" {
						leaked = true
					}
					mu.Unlock()
					if r.URL.Path == "/token" {
						fmt.Fprint(w, `{"token":"cdn-token"}`)
						return
					}
					if scheme == "Basic" {
						w.Header().Set("WWW-Authenticate", `Basic realm="cdn"`)
					} else {
						w.Header().Set("WWW-Authenticate", `Bearer realm="https://`+r.Host+`/token"`)
					}
					w.WriteHeader(http.StatusUnauthorized)
				}))
				defer cdn.Close()
				reg := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					http.Redirect(w, r, cdn.URL+"/artifact", http.StatusFound)
				}))
				defer reg.Close()
				c := NewClient()
				c.HTTP = reg.Client()
				ref := refOf(reg)
				c.Auth = boundAuth{host: ref.Registry, creds: creds.value}
				_, _, err := c.FetchManifest(ref)
				if leaked {
					t.Fatal("the redirect target's challenge received the original registry's credentials")
				}
				if err == nil || !strings.Contains(err.Error(), "another origin") {
					t.Fatalf("FetchManifest: err = %v, want a refused off-origin challenge", err)
				}
			})
		}
	}
}

// The token service's status reason is never copied into an error.
func TestFetchManifest_TokenStatusReasonDoesNotEchoCredentials(t *testing.T) {
	var mu sync.Mutex
	got := ""
	reg := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			mu.Lock()
			got = r.Header.Get("Authorization")
			mu.Unlock()
			rawResponse(w, "HTTP/1.1 401 "+got+"\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
			return
		}
		bearerTo(w, r)
	}))
	defer reg.Close()
	c := NewClient()
	c.HTTP = reg.Client()
	ref := refOf(reg)
	c.Auth = boundAuth{host: ref.Registry, creds: basicCreds()}
	_, _, err := c.FetchManifest(ref)
	if err == nil || got == "" || strings.Contains(err.Error(), got) {
		t.Fatalf("FetchManifest: err = %v, want a refusal without the echoed %q", err, got)
	}
	if !strings.Contains(err.Error(), "401 Unauthorized") {
		t.Fatalf("FetchManifest: err = %v, want the numeric status and its standard text", err)
	}
}

// A malformed redirect Location never reaches an error, even through the
// nested parse error.
func TestFetchManifest_MalformedRedirectErrorDoesNotEchoCredentials(t *testing.T) {
	marker := base64.StdEncoding.EncodeToString([]byte(secretUser + ":" + secretPassword))
	reg := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/token" {
			w.Header().Set("Location", "https://example.test/%zz?credential="+marker)
			w.WriteHeader(http.StatusFound)
			return
		}
		bearerTo(w, r)
	}))
	defer reg.Close()
	c := NewClient()
	c.HTTP = reg.Client()
	ref := refOf(reg)
	c.Auth = boundAuth{host: ref.Registry, creds: basicCreds()}
	_, _, err := c.FetchManifest(ref)
	if err == nil || strings.Contains(err.Error(), marker) {
		t.Fatalf("FetchManifest: err = %v, want a failure without the Location", err)
	}
}

// Transport failures are described by fixed reasons.
func TestFetchManifest_TransportFailuresHaveFixedReasons(t *testing.T) {
	t.Run("untrusted certificate", func(t *testing.T) {
		reg := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
		defer reg.Close()
		c := NewClient()
		c.Auth = nil
		if _, _, err := c.FetchManifest(refOf(reg)); err == nil || !strings.Contains(err.Error(), "TLS certificate verification failed") {
			t.Fatalf("FetchManifest: err = %v, want a TLS verification failure", err)
		}
	})
	t.Run("connection refused", func(t *testing.T) {
		reg := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
		ref := refOf(reg)
		client := reg.Client()
		reg.Close()
		c := NewClient()
		c.HTTP = client
		c.Auth = nil
		if _, _, err := c.FetchManifest(ref); err == nil || !strings.Contains(err.Error(), "connection failed") {
			t.Fatalf("FetchManifest: err = %v, want a connection failure", err)
		}
	})
}

// A malformed response gets its own fixed reason.
func TestFetchManifest_MalformedResponseHasAFixedReason(t *testing.T) {
	reg := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		rawResponse(w, "garbage\r\n\r\n")
	}))
	defer reg.Close()
	c := NewClient()
	c.HTTP = reg.Client()
	c.Auth = nil
	if _, _, err := c.FetchManifest(refOf(reg)); err == nil || !strings.Contains(err.Error(), "invalid response or redirect") {
		t.Fatalf("FetchManifest: err = %v, want an invalid-response failure", err)
	}
}

// A digest is checked before it becomes part of a request path, so that a
// manifest cannot point a request elsewhere on the registry.
func TestFetchBlob_InvalidDigestIsNotRequested(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	reg := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		http.NotFound(w, r)
	}))
	defer reg.Close()
	c := NewClient()
	c.HTTP = reg.Client()
	c.Auth = nil
	var out strings.Builder
	err := c.FetchBlob(refOf(reg), "sha256:../../../other/manifests/1", &out)
	if err == nil || !strings.Contains(err.Error(), "invalid digest") {
		t.Fatalf("FetchBlob: err = %v, want an invalid digest error", err)
	}
	if len(paths) != 0 {
		t.Fatalf("requested %v, want no request", paths)
	}
}

// Errors name the reference the caller asked for, not a digest the
// registry's index supplied.
func TestFetchManifest_ErrorsNameTheRequestedReference(t *testing.T) {
	child := "sha256:" + strings.Repeat("ab", 32)
	reg := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/manifests/1") {
			w.Header().Set("Content-Type", mediaTypeOCIIndex)
			fmt.Fprint(w, `{"schemaVersion":2,"mediaType":"`+mediaTypeOCIIndex+`","manifests":[{"digest":"`+child+`"}]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer reg.Close()
	c := NewClient()
	c.HTTP = reg.Client()
	c.Auth = nil
	ref := refOf(reg)
	_, _, err := c.FetchManifest(ref)
	if err == nil || strings.Contains(err.Error(), strings.Repeat("ab", 32)) || !strings.Contains(err.Error(), ref.String()+": 404 Not Found") {
		t.Fatalf("FetchManifest: err = %v, want the requested reference and the status, without the child digest", err)
	}
}

// An index child digest is checked before it becomes part of a request
// path.
func TestFetchManifest_InvalidChildDigestIsNotRequested(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	reg := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", mediaTypeOCIIndex)
		fmt.Fprint(w, `{"schemaVersion":2,"mediaType":"`+mediaTypeOCIIndex+`","manifests":[{"digest":"sha256:../../../other/manifests/1"}]}`)
	}))
	defer reg.Close()
	c := NewClient()
	c.HTTP = reg.Client()
	c.Auth = nil
	_, _, err := c.FetchManifest(refOf(reg))
	if err == nil || !strings.Contains(err.Error(), "invalid digest") {
		t.Fatalf("FetchManifest: err = %v, want an invalid digest error", err)
	}
	if len(paths) != 1 {
		t.Fatalf("requested %v, want only the index", paths)
	}
}
