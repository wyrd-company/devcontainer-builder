package oci

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/wyrd-company/devcontainer-builder/pkg/registryauth"
)

// CredentialSource supplies the credentials for a registry host.
type CredentialSource interface {
	Credentials(host string) (registryauth.Credentials, error)
}

func (c *Client) do(req *http.Request, ref Reference) (*http.Response, error) {
	if c.authorization == nil {
		c.authorization = map[string]string{}
	}
	if h, ok := c.authorization[ref.Registry]; ok {
		req.Header.Set("Authorization", h)
	}
	resp, err := c.send(req, false, ref.Registry)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		challenge := resp.Header.Get("WWW-Authenticate")
		_ = resp.Body.Close()
		// Credentials are bound to the registry's origin: a challenge from
		// the target of a redirect to another origin never gets them.
		if !sameOrigin(req.URL, resp.Request.URL) {
			return nil, fmt.Errorf("registry %s redirected to another origin that requires authentication; the registry's credentials are not sent there", ref.Registry)
		}
		creds, credErr := c.credentials(ref)
		c.recordAnonymous(ref, credErr)
		h, aerr := c.authorize(ref, challenge, creds)
		if aerr != nil {
			return nil, withCredentialError(aerr, credErr)
		}
		c.authorization[ref.Registry] = h
		newReq := req.Clone(req.Context())
		newReq.Header.Set("Authorization", h)
		resp, err = c.send(newReq, false, ref.Registry)
		if err == nil && credErr != nil && resp.StatusCode == http.StatusUnauthorized {
			_ = resp.Body.Close()
			return nil, withCredentialError(fmt.Errorf("registry %s refused anonymous access", ref.Registry), credErr)
		}
		return resp, err
	}
	return resp, nil
}

// credentials returns the registry's credentials. When they cannot be
// read (for example, a failing credential helper), access continues
// anonymously, as upstream's OCI client does, and the error is returned
// for a later failure to report.
func (c *Client) credentials(ref Reference) (registryauth.Credentials, error) {
	if c.Auth == nil {
		return registryauth.Credentials{}, nil
	}
	creds, err := c.Auth.Credentials(ref.Registry)
	if err != nil {
		return registryauth.Credentials{}, err
	}
	return creds, nil
}

// recordAnonymous keeps credErr as the reason that ref's registry is
// accessed anonymously, or forgets the reason once credentials are read.
func (c *Client) recordAnonymous(ref Reference, credErr error) {
	if c.anonymous == nil {
		c.anonymous = map[string]error{}
	}
	if credErr == nil {
		delete(c.anonymous, ref.Registry)
		return
	}
	c.anonymous[ref.Registry] = credErr
}

// refused returns err, a registry's refusal of a request, wrapping the
// credential source error that made the client access that registry
// anonymously, if any. The message stays err's.
func (c *Client) refused(ref Reference, err error) error {
	if credErr := c.anonymous[ref.Registry]; credErr != nil {
		return &anonymousRefusal{err: err, cred: credErr}
	}
	return err
}

// anonymousRefusal is a registry's refusal of a request that the client
// sent anonymously because the registry's credentials could not be read.
type anonymousRefusal struct{ err, cred error }

func (e *anonymousRefusal) Error() string   { return e.err.Error() }
func (e *anonymousRefusal) Unwrap() []error { return []error{e.err, e.cred} }

func withCredentialError(err, credErr error) error {
	if credErr == nil {
		return err
	}
	return fmt.Errorf("%w (%w)", err, credErr)
}

// authorize answers a registry's WWW-Authenticate challenge with an
// Authorization header value.
func (c *Client) authorize(ref Reference, challenge string, creds registryauth.Credentials) (string, error) {
	scheme, params, _ := strings.Cut(challenge, " ")
	switch strings.ToLower(scheme) {
	case "basic":
		if creds.Username == "" || creds.Password == "" {
			return "", fmt.Errorf("registry %s requires credentials, and the Docker client configuration has none", ref.Registry)
		}
		return "Basic " + basicCredential(creds.Username, creds.Password), nil
	case "bearer":
		if creds.RegistryToken != "" {
			return "Bearer " + creds.RegistryToken, nil
		}
		tok, err := c.acquireToken(ref, parseChallenge(params), creds)
		if err != nil {
			return "", err
		}
		return "Bearer " + tok, nil
	}
	return "", fmt.Errorf("registry %s: unsupported authentication challenge", ref.Registry)
}

