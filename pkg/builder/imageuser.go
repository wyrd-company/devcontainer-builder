// relationships:
//   references: [generator, metadata]

package builder

import (
	"context"
	"fmt"

	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// baseImageUser returns the base image's user for the generated
// Dockerfile's final USER and feature builtins, or "" (root) when
// the lookup is skipped (SkipImageMetaResolve). When the lookup fails
// (for example, a registry whose credentials are missing) it logs a
// warning. In both cases, feature user defaults and the final generated
// image USER fall back to root.
func (b *Builder) baseImageUser(ctx context.Context, dc *spec.DevContainer, opts Options, logf func(string, ...any)) string {
	if opts.SkipImageMetaResolve {
		return ""
	}
	user, err := b.imageUser(ctx, dc, opts)
	if err != nil {
		logf("warning: base image user unknown, feature user defaults and final generated image USER fall back to root: %v", err)
		return ""
	}
	return user
}

// imageUser returns the user the devcontainer's base image runs as,
// which features see as the container user when the spec sets no
// containerUser. An image-based spec uses that image's user. Like
// upstream (findUserStatement with the base image's env), a
// Dockerfile-based spec evaluates the target stage: see dockerfileUser.
// Image configs for the build's primary platform come from the Docker
// daemon or the registry, as for BaseMetadata.
func (b *Builder) imageUser(ctx context.Context, dc *spec.DevContainer, opts Options) (string, error) {
	p := primaryPlatform(opts.Platforms)
	resolve, closeEngine, err := b.imageConfigResolver(ctx, opts)
	if err != nil {
		return "", err
	}
	defer closeEngine()
	imageConfig := func(image string) (*oci.ImageConfig, error) {
		cfg, err := resolve(image)
		if err != nil {
			return nil, fmt.Errorf("base image %s config: %w", image, err)
		}
		return cfg, nil
	}
	dfPath := dc.DockerfileFile()
	if dfPath == "" {
		if dc.Image == "" {
			return "", nil
		}
		cfg, err := imageConfig(dc.Image)
		if err != nil {
			return "", err
		}
		return cfg.Config.User, nil
	}
	target := ""
	if dc.Build != nil {
		target = dc.Build.Target
	}
	return dockerfileUser(dfPath, target, mergeArgs(buildArgsFromSpec(dc), opts.BuildArgs), p, imageConfig)
}
