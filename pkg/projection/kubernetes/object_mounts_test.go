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

func TestProjectObjectReadonlyTarget(t *testing.T) {
	for _, suffix := range []string{"readonly", "ro"} {
		t.Run(suffix, func(t *testing.T) {
			dc := &spec.DevContainer{Mounts: []any{map[string]any{"type": "bind", "source": "/host/data", "target": "/data," + suffix}}}
			result, err := Project(ProjectRequest{Spec: dc, Image: "alpine"})
			if err != nil {
				t.Fatal(err)
			}
			mounts := result.Pod.Spec.Containers[0].VolumeMounts
			if len(mounts) != 1 || mounts[0].MountPath != "/data" || !mounts[0].ReadOnly {
				t.Fatalf("mounts=%+v", mounts)
			}
		})
	}
}
