// relationships:
//   references: [../../pkg/registryauth, ../../pkg/oci]

// Package dockerimage shares native Engine image pulls between hosts.
package dockerimage

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"time"

	"github.com/containerd/platforms"
	imagetypes "github.com/docker/docker/api/types/image"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
)

type RegistryAuth interface{ EngineAuth(string) (string, error) }

// Pull consumes the complete Engine pull stream, including stream errors.
func Pull(ctx context.Context, cli *dockerclient.Client, ref string, auth RegistryAuth, out io.Writer) error {
	var encoded string
	if auth != nil {
		var err error
		encoded, err = auth.EngineAuth(ref)
		if err != nil {
			return fmt.Errorf("pull image %s: %w", ref, err)
		}
	}
	rc, err := cli.ImagePull(ctx, ref, imagetypes.PullOptions{RegistryAuth: encoded})
	if err != nil {
		return fmt.Errorf("pull image %s: %w", ref, err)
	}
	defer rc.Close()
	if out == nil {
		out = io.Discard
	}
	if err := jsonmessage.DisplayJSONMessagesStream(rc, out, 0, false, nil); err != nil {
		return fmt.Errorf("pull image %s: %w", ref, err)
	}
	return nil
}

type Config struct {
	Labels          map[string]string
	Entrypoint, Cmd []string
}

func localConfig(img imagetypes.InspectResponse) *Config {
	if img.Config == nil {
		return nil
	}
	return &Config{Labels: img.Config.Labels, Entrypoint: img.Config.Entrypoint, Cmd: img.Config.Cmd}
}

// Ensure pulls an image only when the Engine reports it missing.
func Ensure(ctx context.Context, cli *dockerclient.Client, ref string, auth RegistryAuth, out io.Writer) error {
	if _, err := cli.ImageInspect(ctx, ref); err == nil {
		return nil
	} else if !dockerclient.IsErrNotFound(err) {
		return fmt.Errorf("inspect image %s: %w", ref, err)
	}
	return Pull(ctx, cli, ref, auth, out)
}

// InspectConfig follows upstream inspectDockerImage: local, registry, then
// pull with five attempts at one-second intervals and inspect locally again.
func InspectConfig(ctx context.Context, cli *dockerclient.Client, ref, platform string, auth RegistryAuth, out io.Writer) (*Config, error) {
	if img, err := cli.ImageInspect(ctx, ref); err == nil {
		return localConfig(img), nil
	}
	p := oci.Platform{OS: runtime.GOOS, Architecture: runtime.GOARCH}
	if platform != "" {
		parsed, err := platforms.Parse(platform)
		if err != nil {
			return nil, err
		}
		p = oci.Platform{OS: parsed.OS, Architecture: parsed.Architecture, Variant: parsed.Variant}
	} else if info, err := cli.Info(ctx); err == nil {
		p.OS, p.Architecture = info.OSType, platforms.Normalize(ocispec.Platform{Architecture: info.Architecture}).Architecture
	}
	registry := oci.NewClient()
	registry.Auth, _ = auth.(oci.CredentialSource)
	if r, err := oci.ParseReference(ref); err == nil {
		if cfg, err := registry.FetchImageConfig(r, p); err == nil {
			return &Config{Labels: cfg.Config.Labels, Entrypoint: cfg.Config.Entrypoint, Cmd: cfg.Config.Cmd}, nil
		}
	}
	for attempt := 0; ; attempt++ {
		err := Pull(ctx, cli, ref, auth, out)
		if err == nil {
			break
		}
		if attempt == 4 {
			return nil, err
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	img, err := cli.ImageInspect(ctx, ref)
	if err != nil {
		return nil, err
	}
	return localConfig(img), nil
}
