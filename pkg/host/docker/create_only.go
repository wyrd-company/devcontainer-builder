// relationships:
//
//	references: [up, spawn, runargs]
package docker

import (
	"fmt"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/volume/mounts"
	"strings"
)

// strictCreateMounts uses the daemon's Linux volume grammar without performing
// local filesystem checks: bind sources belong to the Engine host. Named volumes
// keep their original Bind representation and driver/copy semantics.
func strictCreateMounts(host *container.HostConfig) error {
	parser := mounts.NewLinuxParser()
	var binds []string
	converted := append([]mount.Mount(nil), host.Mounts...)
	for _, m := range converted {
		if m.Type == mount.TypeBind && m.BindOptions != nil && m.BindOptions.CreateMountpoint {
			return fmt.Errorf("mount %q -> %q: create-only does not support bind-create-src; ordinary up still supports it", m.Source, m.Target)
		}
	}
	for _, raw := range host.Binds {
		parsed, err := parser.ParseMountRaw(raw, host.VolumeDriver)
		if err != nil {
			return fmt.Errorf("create-only mount %q: %w", raw, err)
		}
		if parsed.Type != mount.TypeBind {
			binds = append(binds, raw)
			continue
		}
		m := parsed.Spec
		for _, option := range strings.Split(parsed.Mode, ",") {
			switch option {
			case "z", "Z":
				return fmt.Errorf("mount %q: create-only does not support SELinux relabel option %q; ordinary up still supports it", raw, option)
			case "consistent", "cached", "delegated":
				m.Consistency = mount.Consistency(option)
			}
		}
		converted = append(converted, m)
	}
	host.Binds, host.Mounts = binds, converted
	return nil
}
