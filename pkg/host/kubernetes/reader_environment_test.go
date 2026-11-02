// relationships:
//
//	references: [substitute, ../runner/env]
package kubernetes

import (
	"context"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/runner"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"strings"
	"testing"
)

func TestReaderRemoteNullLifecycle(t *testing.T) {
	converted := stringMapFromAny(map[string]any{"NULL": nil, "EMPTY": "", "STRING": "text"})
	env := runner.LifecycleEnv(map[string]string{"NULL": "probe"}, map[string]string{"NULL": "caller"}, converted, nil)
	if env["NULL"] != "null" || env["EMPTY"] != "" || env["STRING"] != "text" {
		t.Fatalf("runtime env: %v", env)
	}
}

func TestReaderEnvironmentUpGuard(t *testing.T) {
	h := &Host{}
	_, err := h.Up(context.Background(), UpRequest{Spec: &spec.DevContainer{Image: "alpine", ContainerEnv: map[string]any{"VALUE": true}}, LocalWorkspace: "/workspace", Image: "alpine"})
	if err == nil || !strings.Contains(err.Error(), "containerEnv") {
		t.Fatalf("environment guard: %v", err)
	}
}
