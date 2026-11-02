package docker

import (
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"

	dockeropts "github.com/docker/cli/opts"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
	"github.com/docker/go-units"
)

// applyRunArgs parses docker-CLI-style flags from a devcontainer.json
// runArgs array and folds them into cfg / host. Strings have already
// been variable-substituted by the caller. The returned name is the
// separate Engine ContainerCreate name argument, not the container hostname.
//
// Unknown or malformed flags return an error rather than being silently
// ignored: a silent drop of e.g. --network or --gpus could leave the
// container running with surprising defaults.
func applyRunArgs(args []string, cfg *container.Config, host *container.HostConfig) (string, error) {
	p := &runArgsParser{args: args}
	for p.more() {
		if err := p.step(cfg, host); err != nil {
			return "", err
		}
	}
	// Docker loads every file before explicit flags, regardless of argv order.
	env, err := dockeropts.ReadKVEnvStrings(p.envFiles, cfg.Env)
	if err != nil {
		return "", fmt.Errorf("runArgs: --env-file: %w", err)
	}
	cfg.Env = env
	labels, err := dockeropts.ReadKVStrings(p.labelFiles, nil)
	if err != nil {
		return "", fmt.Errorf("runArgs: --label-file: %w", err)
	}
	if len(labels) > 0 {
		merged := dockeropts.ConvertKVStringsToMap(labels)
		for k, v := range cfg.Labels {
			merged[k] = v
		}
		cfg.Labels = merged
	}
	return p.name, nil
}

type runArgsParser struct {
	args       []string
	i          int
	name       string
	envFiles   []string
	labelFiles []string
}

func (p *runArgsParser) more() bool { return p.i < len(p.args) }

// pop returns the next argv element and advances.
func (p *runArgsParser) pop() string {
	v := p.args[p.i]
	p.i++
	return v
}

