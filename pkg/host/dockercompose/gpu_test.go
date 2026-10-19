// relationships:
//   references: [gpu, spawn]

package dockercompose

import (
	"context"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"reflect"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/container"
)

func TestComposeGPUDeviceSelection(t *testing.T) {
	for _, tc := range []struct {
		name   string
		device types.DeviceRequest
		want   container.DeviceRequest
	}{
		{"omitted-count", types.DeviceRequest{Capabilities: []string{"gpu"}}, container.DeviceRequest{Count: -1, Capabilities: [][]string{{"gpu"}}}},
		{"count", types.DeviceRequest{Capabilities: []string{"gpu"}, Count: 2}, container.DeviceRequest{Count: 2, Capabilities: [][]string{{"gpu"}}}},
		{"selection", types.DeviceRequest{Capabilities: []string{"gpu", "utility"}, Driver: "nvidia", IDs: []string{"GPU-example"}, Options: types.Mapping{"sample": "value"}}, container.DeviceRequest{Driver: "nvidia", DeviceIDs: []string{"GPU-example"}, Capabilities: [][]string{{"gpu", "utility"}}, Options: map[string]string{"sample": "value"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := types.ServiceConfig{Deploy: &types.DeployConfig{Resources: types.Resources{Reservations: &types.Resource{Devices: []types.DeviceRequest{tc.device}}}}}
			if got := composeDeviceRequests(svc); !reflect.DeepEqual(got, []container.DeviceRequest{tc.want}) {
				t.Fatalf("requests=%+v want %+v", got, tc.want)
			}
		})
	}
}

func TestComposeGPUReservationKeepsResources(t *testing.T) {
	existing := types.DeviceRequest{Capabilities: []string{"sample"}, Count: 2}
	svc := types.ServiceConfig{Deploy: &types.DeployConfig{Resources: types.Resources{Reservations: &types.Resource{MemoryBytes: 128, Devices: []types.DeviceRequest{existing}}}}}
	proj := &types.Project{Services: types.Services{"app": svc, "other": {Image: "busybox"}}}
	dc := &spec.DevContainer{Service: "app", HostRequirements: map[string]any{"gpu": true}}
	if err := New(nil, Options{}).applyGPUAvailability(context.Background(), proj, dc, "all"); err != nil {
		t.Fatal(err)
	}
	reservations := proj.Services["app"].Deploy.Resources.Reservations
	if reservations.MemoryBytes != 128 || len(reservations.Devices) != 2 || !reflect.DeepEqual(reservations.Devices[0], existing) || !reflect.DeepEqual(reservations.Devices[1], types.DeviceRequest{Capabilities: []string{"gpu"}, Count: -1}) || proj.Services["other"].Deploy != nil {
		t.Fatalf("project changed unexpectedly: %+v", proj)
	}
}
