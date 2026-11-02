// relationships:
//   references: [runargs, spawn]

package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	mobycontainer "github.com/moby/moby/api/types/container"
	mobyclient "github.com/moby/moby/client"
)

// createWithRunControls preserves caller connection policy through an injected
// native operation. Inspect confirms that the daemon retained the control.
func (h *Host) createWithRunControls(ctx context.Context, controls *runControls, cfg *container.Config, host *container.HostConfig, networking *network.NetworkingConfig, name string) (container.CreateResponse, error) {
	if controls.Umask == nil {
		return h.client.ContainerCreate(ctx, cfg, host, networking, nil, name)
	}
	if h.opts.NewCreateClient == nil {
		return container.CreateResponse{}, fmt.Errorf("runArgs: --umask requires Options.NewCreateClient with a native Moby client")
	}
	data, err := json.Marshal(container.CreateRequest{Config: cfg, HostConfig: host, NetworkingConfig: networking})
	if err != nil {
		return container.CreateResponse{}, err
	}
	var request mobycontainer.CreateRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return container.CreateResponse{}, fmt.Errorf("create options: %w", err)
	}
	request.HostConfig.Umask = controls.Umask
	native, err := h.opts.NewCreateClient()
	if err != nil {
		return container.CreateResponse{}, err
	}
	defer native.Close()
	result, err := native.ContainerCreate(ctx, mobyclient.ContainerCreateOptions{Config: request.Config, HostConfig: request.HostConfig, NetworkingConfig: request.NetworkingConfig, Name: name})
	if err != nil {
		return container.CreateResponse{}, err
	}
	inspected, err := native.ContainerInspect(ctx, result.ID, mobyclient.ContainerInspectOptions{})
	if err == nil && (inspected.Container.HostConfig == nil || inspected.Container.HostConfig.Umask == nil || *inspected.Container.HostConfig.Umask != *controls.Umask) {
		err = fmt.Errorf("daemon did not retain requested umask %o", *controls.Umask)
	}
	if err != nil {
		_, cleanupErr := native.ContainerRemove(context.WithoutCancel(ctx), result.ID, mobyclient.ContainerRemoveOptions{Force: true})
		if cleanupErr != nil {
			return container.CreateResponse{}, fmt.Errorf("confirm umask for container %s: %w; remove failed: %v", result.ID, err, cleanupErr)
		}
		return container.CreateResponse{}, fmt.Errorf("confirm umask: %w", err)
	}
	return container.CreateResponse{ID: result.ID, Warnings: result.Warnings}, nil
}
