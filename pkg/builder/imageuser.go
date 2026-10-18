package builder

import (
	"fmt"
	"path/filepath"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// baseImageUser returns the base image's user for the generated
// Dockerfile's feature builtins, or "" (root) when there are no features
// or the lookup is skipped (SkipImageMetaResolve). When the lookup fails
// (for example, a registry that needs credentials) it logs a warning and
// features see root, so the build still runs.
func (b *Builder) baseImageUser(dc *spec.DevContainer, opts Options, resolved []features.Resolved, logf func(string, ...any)) string {
	if len(resolved) == 0 || opts.SkipImageMetaResolve {
		return ""
	}
	user, err := b.imageUser(dc, opts)
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
// Image configs are read from their registry for the build's primary
// platform.
func (b *Builder) imageUser(dc *spec.DevContainer, opts Options) (string, error) {
	p := primaryPlatform(opts.Platforms)
	imageConfig := func(image string) (*oci.ImageConfig, error) {
		ref, err := oci.ParseReference(image)
		if err != nil {
			return nil, fmt.Errorf("base image %s: %w", image, err)
		}
		cfg, err := b.Resolver.OCI.FetchImageConfig(ref, oci.Platform{OS: p.OS, Architecture: p.Arch, Variant: p.Variant})
		if err != nil {
			return nil, fmt.Errorf("base image %s config: %w", image, err)
		}
		return cfg, nil
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