// acquireToken requests a registry token from the challenge's realm. An
// identity token is exchanged through the OAuth2 refresh-token grant;
// otherwise the request carries the Basic credentials, if any. The realm
// must be HTTPS, as the registry is.
func (c *Client) acquireToken(ref Reference, params map[string]string, creds registryauth.Credentials) (string, error) {
	realm := params["realm"]
	if realm == "" {
		return "", fmt.Errorf("registry %s: authentication challenge has no realm", ref.Registry)
	}
	u, err := url.Parse(realm)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("registry %s: invalid token realm", ref.Registry)
	}
	if u.Scheme != "https" {
		return "", fmt.Errorf("registry %s: token realm is not HTTPS", ref.Registry)
	}
	scope := params["scope"]
	if scope == "" {
		scope = fmt.Sprintf("repository:%s:pull", ref.Repository)
	}

	var req *http.Request
	if creds.IdentityToken != "" {
		form := url.Values{
			"grant_type":    {"refresh_token"},
			"client_id":     {"devcontainer-builder"},
			"refresh_token": {creds.IdentityToken},
			"scope":         {scope},
		}
		if svc := params["service"]; svc != "" {
			form.Set("service", svc)
		}
		req, err = http.NewRequest(http.MethodPost, u.String(), strings.NewReader(form.Encode()))
		if err != nil {
			return "", fmt.Errorf("registry %s: invalid token realm", ref.Registry)
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		q := u.Query()
		if svc := params["service"]; svc != "" {
			q.Set("service", svc)
		}
		q.Set("scope", scope)
		u.RawQuery = q.Encode()
		req, err = http.NewRequest(http.MethodGet, u.String(), nil)
		if err != nil {
			return "", fmt.Errorf("registry %s: invalid token realm", ref.Registry)
		}
		if creds.Username != "" || creds.Password != "" {
			req.SetBasicAuth(creds.Username, creds.Password)
		}
	}
	resp, err := c.send(req, !creds.Anonymous(), ref.Registry)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("registry %s token request: %s", ref.Registry, statusText(resp.StatusCode))
	}
	var payload struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, tokenResponseLimit)).Decode(&payload); err != nil {
		return "", fmt.Errorf("registry %s token response is malformed", ref.Registry)
	}
	if payload.Token != "" {
		return payload.Token, nil
	}
	return payload.AccessToken, nil
}

// tokenResponseLimit bounds a token endpoint's answer.
const tokenResponseLimit = 1 << 20

// send performs req with redirects bound to its origin. An HTTPS request
// is never redirected to plain HTTP. A redirect that changes the scheme,
// host or port drops the Authorization header, so that a blob redirect to
// a CDN is anonymous; when bound is set, such a redirect is refused, so
// that a credentialed token request reaches only the advertised realm.
// Errors name registry, the configured host, and a fixed reason, never
// a server-supplied URL or message.
func (c *Client) send(req *http.Request, bound bool, registry string) (*http.Response, error) {
	client := *c.HTTP
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) >= 10 {
			return redirectError("registry " + registry + ": stopped after 10 redirects")
		}
		origin := via[0].URL
		if origin.Scheme == "https" && next.URL.Scheme != "https" {
			return redirectError("registry " + registry + ": redirect to a non-HTTPS URL refused")
		}
		if !sameOrigin(origin, next.URL) {
			if bound {
				return redirectError("registry " + registry + ": redirect of a credentialed request to another origin refused")
			}
			next.Header.Del("Authorization")
		}
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		var redirect redirectError
		if errors.As(err, &redirect) {
			return nil, redirect
		}
		return nil, fmt.Errorf("registry %s: request failed: %s", registry, transportReason(err))
	}
	return resp, nil
}

// redirectError is a redirect refusal; its text names only the configured
// registry.
type redirectError string

func (e redirectError) Error() string { return string(e) }

// transportReason is a fixed description of a transport failure, so that
// no server-supplied text (a malformed status line, a redirect URL, an
// HTTP/2 debug message) reaches an error.
func transportReason(err error) string {
	// *url.Error is itself a net.Error; classify what it wraps.
	var uerr *url.Error
	if errors.As(err, &uerr) {
		err = uerr.Err
	}
	var (
		netErr      net.Error
		unknownCA   x509.UnknownAuthorityError
		hostname    x509.HostnameError
		invalid     x509.CertificateInvalidError
		certificate *tls.CertificateVerificationError
	)
	switch {
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return "timed out"
	case errors.As(err, &certificate), errors.As(err, &unknownCA), errors.As(err, &hostname), errors.As(err, &invalid):
		return "TLS certificate verification failed"
	case errors.As(err, &netErr):
		return "connection failed"
	}
	return "invalid response or redirect"
}

// statusText is an HTTP status as its code and standard text, never the
// server's reason phrase.
func statusText(code int) string {
	return strings.TrimSpace(fmt.Sprintf("%d %s", code, http.StatusText(code)))
}

// sameOrigin reports whether a and b share scheme, host and port.
func sameOrigin(a, b *url.URL) bool {
	return strings.EqualFold(a.Scheme, b.Scheme) &&
		strings.EqualFold(a.Hostname(), b.Hostname()) &&
		effectivePort(a) == effectivePort(b)
}

func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}

func basicCredential(user, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
}

func parseChallenge(s string) map[string]string {
	out := map[string]string{}
	for len(s) > 0 {
		eq := strings.Index(s, "=")
		if eq < 0 {
			break
		}
		k := strings.TrimSpace(s[:eq])
		s = s[eq+1:]
		var v string
		if strings.HasPrefix(s, `"`) {
			s = s[1:]
			end := strings.Index(s, `"`)
			if end < 0 {
				break
			}
			v = s[:end]
			s = s[end+1:]
		} else {
			end := strings.IndexAny(s, ", ")
			if end < 0 {
				v = s
				s = ""
			} else {
				v = s[:end]
				s = s[end:]
			}
		}
		out[strings.ToLower(k)] = v
		s = strings.TrimLeft(s, ", \t")
	}
	return out
}