// step consumes one logical flag (and its value, if any).
func (p *runArgsParser) step(cfg *container.Config, host *container.HostConfig) error {
	a := p.pop()
	if a == "" {
		return nil
	}
	if a == "--" {
		return fmt.Errorf("runArgs: positional arguments after %q are not allowed (devcontainer image is set elsewhere)", a)
	}
	if !strings.HasPrefix(a, "-") {
		return fmt.Errorf("runArgs: positional argument %q is not allowed (only flags belong in runArgs)", a)
	}

	// Docker/pflag short options allow clusters and attached values. A boolean
	// consumes only its letter; a value option consumes the remaining suffix.
	if len(a) > 2 && a[0] == '-' && a[1] != '-' && a[2] != '=' {
		if strings.ContainsRune("ditP", rune(a[1])) {
			p.args = append(append([]string{}, p.args[:p.i]...), append([]string{"-" + a[2:]}, p.args[p.i:]...)...)
			a = a[:2]
		} else if strings.ContainsRune("huwelpvmc", rune(a[1])) {
			a = a[:2] + "=" + a[2:]
		}
	}

	flag, val, hasVal := splitFlag(a)
	take := func() (string, error) {
		if hasVal {
			return val, nil
		}
		if p.i >= len(p.args) {
			return "", fmt.Errorf("runArgs: flag %s requires a value", flag)
		}
		return p.pop(), nil
	}
	boolean := func() (bool, error) {
		if !hasVal {
			return true, nil
		}
		v, err := strconv.ParseBool(val)
		if err != nil {
			return false, fmt.Errorf("runArgs: flag %s: %w", flag, err)
		}
		return v, nil
	}

	switch flag {
	case "--init", "--privileged", "--read-only", "-P", "--publish-all",
		"-i", "--interactive", "-t", "--tty", "--rm", "-d", "--detach", "--sig-proxy":
		v, err := boolean()
		if err != nil {
			return err
		}
		switch flag {
		case "--init":
			host.Init = &v
		case "--privileged":
			host.Privileged = v
		case "--read-only":
			host.ReadonlyRootfs = v
		case "-P", "--publish-all":
			host.PublishAllPorts = v
		case "-i", "--interactive":
			cfg.OpenStdin = v
		case "-t", "--tty":
			cfg.Tty = v
		case "--rm":
			host.AutoRemove = v
			// Detach and signal proxy control the CLI process, not creation.
		}

	case "--name":
		v, err := take()
		if err != nil {
			return err
		}
		p.name = v

	// ---------------------------------------------------------------
	// Single-value flags on Config.
	// ---------------------------------------------------------------
	case "-h", "--hostname":
		v, err := take()
		if err != nil {
			return err
		}
		cfg.Hostname = v
	case "--domainname":
		v, err := take()
		if err != nil {
			return err
		}
		cfg.Domainname = v
	case "-u", "--user":
		v, err := take()
		if err != nil {
			return err
		}
		cfg.User = v
	case "-w", "--workdir":
		v, err := take()
		if err != nil {
			return err
		}
		cfg.WorkingDir = v
	case "--mac-address":
		v, err := take()
		if err != nil {
			return err
		}
		cfg.MacAddress = v
	case "--stop-signal":
		v, err := take()
		if err != nil {
			return err
		}
		cfg.StopSignal = v
	case "--stop-timeout":
		v, err := take()
		if err != nil {
			return err
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("runArgs: --stop-timeout: %w", err)
		}
		cfg.StopTimeout = &n

	// ---------------------------------------------------------------
	// Single-value flags on HostConfig namespace / runtime fields.
	// ---------------------------------------------------------------
	case "--network", "--net":
		v, err := take()
		if err != nil {
			return err
		}
		host.NetworkMode = container.NetworkMode(v)
	case "--ipc":
		v, err := take()
		if err != nil {
			return err
		}
		host.IpcMode = container.IpcMode(v)
	case "--pid":
		v, err := take()
		if err != nil {
			return err
		}
		host.PidMode = container.PidMode(v)
	case "--uts":
		v, err := take()
		if err != nil {
			return err
		}
		host.UTSMode = container.UTSMode(v)
	case "--userns":
		v, err := take()
		if err != nil {
			return err
		}
		host.UsernsMode = container.UsernsMode(v)
	case "--cgroupns":
		v, err := take()
		if err != nil {
			return err
		}
		host.CgroupnsMode = container.CgroupnsMode(v)
	case "--cgroup-parent":
		v, err := take()
		if err != nil {
			return err
		}
		host.CgroupParent = v
	case "--runtime":
		v, err := take()
		if err != nil {
			return err
		}
		host.Runtime = v
	case "--restart":
		v, err := take()
		if err != nil {
			return err
		}
		name, retries, err := parseRestart(v)
		if err != nil {
			return err
		}
		host.RestartPolicy = container.RestartPolicy{Name: name, MaximumRetryCount: retries}
	case "--volume-driver":
		v, err := take()
		if err != nil {
			return err
		}
		host.VolumeDriver = v
	case "--isolation":
		v, err := take()
		if err != nil {
			return err
		}
		host.Isolation = container.Isolation(v)

	// ---------------------------------------------------------------
	// Resource limits.
	// ---------------------------------------------------------------
	case "-m", "--memory":
		v, err := take()
		if err != nil {
			return err
		}
		bytes, err := units.RAMInBytes(v)
		if err != nil {
			return fmt.Errorf("runArgs: --memory: %w", err)
		}
		host.Memory = bytes
	case "--memory-swap":
		v, err := take()
		if err != nil {
			return err
		}
		if v == "-1" {
			host.MemorySwap = -1
		} else {
			bytes, err := units.RAMInBytes(v)
			if err != nil {
				return fmt.Errorf("runArgs: --memory-swap: %w", err)
			}
			host.MemorySwap = bytes
		}
	case "--memory-reservation":
		v, err := take()
		if err != nil {
			return err
		}
		bytes, err := units.RAMInBytes(v)
		if err != nil {
			return fmt.Errorf("runArgs: --memory-reservation: %w", err)
		}
		host.MemoryReservation = bytes
	case "--shm-size":
		v, err := take()
		if err != nil {
			return err
		}
		bytes, err := units.RAMInBytes(v)
		if err != nil {
			return fmt.Errorf("runArgs: --shm-size: %w", err)
		}
		host.ShmSize = bytes
	case "--cpus":
		v, err := take()
		if err != nil {
			return err
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("runArgs: --cpus: %w", err)
		}
		host.NanoCPUs = int64(f * 1e9)
	case "-c", "--cpu-shares":
		v, err := take()
		if err != nil {
			return err
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("runArgs: --cpu-shares: %w", err)
		}
		host.CPUShares = n
	case "--cpuset-cpus":
		v, err := take()
		if err != nil {
			return err
		}
		host.CpusetCpus = v
	case "--cpuset-mems":
		v, err := take()
		if err != nil {
			return err
		}
		host.CpusetMems = v
	case "--pids-limit":
		v, err := take()
		if err != nil {
			return err
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("runArgs: --pids-limit: %w", err)
		}
		host.PidsLimit = &n
	case "--oom-score-adj":
		v, err := take()
		if err != nil {
			return err
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("runArgs: --oom-score-adj: %w", err)
		}
		host.OomScoreAdj = n
	case "--oom-kill-disable":
		// docker allows --oom-kill-disable as a bool with optional value.
		if !hasVal {
			b := true
			host.OomKillDisable = &b
		} else {
			b, err := parseBool(val)
			if err != nil {
				return fmt.Errorf("runArgs: --oom-kill-disable: %w", err)
			}
			host.OomKillDisable = &b
		}

	// ---------------------------------------------------------------
	// Repeatable list flags.
	// ---------------------------------------------------------------
	case "--cap-add":
		v, err := take()
		if err != nil {
			return err
		}
		host.CapAdd = append(host.CapAdd, v)
	case "--cap-drop":
		v, err := take()
		if err != nil {
			return err
		}
		host.CapDrop = append(host.CapDrop, v)
	case "--security-opt":
		v, err := take()
		if err != nil {
			return err
		}
		host.SecurityOpt = append(host.SecurityOpt, v)
	case "--device":
		v, err := take()
		if err != nil {
			return err
		}
		dm, err := parseDevice(v)
		if err != nil {
			return err
		}
		host.Devices = append(host.Devices, dm)
	case "--device-cgroup-rule":
		v, err := take()
		if err != nil {
			return err
		}
		host.DeviceCgroupRules = append(host.DeviceCgroupRules, v)
	case "--env-file", "--label-file":
		v, err := take()
		if err != nil {
			return err
		}
		if flag == "--env-file" {
			p.envFiles = append(p.envFiles, v)
		} else {
			p.labelFiles = append(p.labelFiles, v)
		}
	case "-l", "--label":
		v, err := take()
		if err != nil {
			return err
		}
		k, lv := splitKV(v)
		if k == "" {
			return fmt.Errorf("runArgs: --label: empty key in %q", v)
		}
		if cfg.Labels == nil {
			cfg.Labels = map[string]string{}
		}
		cfg.Labels[k] = lv
	case "-e", "--env":
		v, err := take()
		if err != nil {
			return err
		}
		v, err = dockeropts.ValidateEnv(v)
		if err != nil {
			return fmt.Errorf("runArgs: --env: %w", err)
		}
		// Keep argument order, including bare unset keys, for Engine's merge
		// with image defaults and repeated environment overrides.
		cfg.Env = append(cfg.Env, v)
	case "--add-host":
		v, err := take()
		if err != nil {
			return err
		}
		host.ExtraHosts = append(host.ExtraHosts, v)
	case "--dns":
		v, err := take()
		if err != nil {
			return err
		}
		host.DNS = append(host.DNS, v)
	case "--dns-search":
		v, err := take()
		if err != nil {
			return err
		}
		host.DNSSearch = append(host.DNSSearch, v)
	case "--dns-option", "--dns-opt":
		v, err := take()
		if err != nil {
			return err
		}
		host.DNSOptions = append(host.DNSOptions, v)
	case "--group-add":
		v, err := take()
		if err != nil {
			return err
		}
		host.GroupAdd = append(host.GroupAdd, v)
	case "--link":
		v, err := take()
		if err != nil {
			return err
		}
		host.Links = append(host.Links, v)
	case "--volumes-from":
		v, err := take()
		if err != nil {
			return err
		}
		host.VolumesFrom = append(host.VolumesFrom, v)
	case "--expose":
		v, err := take()
		if err != nil {
			return err
		}
		if cfg.ExposedPorts == nil {
			cfg.ExposedPorts = nat.PortSet{}
		}
		ports, _, err := nat.ParsePortSpecs([]string{v})
		if err != nil {
			return fmt.Errorf("runArgs: --expose %q: %w", v, err)
		}
		for p := range ports {
			cfg.ExposedPorts[p] = struct{}{}
		}
	case "-p", "--publish":
		v, err := take()
		if err != nil {
			return err
		}
		exposed, bindings, err := nat.ParsePortSpecs([]string{v})
		if err != nil {
			return fmt.Errorf("runArgs: --publish %q: %w", v, err)
		}
		if cfg.ExposedPorts == nil {
			cfg.ExposedPorts = nat.PortSet{}
		}
		for p := range exposed {
			cfg.ExposedPorts[p] = struct{}{}
		}
		if host.PortBindings == nil {
			host.PortBindings = nat.PortMap{}
		}
		for p, b := range bindings {
			host.PortBindings[p] = append(host.PortBindings[p], b...)
		}
	case "--sysctl":
		v, err := take()
		if err != nil {
			return err
		}
		k, sv := splitKV(v)
		if k == "" {
			return fmt.Errorf("runArgs: --sysctl: missing key in %q", v)
		}
		if host.Sysctls == nil {
			host.Sysctls = map[string]string{}
		}
		host.Sysctls[k] = sv
	case "--ulimit":
		v, err := take()
		if err != nil {
			return err
		}
		u, err := units.ParseUlimit(v)
		if err != nil {
			return fmt.Errorf("runArgs: --ulimit: %w", err)
		}
		host.Ulimits = append(host.Ulimits, u)
	case "--tmpfs":
		v, err := take()
		if err != nil {
			return err
		}
		path, opts := splitTmpfs(v)
		if host.Tmpfs == nil {
			host.Tmpfs = map[string]string{}
		}
		host.Tmpfs[path] = opts
	case "-v", "--volume":
		v, err := take()
		if err != nil {
			return err
		}
		host.Binds = append(host.Binds, v)
	case "--mount":
		v, err := take()
		if err != nil {
			return err
		}
		m, err := parseMountString(v)
		if err != nil {
			return fmt.Errorf("runArgs: --mount %q: %w", v, err)
		}
		host.Mounts = append(host.Mounts, *m)
	case "--gpus":
		v, err := take()
		if err != nil {
			return err
		}
		req, err := parseGPUs(v)
		if err != nil {
			return err
		}
		host.DeviceRequests = append(host.DeviceRequests, req)
	case "--annotation":
		v, err := take()
		if err != nil {
			return err
		}
		k, av := splitKV(v)
		if k == "" {
			return fmt.Errorf("runArgs: --annotation: empty key in %q", v)
		}
		if host.Annotations == nil {
			host.Annotations = map[string]string{}
		}
		host.Annotations[k] = av

	case "--entrypoint":
		// Upstream places its keepalive entrypoint after runArgs, overriding it.
		if _, err := take(); err != nil {
			return err
		}

	default:
		return fmt.Errorf("runArgs: unsupported flag %q", flag)
	}
	return nil
}

