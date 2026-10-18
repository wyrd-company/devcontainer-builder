package oci

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
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
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		challenge := resp.Header.Get("WWW-Authenticate")
		_ = resp.Body.Close()
		creds, credErr := c.credentials(ref)
		h, aerr := c.authorize(ref, challenge, creds)
		if aerr != nil {
			return nil, withCredentialError(aerr, credErr)
		}
		c.authorization[ref.Registry] = h
		newReq := req.Clone(req.Context())
		newReq.Header.Set("Authorization", h)
		resp, err = c.HTTP.Do(newReq)
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

func withCredentialError(err, credErr error) error {
	if credErr == nil {
		return err
	}
	return fmt.Errorf("%w (%v)", err, credErr)
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
	return "", fmt.Errorf("unsupported auth challenge: %q", challenge)
}

// acquireToken requests a registry token from the challenge's realm. An
// identity token is exchanged through the OAuth2 refresh-token grant;
// otherwise the request carries the Basic credentials, if any. Credentials
// are sent only to an HTTPS realm.
func (c *Client) acquireToken(ref Reference, params map[string]string, creds registryauth.Credentials) (string, error) {
	realm := params["realm"]
	if realm == "" {
		return "", fmt.Errorf("auth challenge missing realm")
	}
	u, err := url.Parse(realm)
	if err != nil {
		return "", err
	}
	if !creds.Anonymous() && u.Scheme != "https" {
		return "", fmt.Errorf("registry %s token realm %s is not HTTPS, so its credentials are not sent", ref.Registry, u.Redacted())
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
			return "", err
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
			return "", err
		}
		if creds.Username != "" || creds.Password != "" {
			req.SetBasicAuth(creds.Username, creds.Password)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("token: %s: %s", resp.Status, string(body))
	}
	var payload struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.Token != "" {
		return payload.Token, nil
	}
	return payload.AccessToken, nil
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
