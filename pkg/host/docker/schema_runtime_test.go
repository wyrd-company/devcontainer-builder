// relationships:
//
//	references: [spawn, ports, ../../spec/spec, ../../../docs/audits/schema-conformance]
package docker

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/mount"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestSchemaAppPortPublishing(t *testing.T) {
	for _, tc := range []struct {
		raw      string
		ip, host string
	}{
		{`8080`, "127.0.0.1", "8080"},
		{`"8080"`, "", ""},
		{`"8080/udp"`, "", ""},
		{`"8080-8081"`, "", ""},
		{`"127.0.0.1:9080:8080"`, "127.0.0.1", "9080"},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			var value any
			if err := json.Unmarshal([]byte(tc.raw), &value); err != nil {
				t.Fatal(err)
			}
			cfg, host, _, err := buildContainerConfig(&spec.DevContainer{AppPort: value}, nil, "alpine", "", "", "")
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.ExposedPorts) == 0 {
				t.Fatal("no exposed ports")
			}
			for _, bs := range host.PortBindings {
				if len(bs) != 1 || bs[0].HostIP != tc.ip || bs[0].HostPort != tc.host {
					t.Fatalf("bindings: %+v", host.PortBindings)
				}
			}
		})
	}
}

func TestSchemaMountOptions(t *testing.T) {
	cases := []struct {
		name, raw string
		want      mount.Mount
	}{
		{"volume", "type=volume,source=sample-data,target=/data,volume-nocopy,volume-subpath=payload,volume-label=purpose=sample,volume-driver=local,volume-opt=type=tmpfs", mount.Mount{Type: mount.TypeVolume, Source: "sample-data", Target: "/data", VolumeOptions: &mount.VolumeOptions{NoCopy: true, Subpath: "payload", Labels: map[string]string{"purpose": "sample"}, DriverConfig: &mount.Driver{Name: "local", Options: map[string]string{"type": "tmpfs"}}}}},
		{"bind", "type=bind,src=/host/data,dst=/data,readonly,bind-propagation=rshared,bind-recursive=disabled,bind-create-src", mount.Mount{Type: mount.TypeBind, Source: "/host/data", Target: "/data", ReadOnly: true, BindOptions: &mount.BindOptions{Propagation: mount.PropagationRShared, NonRecursive: true, CreateMountpoint: true}}},
		{"csv", `type=volume,"source=sample,data",target=/data`, mount.Mount{Type: mount.TypeVolume, Source: "sample,data", Target: "/data"}},
		{"default-volume", "source=sample-data,target=/data", mount.Mount{Type: mount.TypeVolume, Source: "sample-data", Target: "/data"}},
		{"tmpfs", "type=tmpfs,target=/data,tmpfs-size=1m,tmpfs-mode=1770", mount.Mount{Type: mount.TypeTmpfs, Target: "/data", TmpfsOptions: &mount.TmpfsOptions{SizeBytes: 1048576, Mode: 01770}}},
		{"image", "type=image,source=alpine,target=/data,image-subpath=etc", mount.Mount{Type: mount.TypeImage, Source: "alpine", Target: "/data", ImageOptions: &mount.ImageOptions{Subpath: "etc"}}},
	}
	for _, tc := range cases {
		for _, route := range []string{"workspaceMount", "mounts", "runArgs"} {
			t.Run(tc.name+"/"+route, func(t *testing.T) {
				dc := &spec.DevContainer{}
				switch route {
				case "workspaceMount":
					dc.WorkspaceMount = tc.raw
				case "mounts":
					dc.Mounts = []any{tc.raw}
				case "runArgs":
					dc.RunArgs = []string{"--mount", tc.raw}
				}
				_, host, _, err := buildContainerConfig(dc, nil, "alpine", "", "", "")
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(host.Mounts, []mount.Mount{tc.want}) {
					t.Fatalf("got %+v; want %+v", host.Mounts, tc.want)
				}
			})
		}
	}
}

func TestSchemaInvalidMountErrors(t *testing.T) {
	for _, raw := range []string{"type=bind,source=/host/data", "type=bind,source=/host/data,target=/data,unknown-option=value", "type=volume,source=sample-data,target=/data,volume-nocopy=invalid", "type=bind,source=/host/data,target=/data,volume-nocopy"} {
		for _, route := range []string{"workspaceMount", "mounts", "runArgs"} {
			t.Run(raw+"/"+route, func(t *testing.T) {
				dc := &spec.DevContainer{}
				switch route {
				case "workspaceMount":
					dc.WorkspaceMount = raw
				case "mounts":
					dc.Mounts = []any{raw}
				case "runArgs":
					dc.RunArgs = []string{"--mount", raw}
				}
				_, _, _, err := buildContainerConfig(dc, nil, "alpine", "/workspace", "/host/sample", "")
				if err == nil || !strings.Contains(err.Error(), route) {
					t.Fatalf("missing contextual mount error: %v", err)
				}
			})
		}
	}
}

func TestSchemaObjectMountOptions(t *testing.T) {
	dc := &spec.DevContainer{Mounts: []any{map[string]any{"type": "volume", "source": "sample-data", "target": "/data,volume-nocopy,volume-subpath=payload"}}}
	_, host, _, err := buildContainerConfig(dc, nil, "alpine", "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(host.Mounts) != 1 || host.Mounts[0].VolumeOptions == nil || !host.Mounts[0].VolumeOptions.NoCopy || host.Mounts[0].VolumeOptions.Subpath != "payload" {
		t.Fatalf("mounts: %+v", host.Mounts)
	}
}