// splitFlag splits "-x" / "--foo" / "--foo=bar" into (flag, value, hasVal).
// The returned flag includes its leading dashes for use in error messages.
func splitFlag(a string) (flag, val string, hasVal bool) {
	if eq := strings.IndexByte(a, '='); eq >= 0 {
		return a[:eq], a[eq+1:], true
	}
	return a, "", false
}

// splitKV splits "k=v" into ("k", "v"). For "k" alone, returns ("k", "").
func splitKV(s string) (k, v string) {
	if eq := strings.IndexByte(s, '='); eq >= 0 {
		return s[:eq], s[eq+1:]
	}
	return s, ""
}

// parseRestart parses "always", "on-failure", "on-failure:5",
// "unless-stopped", "no" into (mode, retries).
func parseRestart(s string) (container.RestartPolicyMode, int, error) {
	name, rest, _ := strings.Cut(s, ":")
	mode := container.RestartPolicyMode(name)
	switch mode {
	case container.RestartPolicyAlways, container.RestartPolicyDisabled, container.RestartPolicyUnlessStopped:
		if rest != "" {
			return "", 0, fmt.Errorf("runArgs: --restart %q: maximum-retry-count only valid with on-failure", s)
		}
		return mode, 0, nil
	case container.RestartPolicyOnFailure:
		if rest == "" {
			return mode, 0, nil
		}
		n, err := strconv.Atoi(rest)
		if err != nil {
			return "", 0, fmt.Errorf("runArgs: --restart %q: %w", s, err)
		}
		return mode, n, nil
	default:
		return "", 0, fmt.Errorf("runArgs: --restart %q: unknown policy", s)
	}
}

