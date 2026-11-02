// relationships:
//
//	references: [project, compose]
package kubernetes

import (
	composetypes "github.com/compose-spec/compose-go/v2/types"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"strings"
	"testing"
)

func TestReaderEnvironmentProjectionGuard(t *testing.T) {
	dc := &spec.DevContainer{Image: "alpine", ContainerEnv: map[string]any{"VALUE": true}}
	_, err := Project(ProjectRequest{Spec: dc, Image: "alpine", LocalWorkspace: "/workspace"})
	if err == nil || !strings.Contains(err.Error(), "containerEnv") {
		t.Fatalf("environment guard %v", err)
	}
}
func TestReaderEnvironmentComposeProjectionGuard(t *testing.T) {
	dc := &spec.DevContainer{DockerComposeFile: spec.StringOrSlice{}, Service: "app", ContainerEnv: map[string]any{"VALUE": true}}
	proj := &composetypes.Project{Name: "sample", Services: composetypes.Services{"app": {ContainerSpec: composetypes.ContainerSpec{Image: "alpine"}}}}
	_, err := ProjectCompose(ComposeProjectRequest{Spec: dc, Project: proj, LocalWorkspace: "/workspace"})
	if err == nil || !strings.Contains(err.Error(), "containerEnv") {
		t.Fatalf("environment guard %v", err)
	}
}
