// relationships:
//   references: [up, ../gpu/availability]

package docker

import (
	"context"
	"github.com/docker/docker/api/types/container"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/gpu"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func (h *Host) applyGPUAvailability(ctx context.Context, dc *spec.DevContainer, mode string, host *container.HostConfig) error {
	requested, err := gpu.Requested(ctx, h.client, dc.HostRequirements["gpu"], mode, len(host.DeviceRequests) > 0, h.logf)
	if err != nil {
		return err
	}
	if requested {
		host.DeviceRequests = append(host.DeviceRequests, container.DeviceRequest{Count: -1, Capabilities: [][]string{{"gpu"}}})
	}
	return nil
}
