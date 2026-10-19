// relationships:
//
//	references: [metadata, up, spec]
package dockercompose

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

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
	if svc.HealthCheck != nil {
		cfg.Healthcheck = translateHealthcheck(svc.HealthCheck)
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
	host.DeviceRequests = composeDeviceRequests(svc)
	if svc.Restart != "" {
		host.RestartPolicy = container.RestartPolicy{Name: container.RestartPolicyMode(svc.Restart)}
	}

	binds, mounts, err := translateVolumes(svc.Volumes, volumeNames)
	if err != nil {
		return nil, err
	}
	host.Binds = binds
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

func translateHealthcheck(in *types.HealthCheckConfig) *container.HealthConfig {
	if in == nil {
		return nil
	}
	out := &container.HealthConfig{
		Test:          append([]string(nil), in.Test...),
		Timeout:       composeDuration(in.Timeout),
		Interval:      composeDuration(in.Interval),
		StartPeriod:   composeDuration(in.StartPeriod),
		StartInterval: composeDuration(in.StartInterval),
	}
	if in.Disable {
		out.Test = []string{"NONE"}
	}
	if in.Retries != nil {
		out.Retries = int(*in.Retries)
	}
	return out
}

func composeDuration(d *types.Duration) time.Duration {
	if d == nil {
		return 0
	}
	return time.Duration(*d)
}

// translateVolumes converts compose volume entries as docker compose does
// (pkg/compose/create.go buildContainerVolumes): a bind mount or a declared
// named volume without Mount-API-only options uses the Bind API, which creates
// a missing host path; other entries use the Mount API. Named volumes are
// renamed to the project-prefixed form. Other types are rejected.
func translateVolumes(vols []types.ServiceVolumeConfig, volumeNames map[string]string) ([]string, []mount.Mount, error) {
	var binds []string
	var out []mount.Mount
	for _, v := range vols {
		m := mount.Mount{
			Target:      v.Target,
			ReadOnly:    v.ReadOnly,
			Consistency: mount.Consistency(v.Consistency),
		}
		switch v.Type {
		case types.VolumeTypeBind, "":
			if !bindRequiresMountAPI(v.Bind) {
				binds = append(binds, toBindString(v.Source, v))
				continue
			}
			m.Type = mount.TypeBind
			m.Source = v.Source
			m.BindOptions = &mount.BindOptions{
				Propagation: mount.Propagation(v.Bind.Propagation),
			}
		case types.VolumeTypeVolume:
			name := volumeNames[v.Source]
			if name != "" && !volumeRequiresMountAPI(v.Volume) {
				binds = append(binds, toBindString(name, v))
				continue
			}
			m.Type = mount.TypeVolume
			m.Source = v.Source
			if name != "" {
				m.Source = name
			}
			if v.Volume != nil {
				m.VolumeOptions = &mount.VolumeOptions{
					NoCopy:  v.Volume.NoCopy,
					Subpath: v.Volume.Subpath,
				}
			}
		default:
			return nil, nil, fmt.Errorf("volume type %q is not supported in this iteration (target %s)", v.Type, v.Target)
		}
		out = append(out, m)
	}
	return binds, out, nil
}

// toBindString matches docker compose: access mode, then SELinux label,
// propagation and nocopy options. Consistency has no Bind-API form.
func toBindString(name string, v types.ServiceVolumeConfig) string {
	options := []string{"rw"}
	if v.ReadOnly {
		options[0] = "ro"
	}
	if v.Bind != nil && v.Bind.SELinux != "" {
		options = append(options, v.Bind.SELinux)
	}
	if v.Bind != nil && v.Bind.Propagation != "" {
		options = append(options, v.Bind.Propagation)
	}
	if v.Volume != nil && v.Volume.NoCopy {
		options = append(options, "nocopy")
	}
	return name + ":" + v.Target + ":" + strings.Join(options, ",")
}

// bindRequiresMountAPI reports bind options the Bind API cannot express.
func bindRequiresMountAPI(bind *types.ServiceVolumeBind) bool {
	return bind != nil && (!bool(bind.CreateHostPath) || bind.Propagation != "" || bind.Recursive != "")
}

// volumeRequiresMountAPI reports volume options the Bind API cannot express.
func volumeRequiresMountAPI(vol *types.ServiceVolumeVolume) bool {
	return vol != nil && (len(vol.Labels) > 0 || vol.Subpath != "" || vol.NoCopy)
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
