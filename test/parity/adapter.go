// relationships:
//
//	references: README
package main

import (
	"context"
	"fmt"
	"path/filepath"
)

// Adapter owns CLI-specific build/up arguments and JSON extraction. Observation,
// scoring, fixture preparation and teardown are shared. A single-dcb adapter can
// replace LegacyAdapter without changing those parts.
type Adapter interface {
	Name() string
	Up(context.Context, *Runner, string, string) ([]byte, error)
}

type LegacyAdapter struct{ Build, Runtime string }

func (LegacyAdapter) Name() string {
	return "dcb + devcontainer-docker (native BuildKit, no VS Code server)"
}
func (a LegacyAdapter) Up(ctx context.Context, r *Runner, workspace, tag string) ([]byte, error) {
	_, err := r.command(ctx, a.Build, "build", "--workspace", workspace, "--load", "--tag", tag, "--label", r.label())
	if err != nil {
		return nil, fmt.Errorf("build: %w", err)
	}
	return r.command(ctx, a.Runtime, "up", "--workspace", workspace, "--image", tag, "--skip-build")
}

type UpstreamAdapter struct{ Version string }

func (a UpstreamAdapter) Name() string { return "@devcontainers/cli@" + a.Version }
func (a UpstreamAdapter) Up(ctx context.Context, r *Runner, workspace, tag string) ([]byte, error) {
	return r.command(ctx, "npx", "--yes", "@devcontainers/cli@"+a.Version, "up", "--workspace-folder", workspace, "--mount-workspace-git-root", "false")
}

func buildAdapter(ctx context.Context, r *Runner) (Adapter, error) {
	a := LegacyAdapter{filepath.Join(r.temp, "dcb"), filepath.Join(r.temp, "devcontainer-docker")}
	for _, binary := range []struct{ path, pkg string }{{a.Build, "./cmd/dcb"}, {a.Runtime, "./cmd/devcontainer-docker"}} {
		if _, err := r.command(ctx, "go", "build", "-o", binary.path, binary.pkg); err != nil {
			return nil, err
		}
	}
	return a, nil
}
