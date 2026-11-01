// relationships:
//
//	references: [project, compose, ../../spec/mounts]
package kubernetes

import (
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"strings"
	"testing"
)

func TestProjectRejectsObjectMountExtensions(t *testing.T) {
	dc := &spec.DevContainer{Image: "alpine", Mounts: []any{map[string]any{"type": "bind", "target": "/data", "readOnly": true}}}
	_, err := Project(ProjectRequest{Spec: dc, Image: "alpine"})
	if err == nil || !strings.Contains(err.Error(), `unsupported object property "readOnly"`) {
		t.Fatalf("Project: %v", err)
	}
	dc.Image = ""
	dc.Service = "app"
	dc.DockerComposeFile = spec.StringOrSlice{"compose.yml"}
	_, err = ProjectCompose(ComposeProjectRequest{Spec: dc})
	if err == nil || !strings.Contains(err.Error(), `unsupported object property "readOnly"`) {
		t.Fatalf("ProjectCompose: %v", err)
	}
}
