// relationships:
//   references: [dockerfileuser, metadata, ../dockerfile/generator]

package builder

import (
	"context"
	"path/filepath"

	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// baseImageUser returns the base image's user for the generated
// Dockerfile's final USER and feature builtins. When the lookup fails it
// logs a warning and falls back to root. SkipImageMetaResolve disables
// registry lookup; an explicitly supplied Docker client still provides metadata.
func (b *Builder) baseImageUser(ctx context.Context, dc *spec.DevContainer, opts Options, logf func(string, ...any)) string {
	user, err := b.imageUser(ctx, dc, opts)
	if err != nil {
		logf("warning: base image user unknown, features see root: %v", err)
		return ""
	}
	return user
}

// imageUser returns the user the devcontainer's base image runs as,
// which features see as the container user when the spec sets no
// containerUser. An image-based spec uses that image's user. Like
// upstream (findUserStatement with the base image's env), a
// Dockerfile-based spec evaluates the target stage: see dockerfileUser.
// Image configs are read locally or from their registry for the build's
// primary platform.
func (b *Builder) imageUser(ctx context.Context, dc *spec.DevContainer, opts Options) (string, error) {
	p := primaryPlatform(opts.Platforms)
	imageConfig := func(image string) (*oci.ImageConfig, error) {
		return b.imageConfig(ctx, image, opts)
	}
	dfPath := dc.DockerfilePath()
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
	ctxDir, err := dc.BuildContextDir()
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(dfPath) {
		dfPath = filepath.Join(ctxDir, dfPath)
	}
	target := ""
	if dc.Build != nil {
		target = dc.Build.Target
	}
	return dockerfileUser(dfPath, target, mergeArgs(buildArgsFromSpec(dc), opts.BuildArgs), p, imageConfig)
}
