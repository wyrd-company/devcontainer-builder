// relationships:
//
//	references: [up, spawn, runargs]
package docker

import (
	"fmt"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"path"
	"strings"
)

// strictCreateMounts uses the daemon's Linux volume grammar without performing
// local filesystem checks: bind sources belong to the Engine host. Named volumes
// keep their original Bind representation and driver/copy semantics.
func strictCreateMounts(host *container.HostConfig) error {
	var binds []string
	converted := append([]mount.Mount(nil), host.Mounts...)
	for _, m := range converted {
		if m.Type == mount.TypeBind && m.BindOptions != nil && m.BindOptions.CreateMountpoint {
			return fmt.Errorf("mount %q -> %q: create-only does not support bind-create-src; ordinary up still supports it", m.Source, m.Target)
		}
	}
	for _, raw := range host.Binds {
		// Only absolute Linux bind sources require conversion. Volume binds retain
		// their raw representation and are validated by the Engine.
		parts := strings.SplitN(raw, ":", 4)
		if len(parts) < 2 || !path.IsAbs(parts[0]) {
			binds = append(binds, raw)
			continue
		}
		m, err := strictBindMount(raw, parts)
		if err != nil {
			return fmt.Errorf("create-only mount %q: %w", raw, err)
		}
		converted = append(converted, m)
	}
	host.Binds, host.Mounts = binds, converted
	return nil
}

// strictBindMount projects Linux -v bind syntax without importing daemon mount
// setup code, which depends on Linux-only safepath operations. Its mode groups
// follow Docker v28.5.2 volume/mounts/linux_parser.go; the Engine validates paths.
func strictBindMount(raw string, parts []string) (mount.Mount, error) {
	m := mount.Mount{Type: mount.TypeBind, Source: parts[0], Target: parts[1]}
	if len(parts) > 3 || m.Target == "" || !path.IsAbs(strings.ReplaceAll(m.Target, `\`, "/")) || path.Clean(strings.ReplaceAll(m.Target, `\`, "/")) == "/" {
		return m, fmt.Errorf("invalid bind specification")
	}
	if len(parts) < 3 || parts[2] == "" {
		return m, nil
	}
	seen := map[string]bool{}
	for _, option := range strings.Split(parts[2], ",") {
		var group string
		switch option {
		case "ro", "rw":
			group = "access"
			m.ReadOnly = option == "ro"
		case "private", "rprivate", "slave", "rslave", "shared", "rshared":
			group = "propagation"
			m.BindOptions = &mount.BindOptions{Propagation: mount.Propagation(option)}
		case "consistent", "cached", "delegated":
			group = "consistency"
			m.Consistency = mount.Consistency(option)
		case "z", "Z":
			return m, fmt.Errorf("mount %q: create-only does not support SELinux relabel option %q; ordinary up still supports it", raw, option)
		default:
			return m, fmt.Errorf("invalid bind mode %q", option)
		}
		if seen[group] {
			return m, fmt.Errorf("duplicate bind mode %q", option)
		}
		seen[group] = true
	}
	return m, nil
}
