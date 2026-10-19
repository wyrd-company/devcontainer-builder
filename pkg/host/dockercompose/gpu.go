// relationships:
//   references: [up, spawn, ../gpu/availability]

package dockercompose

import (
	"context"
	"slices"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/container"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/gpu"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// applyGPUAvailability layers the implicit reservation onto the resolved primary
// service, preserving explicit selections and other resource reservations.
func (h *Host) applyGPUAvailability(ctx context.Context, proj *types.Project, dc *spec.DevContainer, mode string) error {
	svc := proj.Services[dc.Service]
	explicit := len(svc.Gpus) > 0
	if svc.Deploy != nil && svc.Deploy.Resources.Reservations != nil {
		for _, device := range svc.Deploy.Resources.Reservations.Devices {
			explicit = explicit || slices.Contains(device.Capabilities, "gpu")
		}
	}
	requested, err := gpu.Requested(ctx, h.client, dc.HostRequirements["gpu"], mode, explicit, h.logf)
	if err != nil {
		return err
	}
	if requested {
		if svc.Deploy == nil {
			svc.Deploy = &types.DeployConfig{}
		}
		if svc.Deploy.Resources.Reservations == nil {
			svc.Deploy.Resources.Reservations = &types.Resource{}
		}
		svc.Deploy.Resources.Reservations.Devices = append(svc.Deploy.Resources.Reservations.Devices, types.DeviceRequest{Capabilities: []string{"gpu"}, Count: -1})
		proj.Services[dc.Service] = svc
	}
	return nil
}

// composeDeviceRequests preserves Compose GPU device reservations at the
// Engine create boundary. An omitted count with no IDs means all devices.
func composeDeviceRequests(svc types.ServiceConfig) []container.DeviceRequest {
	var requests []container.DeviceRequest
	appendRequest := func(device types.DeviceRequest) {
		count := int(device.Count)
		if count == 0 && len(device.IDs) == 0 {
			count = -1
		}
		requests = append(requests, container.DeviceRequest{Driver: device.Driver, Count: count, DeviceIDs: device.IDs, Capabilities: [][]string{device.Capabilities}, Options: device.Options})
	}
	if svc.Deploy != nil && svc.Deploy.Resources.Reservations != nil {
		for _, device := range svc.Deploy.Resources.Reservations.Devices {
			if slices.Contains(device.Capabilities, "gpu") {
				appendRequest(device)
			}
		}
	}
	for _, device := range svc.Gpus {
		if !slices.Contains(device.Capabilities, "gpu") {
			device.Capabilities = append(device.Capabilities, "gpu")
		}
		appendRequest(device)
	}
	return requests
}
