// relationships:
//
//	references: [metadata, up, spec]
package dockercompose

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
)

// serviceConfigHash returns a stable hex digest of the parts of a
// compose ServiceConfig that affect what the engine API would
// produce. Mirrors the `com.docker.compose.config-hash` label `docker
// compose` writes onto each container. The hash is computed before
// the package's own labels (project, service, container-number,
// config-hash itself) are stamped, so it captures the user-supplied
// shape of the service rather than our bookkeeping.
//
// Stability matters more than correctness here: the same input must
// always produce the same digest across runs. We achieve that by
// JSON-marshaling a normalized snapshot — maps walk in sorted order
// (encoding/json sorts string-keyed maps), slices retain their
// declared order so reordering a compose entry is treated as a
// config change.
func serviceConfigHash(svc types.ServiceConfig) string {
	body, err := json.Marshal(makeServiceHashSnapshot(svc))
	if err != nil {
		// Defensive: marshal can't fail on this struct, but if a
		// future field breaks that, fall back to hashing the image
		// reference rather than panicking.
		body = []byte(svc.Image)
	}
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// serviceHashSnapshot is the normalized projection we hash. Field
// ordering here is deliberate and frozen — adding a new field
// invalidates every existing container's hash, so only add when the
// new field genuinely affects container shape.
type serviceHashSnapshot struct {
	Image       string              `json:"image,omitempty"`
	Entrypoint  []string            `json:"entrypoint,omitempty"`
	Command     []string            `json:"command,omitempty"`
	WorkingDir  string              `json:"working_dir,omitempty"`
	User        string              `json:"user,omitempty"`
	Hostname    string              `json:"hostname,omitempty"`
	DomainName  string              `json:"domain,omitempty"`
	Tty         bool                `json:"tty,omitempty"`
	StdinOpen   bool                `json:"stdin_open,omitempty"`
	StopSignal  string              `json:"stop_signal,omitempty"`
	Init        *bool               `json:"init,omitempty"`
	Privileged  bool                `json:"privileged,omitempty"`
	ReadOnly    bool                `json:"read_only,omitempty"`
	NetworkMode string              `json:"network_mode,omitempty"`
	Ipc         string              `json:"ipc,omitempty"`
	Pid         string              `json:"pid,omitempty"`
	Restart     string              `json:"restart,omitempty"`
	ShmSize     uint64              `json:"shm_size,omitempty"`
	CapAdd      []string            `json:"cap_add,omitempty"`
	CapDrop     []string            `json:"cap_drop,omitempty"`
	SecurityOpt []string            `json:"security_opt,omitempty"`
	GroupAdd    []string            `json:"group_add,omitempty"`
	DNS         []string            `json:"dns,omitempty"`
	DNSSearch   []string            `json:"dns_search,omitempty"`
	DNSOpts     []string            `json:"dns_opts,omitempty"`
	ExtraHosts  []string            `json:"extra_hosts,omitempty"`
	Sysctls     map[string]string   `json:"sysctls,omitempty"`
	Tmpfs       []string            `json:"tmpfs,omitempty"`
	Environment map[string]string   `json:"environment,omitempty"`
	Labels      map[string]string   `json:"labels,omitempty"`
	Volumes     []volumeSnapshot    `json:"volumes,omitempty"`
	Ports       []portSnapshot      `json:"ports,omitempty"`
	Networks    []networkSnapshot   `json:"networks,omitempty"`
	DependsOn   []dependsOnSnapshot `json:"depends_on,omitempty"`
}

type volumeSnapshot struct {
	Type        string `json:"type,omitempty"`
	Source      string `json:"source,omitempty"`
	Target      string `json:"target,omitempty"`
	ReadOnly    bool   `json:"read_only,omitempty"`
	Consistency string `json:"consistency,omitempty"`
	Propagation string `json:"propagation,omitempty"`
	Subpath     string `json:"subpath,omitempty"`
}

type portSnapshot struct {
	Target    uint32 `json:"target,omitempty"`
	Published string `json:"published,omitempty"`
	HostIP    string `json:"host_ip,omitempty"`
	Protocol  string `json:"protocol,omitempty"`
	Mode      string `json:"mode,omitempty"`
}

type networkSnapshot struct {
	Key         string   `json:"key,omitempty"`
	Aliases     []string `json:"aliases,omitempty"`
	Ipv4Address string   `json:"ipv4_address,omitempty"`
	Ipv6Address string   `json:"ipv6_address,omitempty"`
	MacAddress  string   `json:"mac_address,omitempty"`
}

type dependsOnSnapshot struct {
	Service   string `json:"service,omitempty"`
	Condition string `json:"condition,omitempty"`
	Required  bool   `json:"required"`
}

func makeServiceHashSnapshot(svc types.ServiceConfig) serviceHashSnapshot {
	out := serviceHashSnapshot{
		Image:       svc.Image,
		Entrypoint:  []string(svc.Entrypoint),
		Command:     []string(svc.Command),
		WorkingDir:  svc.WorkingDir,
		User:        svc.User,
		Hostname:    svc.Hostname,
		DomainName:  svc.DomainName,
		Tty:         svc.Tty,
		StdinOpen:   svc.StdinOpen,
		StopSignal:  svc.StopSignal,
		Init:        svc.Init,
		Privileged:  svc.Privileged,
		ReadOnly:    svc.ReadOnly,
		NetworkMode: svc.NetworkMode,
		Ipc:         svc.Ipc,
		Pid:         svc.Pid,
		Restart:     svc.Restart,
		ShmSize:     uint64(svc.ShmSize),
		CapAdd:      copyStringSlice(svc.CapAdd),
		CapDrop:     copyStringSlice(svc.CapDrop),
		SecurityOpt: copyStringSlice(svc.SecurityOpt),
		GroupAdd:    copyStringSlice(svc.GroupAdd),
		DNS:         copyStringSlice(svc.DNS),
		DNSSearch:   copyStringSlice(svc.DNSSearch),
		DNSOpts:     copyStringSlice(svc.DNSOpts),
		ExtraHosts:  svc.ExtraHosts.AsList(":"),
		Sysctls:     copyStringMap(map[string]string(svc.Sysctls)),
		Tmpfs:       copyStringSlice(svc.Tmpfs),
		Environment: envToStringMap(svc.Environment),
		Labels:      hashableLabels(svc.Labels),
	}
	if len(svc.Volumes) > 0 {
		out.Volumes = make([]volumeSnapshot, 0, len(svc.Volumes))
		for _, v := range svc.Volumes {
			entry := volumeSnapshot{
				Type:        v.Type,
				Source:      v.Source,
				Target:      v.Target,
				ReadOnly:    v.ReadOnly,
				Consistency: v.Consistency,
			}
			if v.Bind != nil {
				entry.Propagation = v.Bind.Propagation
			}
			if v.Volume != nil {
				entry.Subpath = v.Volume.Subpath
			}
			out.Volumes = append(out.Volumes, entry)
		}
	}
	if len(svc.Ports) > 0 {
		out.Ports = make([]portSnapshot, 0, len(svc.Ports))
		for _, p := range svc.Ports {
			out.Ports = append(out.Ports, portSnapshot{
				Target:    p.Target,
				Published: p.Published,
				HostIP:    p.HostIP,
				Protocol:  p.Protocol,
				Mode:      p.Mode,
			})
		}
	}
	if len(svc.Networks) > 0 {
		netKeys := make([]string, 0, len(svc.Networks))
		for k := range svc.Networks {
			netKeys = append(netKeys, k)
		}
		sort.Strings(netKeys)
		for _, k := range netKeys {
			ep := svc.Networks[k]
			entry := networkSnapshot{Key: k}
			if ep != nil {
				entry.Aliases = copyStringSlice(ep.Aliases)
				entry.Ipv4Address = ep.Ipv4Address
				entry.Ipv6Address = ep.Ipv6Address
				entry.MacAddress = ep.MacAddress
			}
			out.Networks = append(out.Networks, entry)
		}
	}
	if len(svc.DependsOn) > 0 {
		depKeys := make([]string, 0, len(svc.DependsOn))
		for k := range svc.DependsOn {
			depKeys = append(depKeys, k)
		}
		sort.Strings(depKeys)
		for _, k := range depKeys {
			d := svc.DependsOn[k]
			out.DependsOn = append(out.DependsOn, dependsOnSnapshot{
				Service:   k,
				Condition: d.Condition,
				Required:  d.Required,
			})
		}
	}
	return out
}

// envToStringMap flattens a compose env mapping (which distinguishes
// nil "drop" from "" "set to empty") into a plain map for hashing.
// nil values are dropped, matching how compose itself would resolve
// them when issuing the engine call.
func envToStringMap(env types.MappingWithEquals) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for k, v := range env {
		if v == nil {
			continue
		}
		out[k] = *v
	}
	return out
}

// hashableLabels retains captured metadata so hook changes trigger recreation.
// It returns a copy of labels with other
// `com.docker.compose.*` and `devcontainer.*` keys stripped — those
// identify resources and are stamped by the host, so
// hashing them would mean re-hashing our own bookkeeping (and the
// hash itself) and would change every time the project name moves.
func hashableLabels(in types.Labels) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		if strings.HasPrefix(k, "com.docker.compose.") {
			continue
		}
		if strings.HasPrefix(k, "devcontainer.") && k != metadata.Label {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyStringSlice(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	return append([]string(nil), in...)
}
