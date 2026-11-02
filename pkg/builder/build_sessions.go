// relationships:
//   references: [build_options, builder, ../buildkit/client]

package builder

import (
	"fmt"
	buildx "github.com/docker/buildx/build"
	"github.com/moby/buildkit/session"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// buildSessions uses the native buildx providers without normalizing duplicate
// IDs: secret stores select the last source; SSH providers reject duplicate IDs.
func buildSessions(dc *spec.DevContainer) ([]session.Attachable, error) {
	parsed, err := parseBuildOptions(dc)
	if err != nil {
		return nil, err
	}
	var providers []session.Attachable
	if len(parsed.Secrets) > 0 {
		provider, err := buildx.CreateSecrets(parsed.Secrets)
		if err != nil {
			return nil, fmt.Errorf("build.options: secret: %w", err)
		}
		providers = append(providers, provider)
	}
	if len(parsed.SSH) > 0 {
		provider, err := buildx.CreateSSH(parsed.SSH)
		if err != nil {
			return nil, fmt.Errorf("build.options: ssh: %w", err)
		}
		providers = append(providers, provider)
	}
	return providers, nil
}
