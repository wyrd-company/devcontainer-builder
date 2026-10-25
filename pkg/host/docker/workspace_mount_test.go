// relationships:
//   references: [spawn, up, metadata]

package docker

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/docker/docker/api/types/mount"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

func TestEmptyWorkspaceMount(t *testing.T) {
	for _, raw := range []string{`{"image":"alpine:3.20","workspaceMount":""}`, `{"image":"alpine:3.20","workspaceMount":"${localEnv:SAMPLE_MOUNT}"}`} {
		t.Run(raw, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "devcontainer.json")
			if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
				t.Fatal(err)
			}
			dc, err := spec.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			dc.Mounts = []any{"type=bind,source=/host/data,target=/data"}
			dc, _, err = runtimeMetadata(dc, metadata.Collect(nil, nil, dc), substitute.Context{LocalEnv: map[string]string{}})
			if err != nil {
				t.Fatal(err)
			}
			cfg, host, err := buildContainerConfig(dc, nil, "alpine", "/tmp", "/host/sample", "")
			if err != nil {
				t.Fatal(err)
			}
			want := []mount.Mount{{Type: mount.TypeBind, Source: "/host/data", Target: "/data"}}
			if !reflect.DeepEqual(host.Mounts, want) {
				t.Fatalf("mounts = %+v, want %+v", host.Mounts, want)
			}
			root := mount.Mount{Type: mount.TypeBind, Source: "/host", Target: "/workspaces"}
			extra := mount.Mount{Type: mount.TypeVolume, Source: "sample-cache", Target: "/cache"}
			applyRequestIdentity(cfg, host, dc, UpRequest{WorkspaceMount: &root, Mounts: []mount.Mount{extra}})
			want = append(want, extra)
			if !reflect.DeepEqual(host.Mounts, want) {
				t.Fatalf("request mounts = %+v, want %+v", host.Mounts, want)
			}
		})
	}
}
