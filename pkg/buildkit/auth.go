package buildkit

import (
	"context"

	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/auth"
	"google.golang.org/grpc"

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
// session. It implements only Credentials: BuildKit then fetches registry
// tokens itself with those credentials, so the session needs no token
// signing key and writes nothing to disk.
type sessionAuth struct {
	auth.UnimplementedAuthServer
	creds RegistryAuth
}

func newSessionAuth(creds RegistryAuth) session.Attachable {
	return &sessionAuth{creds: creds}
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
