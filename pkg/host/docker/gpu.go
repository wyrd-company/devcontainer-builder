// relationships:
//   references: [up, ../../../reference/cli/src/spec-node/utils]

package docker

import (
	"context"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// applyGPUAvailability adds the implicit host requirement after explicit runArgs
// have been parsed. Explicit device selection always remains authoritative.
func (h *Host) applyGPUAvailability(ctx context.Context, dc *spec.DevContainer, mode string, host *container.HostConfig) error {
	if mode == "" {
		mode = "detect"
	}
	switch mode {
	case "all", "detect", "none":
	default:
		return errorf("GPU availability must be all, detect, or none")
	}
	gpu := dc.HostRequirements["gpu"]
	if gpu == nil || gpu == false || gpu == "" || len(host.DeviceRequests) > 0 {
		return nil
	}
	available := mode == "all"
	if mode == "detect" {
		info, err := h.client.Info(ctx)
		if err != nil {
			return errorf("detect GPU support: %w", err)
		}
		available = strings.Contains(info.Runtimes["nvidia"].Path, "nvidia-container-runtime")
	}
	if available {
		host.DeviceRequests = append(host.DeviceRequests, container.DeviceRequest{Count: -1, Capabilities: [][]string{{"gpu"}}})
	} else if gpu != "optional" {
		h.logf("warning: No GPU support found yet a GPU was required - consider marking it as optional")
	}
	return nil
}
