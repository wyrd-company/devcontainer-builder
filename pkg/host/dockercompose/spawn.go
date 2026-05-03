package dockercompose

import (
	"fmt"
	"sort"
	"strconv"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/strslice"
	"github.com/docker/go-connections/nat"
)

// translation holds the docker engine API call shape for a single
// compose service.
type translation struct {
	Name       string
	Config     *container.Config
	HostConfig *container.HostConfig
	NetConfig  *network.NetworkingConfig
}

// translateService converts a compose ServiceConfig into the
// container.Config / HostConfig / NetworkingConfig triple that
// ContainerCreate expects.
//
// Coverage limits (see package doc): image/cluster volume types are
// rejected. tmpfs is honored via HostConfig.Tmpfs (compose tmpfs
// volumes return an error from translateVolumes — use the service-
// level `tmpfs:` field instead). Configs and secrets are honored when
// a non-nil materializer is supplied; the materializer resolves each
// to an on-host file the daemon then bind-mounts read-only into the
// container.
func translateService(
	proj *types.Project,
	svc types.ServiceConfig,
	containerName string,
	labels map[string]string,
	networkNames map[string]string,
	volumeNames map[string]string,
	materializer *objectMaterializer,
) (*translation, error) {
	cfg := &container.Config{
		Image:      svc.Image,
		Hostname:   svc.Hostname,
		Domainname: svc.DomainName,
		User:       svc.User,
		Labels:     labels,
		WorkingDir: svc.WorkingDir,
		Tty:        svc.Tty,
		OpenStdin:  svc.StdinOpen,
		StopSignal: svc.StopSignal,
	}
	if len(svc.Entrypoint) > 0 {
		cfg.Entrypoint = strslice.StrSlice(svc.Entrypoint)
	}
	if len(svc.Command) > 0 {
		cfg.Cmd = strslice.StrSlice(svc.Command)
	}
	cfg.Env = sortedEnv(svc.Environment)

	exposed := nat.PortSet{}
	bindings := nat.PortMap{}
	for _, p := range svc.Ports {
		port, err := nat.NewPort(orDefault(p.Protocol, "tcp"), strconv.FormatUint(uint64(p.Target), 10))
		if err != nil {
			return nil, fmt.Errorf("port %d/%s: %w", p.Target, p.Protocol, err)
		}
		exposed[port] = struct{}{}
		if p.Published != "" {
			bindings[port] = append(bindings[port], nat.PortBinding{
				HostIP:   p.HostIP,
				HostPort: p.Published,
			})
		}
	}
	if len(exposed) > 0 {
		cfg.ExposedPorts = exposed
	}

	host := &container.HostConfig{
		AutoRemove:     false,
		Privileged:     svc.Privileged,
		ReadonlyRootfs: svc.ReadOnly,
		CapAdd:         svc.CapAdd,
		CapDrop:        svc.CapDrop,
		SecurityOpt:    svc.SecurityOpt,
		NetworkMode:    container.NetworkMode(svc.NetworkMode),
		IpcMode:        container.IpcMode(svc.Ipc),
		PidMode:        container.PidMode(svc.Pid),
		Init:           svc.Init,
		ShmSize:        int64(svc.ShmSize),
		PortBindings:   bindings,
		ExtraHosts:     svc.ExtraHosts.AsList(":"),
		DNS:            svc.DNS,
		DNSSearch:      svc.DNSSearch,
		DNSOptions:     svc.DNSOpts,
		GroupAdd:       svc.GroupAdd,
		Sysctls:        svc.Sysctls,
		Tmpfs:          tmpfsList(svc.Tmpfs),
	}
	if svc.Restart != "" {
		host.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyMode(svc.Restart)}
	}

	mounts, err := translateVolumes(svc.Volumes, volumeNames)
	if err != nil {
		return nil, err
	}
	objMounts, err := translateConfigsAndSecrets(proj, svc, materializer)
	if err != nil {
		return nil, err
	}
	host.Mounts = append(mounts, objMounts...)

	netCfg := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{},
	}
	keys := make([]string, 0, len(svc.Networks))
	for k := range svc.Networks {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ep := svc.Networks[k]
		name := networkNames[k]
		if name == "" {
			name = resourceName(proj.Name, k, "")
		}
		es := &network.EndpointSettings{}
		// Always make the bare service name resolvable inside the
		// network — compose's per-service hostname guarantee.
		es.Aliases = append(es.Aliases, svc.Name)
		if ep != nil {
			es.Aliases = append(es.Aliases, ep.Aliases...)
			es.MacAddress = ep.MacAddress
			if ep.Ipv4Address != "" || ep.Ipv6Address != "" {
				es.IPAMConfig = &network.EndpointIPAMConfig{
					IPv4Address: ep.Ipv4Address,
					IPv6Address: ep.Ipv6Address,
				}
			}
		}
		netCfg.EndpointsConfig[name] = es
	}

	return &translation{
		Name:       containerName,
		Config:     cfg,
		HostConfig: host,
		NetConfig:  netCfg,
	}, nil
}

// translateVolumes converts compose volume entries into docker mounts.
// Bind mounts pass through directly; named volumes are renamed to the
// project-prefixed form. Other types are rejected.
func translateVolumes(vols []types.ServiceVolumeConfig, volumeNames map[string]string) ([]mount.Mount, error) {
	if len(vols) == 0 {
		return nil, nil
	}
	out := make([]mount.Mount, 0, len(vols))
	for _, v := range vols {
		m := mount.Mount{
			Target:   v.Target,
			ReadOnly: v.ReadOnly,
		}
		switch v.Type {
		case types.VolumeTypeBind, "":
			m.Type = mount.TypeBind
			m.Source = v.Source
			if v.Bind != nil {
				m.BindOptions = &mount.BindOptions{
					Propagation: mount.Propagation(v.Bind.Propagation),
				}
			}
		case types.VolumeTypeVolume:
			m.Type = mount.TypeVolume
			if name := volumeNames[v.Source]; name != "" {
				m.Source = name
			} else {
				m.Source = v.Source
			}
			if v.Volume != nil {
				m.VolumeOptions = &mount.VolumeOptions{
					NoCopy:  v.Volume.NoCopy,
					Subpath: v.Volume.Subpath,
				}
			}
		default:
			return nil, fmt.Errorf("volume type %q is not supported in this iteration (target %s)", v.Type, v.Target)
		}
		out = append(out, m)
	}
	return out, nil
}

func tmpfsList(t types.StringList) map[string]string {
	if len(t) == 0 {
		return nil
	}
	out := make(map[string]string, len(t))
	for _, s := range t {
		out[s] = ""
	}
	return out
}

// sortedEnv returns the service environment as KEY=VAL strings in
// stable order. Compose `MappingWithEquals` distinguishes nil values
// (=> drop the variable from the env that would otherwise be set by
// envFile / shell) from "" (=> set to empty); we treat nil as "drop"
// by skipping it, matching docker compose's behavior.
func sortedEnv(env types.MappingWithEquals) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		v := env[k]
		if v == nil {
			continue
		}
		out = append(out, k+"="+*v)
	}
	return out
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}
