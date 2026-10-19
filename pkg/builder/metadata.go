// relationships:
//
//	references: [dockerfileuser, metadata, oci]
package builder

import (
	"context"
	"fmt"

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
	imageConfig, closeEngine, err := b.imageConfigResolver(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("base metadata %w", err)
	}
	defer closeEngine()
	p := primaryPlatform(opts.Platforms)
	var cfg *oci.ImageConfig
	if df := dc.DockerfileFile(); df != "" {
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

// imageConfigResolver returns the lookup of base image configs for the
// build's primary platform. An image present in the Docker daemon for that
// platform is read through the Engine API (opts.Docker, or a default
// client), so that locally built images resolve. Any other image is read
// from its registry by the resolver's OCI client, with the Docker client
// configuration's credentials, unless SkipImageMetaResolve is set. The
// returned close releases a default Engine client.
func (b *Builder) imageConfigResolver(ctx context.Context, opts Options) (func(string) (*oci.ImageConfig, error), func(), error) {
	closeEngine := func() {}
	if opts.Docker == nil && !opts.SkipImageMetaResolve {
		cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
		if err != nil {
			return nil, nil, fmt.Errorf("docker client: %w", err)
		}
		closeEngine = func() { _ = cli.Close() }
		opts.Docker = cli
	}
	p := primaryPlatform(opts.Platforms)
	resolve := func(ref string) (*oci.ImageConfig, error) {
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
	return resolve, closeEngine, nil
}
