// relationships:
//
//	references: [host, composebuild, ../../buildkit/auth, ../../registryauth]
package dockercompose

import (
	"fmt"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/registry"

	"github.com/wyrd-company/devcontainer-builder/pkg/buildkit"
	"github.com/wyrd-company/devcontainer-builder/pkg/registryauth"
)

// engineAuth is the credential source of Options.RegistryAuth.
type engineAuth = interface{ EngineAuth(string) (string, error) }

// serviceBuilds are the inputs of Up's builds of the non-primary services.
func (h *Host) serviceBuilds(proj *types.Project, primary string) BuildServiceImagesInputs {
	return BuildServiceImagesInputs{
		Project:      proj,
		Skip:         []string{primary},
		Docker:       h.client,
		RegistryAuth: buildAuth(h.opts.RegistryAuth),
		Stdout:       h.opts.Stdout,
		Stderr:       h.opts.Stderr,
		Logger:       h.opts.Logger,
	}
}

// buildAuth is the BuildKit credential source for a host's RegistryAuth:
// the value itself when it answers BuildKit, an adapter over its
// EngineAuth values otherwise, and nil (the BuildKit default) for none.
// A caller's source is never replaced by the Docker configuration.
func buildAuth(auth engineAuth) buildkit.RegistryAuth {
	switch a := auth.(type) {
	case nil:
		return nil
	case buildkit.RegistryAuth:
		return a
	default:
		return engineCredentials{a}
	}
}

// engineCredentialsRepository is the repository of the reference that
// engineCredentials asks EngineAuth about. BuildKit asks for credentials
// per registry host, so only the reference's host is meaningful.
const engineCredentialsRepository = "credentials"

// engineCredentials answers BuildKit's per-host credential requests from
// an Engine API credential source, by decoding the X-Registry-Auth value
// it gives for a reference on that host.
type engineCredentials struct{ engineAuth }

func (a engineCredentials) Credentials(host string) (registryauth.Credentials, error) {
	v, err := a.EngineAuth(host + "/" + engineCredentialsRepository)
	if err != nil || v == "" {
		return registryauth.Credentials{}, err
	}
	ac, err := registry.DecodeAuthConfig(v)
	if err != nil {
		// A fixed reason, as for every credential error: the value holds
		// the secret.
		return registryauth.Credentials{}, fmt.Errorf("registry credentials for %s: the X-Registry-Auth value is malformed", host)
	}
	return registryauth.Credentials{
		Username:      ac.Username,
		Password:      ac.Password,
		IdentityToken: ac.IdentityToken,
		RegistryToken: ac.RegistryToken,
	}, nil
}
