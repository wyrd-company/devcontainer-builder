// relationships:
//
//	references: [create_only, runargs]
package docker

import (
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"reflect"
	"strings"
	"testing"
)

func TestStrictCreateMounts(t *testing.T) {
	host := &container.HostConfig{Binds: []string{"/sample/source:/sample/target:ro,rslave,cached", "sample-volume:/volume:nocopy"}}
	if err := strictCreateMounts(host); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(host.Binds, []string{"sample-volume:/volume:nocopy"}) || len(host.Mounts) != 1 {
		t.Fatalf("host=%+v", host)
	}
	m := host.Mounts[0]
	if m.Type != mount.TypeBind || m.Source != "/sample/source" || m.Target != "/sample/target" || !m.ReadOnly || m.Consistency != mount.ConsistencyCached || m.BindOptions == nil || m.BindOptions.Propagation != mount.PropagationRSlave || m.BindOptions.CreateMountpoint {
		t.Fatalf("mount=%+v", m)
	}
}

func TestStrictCreateMountErrors(t *testing.T) {
	for _, option := range []string{"z", "Z"} {
		raw := "/sample/source:/sample/target:" + option
		host := &container.HostConfig{Binds: []string{raw}}
		err := strictCreateMounts(host)
		if err == nil || !strings.Contains(err.Error(), raw) || !strings.Contains(err.Error(), "ordinary up still supports it") {
			t.Fatalf("err=%v", err)
		}
		if !reflect.DeepEqual(host.Binds, []string{raw}) || len(host.Mounts) != 0 {
			t.Fatal("rejected request modified")
		}
	}
	host := &container.HostConfig{Mounts: []mount.Mount{{Type: mount.TypeBind, Source: "/sample/source", Target: "/sample/target", BindOptions: &mount.BindOptions{CreateMountpoint: true}}}}
	err := strictCreateMounts(host)
	if err == nil || !strings.Contains(err.Error(), "bind-create-src") || !strings.Contains(err.Error(), "/sample/source") || !strings.Contains(err.Error(), "ordinary up still supports it") {
		t.Fatalf("err=%v", err)
	}
}
