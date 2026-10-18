// relationships:
//
//	references: [config, host, lifecycle, metadata]
package cli

import (
	"context"
	"encoding/json"
	"errors"

	dockerclient "github.com/docker/docker/client"

	"github.com/wyrd-company/devcontainer-builder/pkg/builder"
	hostdocker "github.com/wyrd-company/devcontainer-builder/pkg/host/docker"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/dockercompose"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

func (a *application) mergedConfiguration(ctx context.Context, dc *spec.DevContainer, workspace, id string, configuration map[string]any) (metadata.Merged, error) {
	// Reuse the loaded/substituted snapshot, including extension fields.
	raw, err := json.Marshal(configuration)
	if err != nil {
		return metadata.Merged{}, err
	}
	var substituted spec.DevContainer
	if err := json.Unmarshal(raw, &substituted); err != nil {
		return metadata.Merged{}, err
	}
	substituted.Path, substituted.Raw = dc.Path, raw
	cli, err := newDockerClient()
	if err != nil {
		return metadata.Merged{}, err
	}
	defer cli.Close()
	entries, labels, err := a.readMetadata(ctx, cli, &substituted, workspace, id)
	if err != nil {
		return metadata.Merged{}, err
	}
	sub := substitutionContext(&substituted, workspace, labels)
	entries, err = metadata.Substitute(entries, func(s string) string { return substitute.Substitute(sub, s) })
	if err != nil {
		return metadata.Merged{}, err
	}
	return metadata.Merge(&substituted, entries), nil
}

func (a *application) readMetadata(ctx context.Context, cli *dockerclient.Client, dc *spec.DevContainer, workspace, id string) ([]metadata.Entry, map[string]string, error) {
	containerID, err := a.findContainer(ctx, cli, dc, workspace, id)
	if err == nil {
		info, err := cli.ContainerInspect(ctx, containerID)
		if err != nil {
			return nil, nil, err
		}
		var labels map[string]string
		if info.Config != nil {
			labels = info.Config.Labels
		}
		entries, err := a.containerMetadata(dc, labels)
		return entries, labels, err
	}
	if id != "" || !errors.Is(err, hostdocker.ErrContainerNotFound) && !errors.Is(err, dockercompose.ErrProjectNotFound) {
		return nil, nil, err
	}
	base, err := metadataBaseSpec(ctx, dc, workspace)
	if err != nil {
		return nil, nil, err
	}
	entries, err := builder.New().BaseMetadata(ctx, base, builder.Options{Docker: cli, Logger: a.logf})
	if err != nil {
		return nil, nil, err
	}
	resolved, cleanup, err := a.resolveFeatures(dc)
	if err != nil {
		return nil, nil, err
	}
	defer cleanup()
	return metadata.Collect(entries, resolved, dc), nil, nil
}

func metadataBaseSpec(ctx context.Context, dc *spec.DevContainer, workspace string) (*spec.DevContainer, error) {
	if dc.Mode() != spec.ModeCompose {
		return dc, nil
	}
	project, err := dockercompose.LoadProject(ctx, dockercompose.LoadInputs{Spec: dc, Workspace: workspace, LocalEnv: envMap()})
	if err != nil {
		return nil, err
	}
	service := project.Services[dc.Service]
	out := *dc
	out.Image, out.Dockerfile, out.Build = service.Image, "", nil
	if service.Build != nil {
		args := map[string]string{}
		for key, value := range service.Build.Args {
			if value != nil {
				args[key] = *value
			}
		}
		out.Image = ""
		out.Build = &spec.Build{Context: service.Build.Context, Dockerfile: service.Build.Dockerfile, Target: service.Build.Target, Args: args}
		if out.Build.Dockerfile == "" {
			out.Build.Dockerfile = "Dockerfile"
		}
	}
	return &out, nil
}

func (a *application) containerMetadata(dc *spec.DevContainer, labels map[string]string) ([]metadata.Entry, error) {
	entries := metadata.Read(labels, a.logf)
	if len(entries) > 0 {
		return metadata.Update(entries, dc), nil
	}
	resolved, cleanup, err := a.resolveFeatures(dc)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	return metadata.Collect(nil, resolved, dc), nil
}

// effectiveContainerConfig supplies metadata-backed user/env settings to exec
// and result reporting. Lifecycle collection is handled by containerMetadata.
func effectiveContainerConfig(dc *spec.DevContainer, labels map[string]string) *spec.DevContainer {
	entries := metadata.Read(labels, nil)
	if len(entries) == 0 {
		return dc
	}
	return metadata.Merge(dc, metadata.Update(entries, dc)).DevContainer
}
