// relationships:
//
//	references: [dockerfileuser, metadata, oci]
package builder

import (
	"context"
	"fmt"
	"path/filepath"

	dockerclient "github.com/docker/docker/client"

	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// BaseMetadata reads the inherited metadata for the already-loaded config.
// Dockerfile configs follow the selected target's base-image ancestry. Image
// configs come from opts.Docker (or the default Engine client) when present
// locally for the requested platform, otherwise from the resolver's OCI registry
// client. SkipImageMetaResolve disables registry lookup. It does not build or pull
// image layers, and does not reload the devcontainer configuration.
func (b *Builder) BaseMetadata(ctx context.Context, dc *spec.DevContainer, opts Options) ([]metadata.Entry, error) {
	if dc == nil {
		return nil, fmt.Errorf("base metadata: config is required")
	}
	p := primaryPlatform(opts.Platforms)
	imageConfig := func(ref string) (*oci.ImageConfig, error) {
		return b.imageConfig(ctx, ref, opts)
	}
	var cfg *oci.ImageConfig
	var err error
	if df := dc.DockerfilePath(); df != "" {
		contextDir, contextErr := dc.BuildContextDir()
		if contextErr != nil {
			return nil, contextErr
		}
		if !filepath.IsAbs(df) {
			df = filepath.Join(contextDir, df)
		}
		target := ""
		if dc.Build != nil {
			target = dc.Build.Target
		}
		st, stateErr := dockerfileState(df, target, mergeArgs(buildArgsFromSpec(dc), opts.BuildArgs), p, imageConfig)
		if stateErr != nil {
			return nil, stateErr
		}
		cfg, err = st.root.config()
	} else if dc.Image != "" {
		cfg, err = imageConfig(dc.Image)
	} else {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("base metadata: %w", err)
	}
	return metadata.Read(cfg.Config.Labels, opts.Logger), nil
}

// imageConfig reads local image metadata before consulting the registry.
// SkipImageMetaResolve suppresses registry access while retaining local metadata.
func (b *Builder) imageConfig(ctx context.Context, ref string, opts Options) (*oci.ImageConfig, error) {
	if opts.Docker == nil && !opts.SkipImageMetaResolve {
		cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
		if err != nil {
			return nil, fmt.Errorf("base metadata docker client: %w", err)
		}
		defer cli.Close()
		opts.Docker = cli
	}
	p := primaryPlatform(opts.Platforms)

	if ref == "scratch" {
		return &oci.ImageConfig{}, nil
	}
	if opts.Docker != nil {
		img, err := opts.Docker.ImageInspect(ctx, ref)
		if err == nil {
			if len(opts.Platforms) == 0 || img.Os == p.OS && img.Architecture == p.Arch && (p.Variant == "" || img.Variant == p.Variant) {
				cfg := &oci.ImageConfig{}
				if img.Config != nil {
					cfg.Config.User = img.Config.User
					cfg.Config.Env = img.Config.Env
					cfg.Config.Labels = img.Config.Labels
				}
				return cfg, nil
			}
		} else if !dockerclient.IsErrNotFound(err) && !dockerclient.IsErrConnectionFailed(err) {
			return nil, err
		}
	}
	if opts.SkipImageMetaResolve {
		return &oci.ImageConfig{}, nil
	}
	parsed, err := oci.ParseReference(ref)
	if err != nil {
		return nil, err
	}
	return b.Resolver.OCI.FetchImageConfig(parsed, oci.Platform{OS: p.OS, Architecture: p.Arch, Variant: p.Variant})
}