// parseDevice parses "host[:container[:perms]]" into a DeviceMapping.
func parseDevice(s string) (container.DeviceMapping, error) {
	parts := strings.SplitN(s, ":", 3)
	dm := container.DeviceMapping{PathOnHost: parts[0], CgroupPermissions: "rwm"}
	dm.PathInContainer = parts[0]
	if len(parts) >= 2 && parts[1] != "" {
		dm.PathInContainer = parts[1]
	}
	if len(parts) == 3 && parts[2] != "" {
		dm.CgroupPermissions = parts[2]
	}
	if dm.PathOnHost == "" {
		return dm, fmt.Errorf("runArgs: --device %q: missing host path", s)
	}
	return dm, nil
}

// splitTmpfs parses "/path[:options]". The options string is opaque to
// us — docker parses it on its side.
func splitTmpfs(s string) (path, opts string) {
	if i := strings.IndexByte(s, ':'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// parseGPUs translates the docker `--gpus` shorthand into a
// DeviceRequest. Mirrors the docker CLI parser: bare "all", an integer
// count, or a CSV of key=value fields where multi-valued fields like
// device=... and capabilities=gpu,utility must be CSV-quoted to keep
// their inner commas attached. Examples:
//
//	all
//	2
//	"device=GPU-a,GPU-b",capabilities=gpu
//	count=2,driver=nvidia,"capabilities=gpu,utility"
func parseGPUs(s string) (container.DeviceRequest, error) {
	s = strings.TrimSpace(s)
	req := container.DeviceRequest{Capabilities: [][]string{{"gpu"}}}
	if s == "all" || s == `"all"` || s == `'all'` {
		req.Count = -1
		return req, nil
	}
	if n, err := strconv.Atoi(strings.Trim(s, `"'`)); err == nil {
		req.Count = n
		return req, nil
	}

	r := csv.NewReader(strings.NewReader(s))
	fields, err := r.Read()
	if err != nil {
		return req, fmt.Errorf("runArgs: --gpus %q: %w", s, err)
	}

	caps := []string{}
	for _, field := range fields {
		k, v, ok := strings.Cut(field, "=")
		if !ok {
			if k == "all" {
				req.Count = -1
				continue
			}
			return req, fmt.Errorf("runArgs: --gpus: expected key=value, got %q", field)
		}
		switch k {
		case "count":
			if v == "all" {
				req.Count = -1
			} else {
				n, err := strconv.Atoi(v)
				if err != nil {
					return req, fmt.Errorf("runArgs: --gpus count=%q: %w", v, err)
				}
				req.Count = n
			}
		case "device":
			req.DeviceIDs = append(req.DeviceIDs, splitCSV(v)...)
		case "driver":
			req.Driver = v
		case "capabilities":
			caps = append(caps, splitCSV(v)...)
		case "options":
			if req.Options == nil {
				req.Options = map[string]string{}
			}
			for _, opt := range splitCSV(v) {
				ok, ov := splitKV(opt)
				req.Options[ok] = ov
			}
		default:
			return req, fmt.Errorf("runArgs: --gpus: unknown key %q", k)
		}
	}
	if len(caps) > 0 {
		req.Capabilities = [][]string{caps}
	}
	if req.Count == 0 && len(req.DeviceIDs) == 0 {
		// Match docker CLI default when the user passes only `capabilities=...`.
		req.Count = -1
	}
	return req, nil
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := parts[:0]
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func parseBool(s string) (bool, error) {
	switch strings.ToLower(s) {
	case "true", "1", "yes":
		return true, nil
	case "false", "0", "no":
		return false, nil
	}
	return false, fmt.Errorf("invalid boolean %q", s)
}
