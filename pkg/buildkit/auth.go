package buildkit

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"sync"

	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth"
	"golang.org/x/crypto/nacl/sign"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/wyrd-company/devcontainer-builder/pkg/registryauth"
)

// RegistryAuth supplies the registry credentials of a solve.
type RegistryAuth interface {
	// Credentials returns the credentials for a registry host.
	Credentials(host string) (registryauth.Credentials, error)
	// EngineAuth returns the Engine API X-Registry-Auth value for an
	// image reference, or "" for an anonymous request.
	EngineAuth(ref string) (string, error)
}

// registryAuth is opt's RegistryAuth, or the docker CLI's configuration
// when it is nil.
func registryAuth(opt SolveOptions) RegistryAuth {
	if opt.RegistryAuth != nil {
		return opt.RegistryAuth
	}
	return registryauth.FromEnvironment()
}

// sessionAuth answers BuildKit's credential requests over the client
// session. For most registries it implements only Credentials: BuildKit
// then fetches registry tokens itself with those credentials. For a
// registry with a static registry token, the session is the token
// authority and FetchToken returns that token, as BuildKit's own provider
// does. Authority keys live in memory for the session, so the session
// writes nothing to disk.
type sessionAuth struct {
	auth.UnimplementedAuthServer
	creds RegistryAuth

	mu   sync.Mutex
	keys map[string]ed25519.PrivateKey // host -> token authority key
}

func newSessionAuth(creds RegistryAuth) session.Attachable {
	return &sessionAuth{creds: creds, keys: map[string]ed25519.PrivateKey{}}
}

func (a *sessionAuth) Register(server *grpc.Server) {
	auth.RegisterAuthServer(server, a)
}

func (a *sessionAuth) Credentials(_ context.Context, req *auth.CredentialsRequest) (*auth.CredentialsResponse, error) {
	c, err := a.creds.Credentials(req.Host)
	if err != nil {
		return nil, err
	}
	if c.IdentityToken != "" {
		return &auth.CredentialsResponse{Secret: c.IdentityToken}, nil
	}
	return &auth.CredentialsResponse{Username: c.Username, Secret: c.Password}, nil
}

func (a *sessionAuth) GetTokenAuthority(_ context.Context, req *auth.GetTokenAuthorityRequest) (*auth.GetTokenAuthorityResponse, error) {
	key, err := a.authority(req.Host)
	if err != nil {
		return nil, err
	}
	return &auth.GetTokenAuthorityResponse{PublicKey: key.Public().(ed25519.PublicKey)}, nil
}

func (a *sessionAuth) VerifyTokenAuthority(_ context.Context, req *auth.VerifyTokenAuthorityRequest) (*auth.VerifyTokenAuthorityResponse, error) {
	key, err := a.authority(req.Host)
	if err != nil {
		return nil, err
	}
	var priv [64]byte
	copy(priv[:], key)
	return &auth.VerifyTokenAuthorityResponse{Signed: sign.Sign(nil, req.Payload, &priv)}, nil
}

func (a *sessionAuth) FetchToken(_ context.Context, req *auth.FetchTokenRequest) (*auth.FetchTokenResponse, error) {
	token, err := a.registryToken(req.Host)
	if err != nil {
		return nil, err
	}
	return &auth.FetchTokenResponse{Token: token, ExpiresIn: registryTokenExpiry}, nil
}

// registryTokenExpiry is the validity, in seconds, reported for a static
// registry token, the value BuildKit's own provider reports.
const registryTokenExpiry = 60

// registryToken is host's static registry token. A host without one is
// unimplemented, so that BuildKit uses Credentials instead.
func (a *sessionAuth) registryToken(host string) (string, error) {
	c, err := a.creds.Credentials(host)
	if err != nil {
		return "", err
	}
	if c.RegistryToken == "" {
		return "", status.Errorf(codes.Unimplemented, "no registry token for %s", host)
	}
	return c.RegistryToken, nil
}

// authority is the session's token authority key for a host with a
// static registry token.
func (a *sessionAuth) authority(host string) (ed25519.PrivateKey, error) {
	if _, err := a.registryToken(host); err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if key, ok := a.keys[host]; ok {
		return key, nil
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	a.keys[host] = key
	return key, nil
}
