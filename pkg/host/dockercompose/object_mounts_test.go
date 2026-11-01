// relationships:
//
//	references: [up, ../../spec/mounts]
package dockercompose

import (
	"context"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"strings"
	"testing"
)

func TestUpRejectsObjectMountBeforeDocker(t *testing.T) {
	dc := &spec.DevContainer{DockerComposeFile: spec.StringOrSlice{"compose.yml"}, Service: "app", Mounts: []any{map[string]any{"type": "bind", "target": "/data", "readOnly": true}}}
	_, err := New(nil, Options{}).Up(context.Background(), UpRequest{Spec: dc, LocalWorkspace: "/sample"})
	if err == nil || !strings.Contains(err.Error(), `unsupported object property "readOnly"`) {
		t.Fatalf("Up: %v", err)
	}
}
