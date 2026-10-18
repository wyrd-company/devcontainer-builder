package buildkit

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/moby/buildkit/session/auth"

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
