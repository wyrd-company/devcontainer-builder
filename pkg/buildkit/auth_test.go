package buildkit

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/moby/buildkit/session/auth"
	"golang.org/x/crypto/nacl/sign"

	"github.com/wyrd-company/devcontainer-builder/pkg/registryauth"
)

func sessionCredentials(t *testing.T, creds RegistryAuth, host string) (*auth.CredentialsResponse, error) {
	t.Helper()
	server, ok := newSessionAuth(creds).(auth.AuthServer)
	if !ok {
		t.Fatal("session attachable is not an auth server")
	}
	return server.Credentials(context.Background(), &auth.CredentialsRequest{Host: host})
}

func TestSessionAuth_AnswersWithRegistryCredentials(t *testing.T) {
	creds := registryauth.New(dockerConfig(t, `{"auths":{"registry.example.test":{"auth":"`+
		base64.StdEncoding.EncodeToString([]byte("testuser:testpassword"))+`"}}}`))
	resp, err := sessionCredentials(t, creds, "registry.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Username != "testuser" || resp.Secret != "testpassword" {
		t.Fatalf("Credentials = %+v, want the configured credentials", resp)
	}
	resp, err = sessionCredentials(t, creds, "other.example.test")
	if err != nil || resp.Username != "" || resp.Secret != "" {
		t.Fatalf("Credentials for an unconfigured host = %+v, %v; want anonymous", resp, err)
	}
}

func TestSessionAuth_IdentityTokenIsTheSecret(t *testing.T) {
	creds := registryauth.New(dockerConfig(t, `{"auths":{"registry.example.test":{"auth":"`+
		base64.StdEncoding.EncodeToString([]byte("testuser:"))+`","identitytoken":"test-refresh-token"}}}`))
	resp, err := sessionCredentials(t, creds, "registry.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Username != "" || resp.Secret != "test-refresh-token" {
		t.Fatalf("Credentials = %+v, want the identity token as the secret", resp)
	}
}

func TestSessionAuth_CredentialErrorFails(t *testing.T) {
	_, err := sessionCredentials(t, failingAuth{}, "registry.example.test")
	if err == nil || !strings.Contains(err.Error(), "credential helper failed") {
		t.Fatalf("Credentials err = %v, want the credential error", err)
	}
}

// BuildKit fetches tokens itself when the session has no token authority.
func TestSessionAuth_HasNoTokenAuthority(t *testing.T) {
	server := newSessionAuth(anonymous(t)).(auth.AuthServer)
	if _, err := server.GetTokenAuthority(context.Background(), &auth.GetTokenAuthorityRequest{Host: "registry.example.test"}); err == nil {
		t.Fatal("GetTokenAuthority succeeded, want unimplemented")
	}
}

func TestRegistryAuth_DefaultsToTheDockerConfiguration(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", dockerConfig(t, `{"auths":{"registry.example.test":{"auth":"`+
		base64.StdEncoding.EncodeToString([]byte("testuser:testpassword"))+`"}}}`))
	got, err := registryAuth(SolveOptions{}).Credentials("registry.example.test")
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != "testuser" || got.Password != "testpassword" {
		t.Fatalf("default Credentials = %+v, want the DOCKER_CONFIG entry", got)
	}
	explicit := anonymous(t)
	if registryAuth(SolveOptions{RegistryAuth: explicit}) != explicit {
		t.Fatal("registryAuth replaced an explicit RegistryAuth")
	}
}

// registryTokenAuth answers every host with a static registry token.
type registryTokenAuth struct{}

func (registryTokenAuth) Credentials(string) (registryauth.Credentials, error) {
	return registryauth.Credentials{RegistryToken: "synthetic-registry-token"}, nil
}

func (registryTokenAuth) EngineAuth(string) (string, error) { return "", nil }

// A static registry token reaches BuildKit: the session is the token
// authority for that host and FetchToken returns the token.
func TestSessionAuth_RegistryTokenIsFetched(t *testing.T) {
	server := newSessionAuth(registryTokenAuth{}).(auth.AuthServer)
	ctx := context.Background()
	authority, err := server.GetTokenAuthority(ctx, &auth.GetTokenAuthorityRequest{Host: "registry.example.test", Salt: []byte("salt")})
	if err != nil || len(authority.PublicKey) != ed25519.PublicKeySize {
		t.Fatalf("GetTokenAuthority = %v, %v; want an ed25519 public key", authority, err)
	}
	payload := []byte("synthetic-payload-0123456789abcdef")
	verified, err := server.VerifyTokenAuthority(ctx, &auth.VerifyTokenAuthorityRequest{Host: "registry.example.test", Salt: []byte("salt"), Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	var pub [32]byte
	copy(pub[:], authority.PublicKey)
	if got, ok := sign.Open(nil, verified.Signed, &pub); !ok || string(got) != string(payload) {
		t.Fatal("VerifyTokenAuthority did not sign the payload with the authority key")
	}
	tok, err := server.FetchToken(ctx, &auth.FetchTokenRequest{Host: "registry.example.test", Scopes: []string{"repository:team/app:pull"}})
	if err != nil || tok.Token != "synthetic-registry-token" {
		t.Fatalf("FetchToken = %v, %v; want the registry token", tok, err)
	}
	creds, err := server.Credentials(ctx, &auth.CredentialsRequest{Host: "registry.example.test"})
	if err != nil || creds.Secret != "" {
		t.Fatalf("Credentials = %v, %v; want no secret for a token-only host", creds, err)
	}
}

func TestWithCredentialError_KeepsTheSourceErrorTyped(t *testing.T) {
	sentinel := errors.New("synthetic credential source failure")
	session := newSessionAuth(sentinelAuth{sentinel})
	server, ok := session.(auth.AuthServer)
	if !ok {
		t.Fatal("session attachable is not an auth server")
	}
	if _, err := server.Credentials(context.Background(), &auth.CredentialsRequest{Host: "registry.example.test"}); !errors.Is(err, sentinel) {
		t.Fatalf("Credentials err = %v, want the source error", err)
	}
	solve := errors.New("failed to solve: synthetic")
	err := withCredentialError(solve, session)
	if !errors.Is(err, sentinel) || !errors.Is(err, solve) || err.Error() != solve.Error() {
		t.Fatalf("withCredentialError = %v, want the solve message wrapping both errors", err)
	}
}

func TestWithCredentialError_KeepsOtherErrorsUnchanged(t *testing.T) {
	session := newSessionAuth(registryTokenAuth{})
	solve := errors.New("failed to solve: synthetic")
	if err := withCredentialError(solve, session); err != solve {
		t.Fatalf("withCredentialError = %v, want the solve error itself", err)
	}
	if err := withCredentialError(nil, session); err != nil {
		t.Fatalf("withCredentialError(nil) = %v, want nil", err)
	}
}

type sentinelAuth struct{ err error }

func (a sentinelAuth) Credentials(string) (registryauth.Credentials, error) {
	return registryauth.Credentials{}, a.err
}
func (a sentinelAuth) EngineAuth(string) (string, error) { return "", a.err }
