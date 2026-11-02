// relationships:
//
//	references: [substitute, ../runner/env]
package dockercompose

import (
	"context"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/runner"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"strings"
	"testing"
)

func TestReaderRemoteNullLifecycle(t *testing.T) {
	converted := stringMap(map[string]any{"NULL": nil, "EMPTY": "", "STRING": "text"})
	env := runner.LifecycleEnv(map[string]string{"NULL": "probe"}, map[string]string{"NULL": "caller"}, converted, nil)
	if env["NULL"] != "null" || env["EMPTY"] != "" || env["STRING"] != "text" {
		t.Fatalf("runtime env: %v", env)
	}
}

func TestReaderEnvironmentUpGuard(t *testing.T) {
	h := &Host{}
	_, err := h.Up(context.Background(), UpRequest{Spec: &spec.DevContainer{DockerComposeFile: spec.StringOrSlice{}, Service: "app", ContainerEnv: map[string]any{"VALUE": true}}, LocalWorkspace: "/workspace", PrimaryImage: "alpine"})
	if err == nil || !strings.Contains(err.Error(), "containerEnv") {
		t.Fatalf("environment guard: %v", err)
	}
}
