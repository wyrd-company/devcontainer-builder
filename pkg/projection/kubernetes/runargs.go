package kubernetes

import (
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/docker/go-units"
)

// applyRunArgs parses docker-CLI-style flags from a devcontainer.json
// runArgs array and folds them into the projected pod / container.
//
// The vocabulary is the same one pkg/host/docker.applyRunArgs accepts.
// Flags that map cleanly onto Kubernetes (--cap-add, --user,
// --memory, --sysctl, --device-cgroup-rule, --restart, namespace
// flags with "host" / "shareable" arguments, env, volumes, hosts,
// dns) are translated. Flags that don't have a Kubernetes equivalent
// (--gpus device IDs, --network mode strings, --userns, --cgroup-parent,
// --runtime-specific options) return an error rather than being
// silently dropped — the same conservative posture pkg/host/docker
// takes against unknown flags.
//
// Flags that pkg/host/docker tolerates as docker-runtime no-ops (-i,
// -t, -d, --rm, --sig-proxy, --name) are also no-ops here.
func applyRunArgs(args []string, pod *corev1.Pod, c *corev1.Container) error {
	p := &runArgsParser{args: args}
	for p.more() {
		if err := p.step(pod, c); err != nil {
			return err
		}
	}
	return nil
}

type runArgsParser struct {
	args []string
	i    int
}

func (p *runArgsParser) more() bool { return p.i < len(p.args) }

func (p *runArgsParser) pop() string {
	v := p.args[p.i]
	p.i++
	return v
}

func (p *runArgsParser) step(pod *corev1.Pod, c *corev1.Container) error {
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
	if isCombinedBoolShort(a) {
		return nil
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
	requireNoValue := func() error {
		if hasVal {
			return fmt.Errorf("runArgs: flag %s does not take a value", flag)
		}
		return nil
	}

	switch flag {
	// --------------------------------------------------------------
	// Booleans on SecurityContext.
	// --------------------------------------------------------------
	case "--privileged":
		if err := requireNoValue(); err != nil {
			return err
		}
		ensureSecCtx(c).Privileged = boolPtr(true)
	case "--read-only":
		if err := requireNoValue(); err != nil {
			return err
		}
		ensureSecCtx(c).ReadOnlyRootFilesystem = boolPtr(true)

	// --init has no PodSpec equivalent. Kubernetes containers start
	// as PID 1 of the container; the spec doesn't expose a tini-like
	// reaper. The image itself is the place to put one.
	case "--init":
		return fmt.Errorf("runArgs: --init has no Kubernetes equivalent (PID 1 is the container's command; build a tini-style entrypoint into the image)")

	// Tolerated docker-runtime no-ops.
	case "-d", "--detach", "-i", "--interactive", "-t", "--tty", "--rm", "--sig-proxy":
		// no-op

	// --name → metadata.name (only if not already set by the request).
	case "--name":
		v, err := take()
		if err != nil {
			return err
		}
		if pod.Name == "" {
			pod.Name = v
		}

	case "-P", "--publish-all":
		return fmt.Errorf("runArgs: --publish-all has no Kubernetes equivalent (declare ContainerPorts plus a Service for external reach)")

	// --------------------------------------------------------------
	// Container-level fields.
	// --------------------------------------------------------------
	case "-h", "--hostname":
		v, err := take()
		if err != nil {
			return err
		}
		pod.Spec.Hostname = v
	case "--domainname":
		v, err := take()
		if err != nil {
			return err
		}
		pod.Spec.Subdomain = v
	case "-u", "--user":
		v, err := take()
		if err != nil {
			return err
		}
		if err := applyContainerUser(c, v); err != nil {
			return fmt.Errorf("runArgs: --user: %w", err)
		}
	case "-w", "--workdir":
		v, err := take()
		if err != nil {
			return err
		}
		c.WorkingDir = v
	case "--mac-address":
		return fmt.Errorf("runArgs: --mac-address has no Kubernetes equivalent")
	case "--stop-signal":
		return fmt.Errorf("runArgs: --stop-signal has no PodSpec equivalent (Kubernetes always sends SIGTERM, then SIGKILL after terminationGracePeriodSeconds)")
	case "--stop-timeout":
		v, err := take()
		if err != nil {
			return err
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("runArgs: --stop-timeout: %w", err)
		}
		pod.Spec.TerminationGracePeriodSeconds = int64Ptr(n)

	// --------------------------------------------------------------
	// Namespace-style flags. Only "host" maps cleanly onto PodSpec.
	// --------------------------------------------------------------
	case "--network", "--net":
		v, err := take()
		if err != nil {
			return err
		}
		switch v {
		case "host":
			pod.Spec.HostNetwork = true
		case "none", "bridge", "default":
			return fmt.Errorf("runArgs: --network=%q has no Kubernetes equivalent (pod networking is supplied by the cluster CNI)", v)
		default:
			return fmt.Errorf("runArgs: --network=%q has no Kubernetes equivalent (named docker networks don't translate; use NetworkPolicies)", v)
		}
	case "--ipc":
		v, err := take()
		if err != nil {
			return err
		}
		switch v {
		case "host":
			pod.Spec.HostIPC = true
		case "shareable":
			pod.Spec.ShareProcessNamespace = boolPtr(true)
		default:
			return fmt.Errorf("runArgs: --ipc=%q has no Kubernetes equivalent (only host / shareable map cleanly)", v)
		}
	case "--pid":
		v, err := take()
		if err != nil {
			return err
		}
		switch v {
		case "host":
			pod.Spec.HostPID = true
		case "container":
			pod.Spec.ShareProcessNamespace = boolPtr(true)
		default:
			return fmt.Errorf("runArgs: --pid=%q has no Kubernetes equivalent (only host / container map cleanly)", v)
		}
	case "--uts":
		v, err := take()
		if err != nil {
			return err
		}
		if v != "host" {
			return fmt.Errorf("runArgs: --uts=%q has no Kubernetes equivalent", v)
		}
		// PodSpec doesn't expose hostUTS. Closest is the implicit
		// "uts is shared with host" when hostNetwork=true. Otherwise
		// reject.
		return fmt.Errorf("runArgs: --uts=host has no PodSpec equivalent (hostUTS is implied by hostNetwork=true)")
	case "--userns":
		v, err := take()
		if err != nil {
			return err
		}
		_ = v
		return fmt.Errorf("runArgs: --userns has no stable Kubernetes equivalent (UserNamespaces is alpha-gated; configure HostUsers via PodSpec patch)")
	case "--cgroupns", "--cgroup-parent":
		_, err := take()
		if err != nil {
			return err
		}
		return fmt.Errorf("runArgs: %s has no Kubernetes equivalent (cgroups are managed by the kubelet)", flag)
	case "--runtime":
		v, err := take()
		if err != nil {
			return err
		}
		pod.Spec.RuntimeClassName = stringPtr(v)
	case "--restart":
		v, err := take()
		if err != nil {
			return err
		}
		policy, err := mapRestart(v)
		if err != nil {
			return err
		}
		pod.Spec.RestartPolicy = policy
	case "--volume-driver":
		_, err := take()
		if err != nil {
			return err
		}
		return fmt.Errorf("runArgs: --volume-driver has no Kubernetes equivalent")
	case "--isolation":
		_, err := take()
		if err != nil {
			return err
		}
		return fmt.Errorf("runArgs: --isolation is Windows-only on docker; no Kubernetes equivalent")

	// --------------------------------------------------------------
	// Resource limits → Container.Resources.
	// --------------------------------------------------------------
	case "-m", "--memory":
		v, err := take()
		if err != nil {
			return err
		}
		bytes, err := units.RAMInBytes(v)
		if err != nil {
			return fmt.Errorf("runArgs: --memory: %w", err)
		}
		setResourceLimit(c, corev1.ResourceMemory, *resource.NewQuantity(bytes, resource.BinarySI))
	case "--memory-swap":
		_, err := take()
		if err != nil {
			return err
		}
		return fmt.Errorf("runArgs: --memory-swap has no first-class Kubernetes equivalent (swap support is alpha-gated; configure on the kubelet)")
	case "--memory-reservation":
		v, err := take()
		if err != nil {
			return err
		}
		bytes, err := units.RAMInBytes(v)
		if err != nil {
			return fmt.Errorf("runArgs: --memory-reservation: %w", err)
		}
		setResourceRequest(c, corev1.ResourceMemory, *resource.NewQuantity(bytes, resource.BinarySI))
	case "--shm-size":
		v, err := take()
		if err != nil {
			return err
		}
		bytes, err := units.RAMInBytes(v)
		if err != nil {
			return fmt.Errorf("runArgs: --shm-size: %w", err)
		}
		applyShmSize(pod, c, bytes)
	case "--cpus":
		v, err := take()
		if err != nil {
			return err
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return fmt.Errorf("runArgs: --cpus: %w", err)
		}
		// Kubernetes uses milliCPU; "1.5" → "1500m".
		milli := int64(f * 1000)
		setResourceLimit(c, corev1.ResourceCPU, *resource.NewMilliQuantity(milli, resource.DecimalSI))
	case "-c", "--cpu-shares":
		_, err := take()
		if err != nil {
			return err
		}
		return fmt.Errorf("runArgs: --cpu-shares has no first-class Kubernetes equivalent (use Container.Resources.requests.cpu for proportional scheduling)")
	case "--cpuset-cpus", "--cpuset-mems":
		_, err := take()
		if err != nil {
			return err
		}
		return fmt.Errorf("runArgs: %s has no Kubernetes equivalent (cpuset pinning is via the CPUManager / TopologyManager kubelet plugins)", flag)
	case "--pids-limit":
		_, err := take()
		if err != nil {
			return err
		}
		return fmt.Errorf("runArgs: --pids-limit has no PodSpec equivalent (cluster-wide podPidsLimit is set on the kubelet)")
	case "--oom-score-adj":
		_, err := take()
		if err != nil {
			return err
		}
		return fmt.Errorf("runArgs: --oom-score-adj has no PodSpec equivalent (oom adjustment is derived from QoS class)")
	case "--oom-kill-disable":
		if !hasVal {
			// no value form
		} else {
			_ = val
		}
		return fmt.Errorf("runArgs: --oom-kill-disable has no Kubernetes equivalent")

	// --------------------------------------------------------------
	// Repeatable list flags.
	// --------------------------------------------------------------
	case "--cap-add":
		v, err := take()
		if err != nil {
			return err
		}
		sc := ensureSecCtx(c)
		if sc.Capabilities == nil {
			sc.Capabilities = &corev1.Capabilities{}
		}
		sc.Capabilities.Add = append(sc.Capabilities.Add, corev1.Capability(v))
	case "--cap-drop":
		v, err := take()
		if err != nil {
			return err
		}
		sc := ensureSecCtx(c)
		if sc.Capabilities == nil {
			sc.Capabilities = &corev1.Capabilities{}
		}
		sc.Capabilities.Drop = append(sc.Capabilities.Drop, corev1.Capability(v))
	case "--security-opt":
		v, err := take()
		if err != nil {
			return err
		}
		applySecurityOpt(c, v)
	case "--device":
		_, err := take()
		if err != nil {
			return err
		}
		return fmt.Errorf("runArgs: --device has no PodSpec equivalent (use a DevicePlugin or set Container.Resources.limits with the device's resource name, e.g. nvidia.com/gpu)")
	case "--device-cgroup-rule":
		_, err := take()
		if err != nil {
			return err
		}
		return fmt.Errorf("runArgs: --device-cgroup-rule has no Kubernetes equivalent (cgroup access is managed by the kubelet / device plugins)")
	case "-l", "--label":
		v, err := take()
		if err != nil {
			return err
		}
		k, lv := splitKV(v)
		if k == "" {
			return fmt.Errorf("runArgs: --label: empty key in %q", v)
		}
		if !isLabelValueValid(lv) {
			return fmt.Errorf("runArgs: --label %q: value is not a valid Kubernetes label value (use --annotation instead)", v)
		}
		if pod.Labels == nil {
			pod.Labels = map[string]string{}
		}
		pod.Labels[k] = lv
	case "-e", "--env":
		v, err := take()
		if err != nil {
			return err
		}
		if !strings.ContainsRune(v, '=') {
			return fmt.Errorf("runArgs: --env %q: bare KEY form not supported, use KEY=VALUE", v)
		}
		k, ev := splitKV(v)
		c.Env = upsertEnvVar(c.Env, k, ev)
	case "--add-host":
		v, err := take()
		if err != nil {
			return err
		}
		host, ip, ok := strings.Cut(v, ":")
		if !ok {
			return fmt.Errorf("runArgs: --add-host %q: expected hostname:ip", v)
		}
		// docker accepts both "host:ip" and "host=ip"; the form here
		// is "host:ip" since that's the docker-cli-canonical shape.
		appendHostAlias(pod, ip, host)
	case "--dns":
		v, err := take()
		if err != nil {
			return err
		}
		ensureDNSConfig(pod)
		pod.Spec.DNSConfig.Nameservers = append(pod.Spec.DNSConfig.Nameservers, v)
		pod.Spec.DNSPolicy = corev1.DNSNone
	case "--dns-search":
		v, err := take()
		if err != nil {
			return err
		}
		ensureDNSConfig(pod)
		pod.Spec.DNSConfig.Searches = append(pod.Spec.DNSConfig.Searches, v)
	case "--dns-option", "--dns-opt":
		v, err := take()
		if err != nil {
			return err
		}
		ensureDNSConfig(pod)
		// docker --dns-option uses "name:value" (e.g. "ndots:2"), but
		// also accepts "name=value" in some shells. Support both.
		k, dv, ok := strings.Cut(v, ":")
		if !ok {
			k, dv = splitKV(v)
		}
		opt := corev1.PodDNSConfigOption{Name: k}
		if dv != "" {
			optVal := dv
			opt.Value = &optVal
		}
		pod.Spec.DNSConfig.Options = append(pod.Spec.DNSConfig.Options, opt)
	case "--group-add":
		v, err := take()
		if err != nil {
			return err
		}
		gid, err := parseInt64(v)
		if err != nil {
			return fmt.Errorf("runArgs: --group-add %q: Kubernetes supplementalGroups must be numeric", v)
		}
		ensurePodSecCtx(pod).SupplementalGroups = append(ensurePodSecCtx(pod).SupplementalGroups, gid)
	case "--link":
		_, err := take()
		if err != nil {
			return err
		}
		return fmt.Errorf("runArgs: --link is legacy docker container linking with no Kubernetes equivalent (use a Service)")
	case "--volumes-from":
		_, err := take()
		if err != nil {
			return err
		}
		return fmt.Errorf("runArgs: --volumes-from has no Kubernetes equivalent (declare the same Volume on each container's volumeMounts)")
	case "--expose":
		v, err := take()
		if err != nil {
			return err
		}
		ports, err := stringPortSpec(v, "--expose")
		if err != nil {
			return err
		}
		c.Ports = append(c.Ports, ports...)
	case "-p", "--publish":
		v, err := take()
		if err != nil {
			return err
		}
		ports, err := parsePublish(v)
		if err != nil {
			return err
		}
		c.Ports = append(c.Ports, ports...)
	case "--sysctl":
		v, err := take()
		if err != nil {
			return err
		}
		k, sv := splitKV(v)
		if k == "" {
			return fmt.Errorf("runArgs: --sysctl: missing key in %q", v)
		}
		ensurePodSecCtx(pod).Sysctls = append(ensurePodSecCtx(pod).Sysctls, corev1.Sysctl{Name: k, Value: sv})
	case "--ulimit":
		_, err := take()
		if err != nil {
			return err
		}
		return fmt.Errorf("runArgs: --ulimit has no PodSpec equivalent (cluster-wide ulimits are configured on the kubelet / container runtime)")
	case "--tmpfs":
		v, err := take()
		if err != nil {
			return err
		}
		path, _ := splitTmpfs(v)
		name := sanitizeVolumeName("tmpfs-" + path)
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
			Name: name,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory},
			},
		})
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{Name: name, MountPath: path})
	case "-v", "--volume":
		v, err := take()
		if err != nil {
			return err
		}
		vol, vm, err := parseShortVolume(v)
		if err != nil {
			return err
		}
		pod.Spec.Volumes = append(pod.Spec.Volumes, *vol)
		c.VolumeMounts = append(c.VolumeMounts, *vm)
	case "--mount":
		v, err := take()
		if err != nil {
			return err
		}
		vol, vm, err := parseMountString(v, "")
		if err != nil {
			return err
		}
		pod.Spec.Volumes = append(pod.Spec.Volumes, *vol)
		c.VolumeMounts = append(c.VolumeMounts, *vm)
	case "--gpus":
		v, err := take()
		if err != nil {
			return err
		}
		count, resourceName, err := parseGPUs(v)
		if err != nil {
			return err
		}
		setResourceLimit(c, corev1.ResourceName(resourceName), *resource.NewQuantity(int64(count), resource.DecimalSI))
	case "--annotation":
		v, err := take()
		if err != nil {
			return err
		}
		k, av := splitKV(v)
		if k == "" {
			return fmt.Errorf("runArgs: --annotation: empty key in %q", v)
		}
		if pod.Annotations == nil {
			pod.Annotations = map[string]string{}
		}
		pod.Annotations[k] = av

	case "--entrypoint":
		return fmt.Errorf("runArgs: --entrypoint is not supported (the projection owns the keepalive entrypoint)")

	default:
		return fmt.Errorf("runArgs: unsupported flag %q", flag)
	}
	return nil
}

// upsertEnvVar replaces by name or appends.
func upsertEnvVar(env []corev1.EnvVar, k, v string) []corev1.EnvVar {
	for i := range env {
		if env[i].Name == k {
			env[i].Value = v
			return env
		}
	}
	return append(env, corev1.EnvVar{Name: k, Value: v})
}

func setResourceLimit(c *corev1.Container, name corev1.ResourceName, q resource.Quantity) {
	if c.Resources.Limits == nil {
		c.Resources.Limits = corev1.ResourceList{}
	}
	c.Resources.Limits[name] = q
}

func setResourceRequest(c *corev1.Container, name corev1.ResourceName, q resource.Quantity) {
	if c.Resources.Requests == nil {
		c.Resources.Requests = corev1.ResourceList{}
	}
	c.Resources.Requests[name] = q
}

// applyShmSize translates --shm-size to a memory-medium emptyDir
// mounted at /dev/shm with a sizeLimit, the same shape kubelet
// enforces with sizeMemoryBackedVolumes.
func applyShmSize(pod *corev1.Pod, c *corev1.Container, bytes int64) {
	q := resource.NewQuantity(bytes, resource.BinarySI)
	pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{
		Name: "shm",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{
				Medium:    corev1.StorageMediumMemory,
				SizeLimit: q,
			},
		},
	})
	c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{Name: "shm", MountPath: "/dev/shm"})
}

func ensurePodSecCtx(p *corev1.Pod) *corev1.PodSecurityContext {
	if p.Spec.SecurityContext == nil {
		p.Spec.SecurityContext = &corev1.PodSecurityContext{}
	}
	return p.Spec.SecurityContext
}

func ensureDNSConfig(p *corev1.Pod) {
	if p.Spec.DNSConfig == nil {
		p.Spec.DNSConfig = &corev1.PodDNSConfig{}
	}
}

func appendHostAlias(p *corev1.Pod, ip, host string) {
	for i := range p.Spec.HostAliases {
		if p.Spec.HostAliases[i].IP == ip {
			p.Spec.HostAliases[i].Hostnames = append(p.Spec.HostAliases[i].Hostnames, host)
			return
		}
	}
	p.Spec.HostAliases = append(p.Spec.HostAliases, corev1.HostAlias{IP: ip, Hostnames: []string{host}})
}

// mapRestart translates docker --restart values onto pod restartPolicy.
func mapRestart(s string) (corev1.RestartPolicy, error) {
	name, _, _ := strings.Cut(s, ":")
	switch name {
	case "no":
		return corev1.RestartPolicyNever, nil
	case "always", "unless-stopped":
		return corev1.RestartPolicyAlways, nil
	case "on-failure":
		return corev1.RestartPolicyOnFailure, nil
	}
	return "", fmt.Errorf("runArgs: --restart %q: unknown policy", s)
}

// parseGPUs translates the docker --gpus shorthand into a Kubernetes
// resource entry (count, resourceName). Device-ID forms are rejected
// because they assume host-local indices the cluster scheduler can't
// reason about.
func parseGPUs(s string) (count int, resourceName string, err error) {
	s = strings.TrimSpace(s)
	resourceName = "nvidia.com/gpu"
	if s == "all" || s == `"all"` || s == `'all'` {
		// "all" means "as many as the host has", which Kubernetes can't
		// represent — the resource model is per-pod allocation. Pick 1
		// as a documented default; the embedder can override.
		return 1, resourceName, nil
	}
	if n, err := strconv.Atoi(strings.Trim(s, `"'`)); err == nil {
		if n < 1 {
			return 0, "", fmt.Errorf("runArgs: --gpus %q: count must be >= 1", s)
		}
		return n, resourceName, nil
	}
	// CSV form. We accept count=N and driver=nvidia (which sets the
	// resource name); device=ID is rejected, capabilities is ignored.
	for _, field := range splitFields(s) {
		k, v, ok := strings.Cut(field, "=")
		if !ok {
			return 0, "", fmt.Errorf("runArgs: --gpus: expected key=value, got %q", field)
		}
		switch k {
		case "count":
			if v == "all" {
				count = 1
				continue
			}
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return 0, "", fmt.Errorf("runArgs: --gpus count=%q: %w", v, err)
			}
			count = n
		case "device":
			return 0, "", fmt.Errorf("runArgs: --gpus device=%q: device IDs are host-local and not addressable from a Kubernetes pod (use a node selector / device plugin)", v)
		case "driver":
			switch v {
			case "nvidia":
				resourceName = "nvidia.com/gpu"
			case "amd":
				resourceName = "amd.com/gpu"
			default:
				resourceName = v + "/gpu"
			}
		case "capabilities", "options":
			// Capabilities (gpu, utility, compute) and provider-specific
			// options are configured on the device plugin / runtime in
			// Kubernetes; they don't have a per-pod surface.
		default:
			return 0, "", fmt.Errorf("runArgs: --gpus: unknown key %q", k)
		}
	}
	if count == 0 {
		count = 1
	}
	return count, resourceName, nil
}

// splitFields splits a comma-separated list, honoring CSV-style quoting
// so "device=GPU-a,GPU-b" survives — same trick the docker host's
// parseGPUs uses, but minimal since we reject device= anyway.
func splitFields(s string) []string {
	if !strings.ContainsRune(s, '"') {
		return strings.Split(s, ",")
	}
	out := []string{}
	var buf strings.Builder
	inQuote := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuote = !inQuote
		case r == ',' && !inQuote:
			out = append(out, buf.String())
			buf.Reset()
		default:
			buf.WriteRune(r)
		}
	}
	if buf.Len() > 0 {
		out = append(out, buf.String())
	}
	return out
}

// parsePublish translates "host:container[/proto]" / "container[/proto]"
// into ContainerPort entries with hostPort set when the host side is
// explicit. The "iface:host:container" form rejects: pod hostPort is
// numeric, no interface concept.
func parsePublish(s string) ([]corev1.ContainerPort, error) {
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 1:
		return stringPortSpec(parts[0], "--publish")
	case 2:
		hostPart := parts[0]
		containerPart, proto, hasProto := strings.Cut(parts[1], "/")
		host, err := strconv.Atoi(hostPart)
		if err != nil {
			return nil, fmt.Errorf("runArgs: --publish %q: host port must be numeric", s)
		}
		container, err := strconv.Atoi(containerPart)
		if err != nil {
			return nil, fmt.Errorf("runArgs: --publish %q: container port must be numeric", s)
		}
		pp := corev1.ProtocolTCP
		if hasProto {
			switch strings.ToLower(proto) {
			case "tcp":
				pp = corev1.ProtocolTCP
			case "udp":
				pp = corev1.ProtocolUDP
			case "sctp":
				pp = corev1.ProtocolSCTP
			default:
				return nil, fmt.Errorf("runArgs: --publish %q: unknown protocol %q", s, proto)
			}
		}
		return []corev1.ContainerPort{{HostPort: int32(host), ContainerPort: int32(container), Protocol: pp}}, nil
	case 3:
		return nil, fmt.Errorf("runArgs: --publish %q: iface:host:container form has no Kubernetes equivalent (hostPort binds on every node, not a specific interface)", s)
	}
	return nil, fmt.Errorf("runArgs: --publish %q: malformed", s)
}

// parseShortVolume parses the "/host:/container[:ro]" short-form -v
// flag into a Volume + VolumeMount. The named-volume form
// ("name:/container") is rejected — same reason mounts.go rejects
// type=volume.
func parseShortVolume(s string) (*corev1.Volume, *corev1.VolumeMount, error) {
	parts := strings.SplitN(s, ":", 3)
	if len(parts) < 2 {
		return nil, nil, fmt.Errorf("runArgs: --volume %q: expected /host:/container[:ro]", s)
	}
	src, dst := parts[0], parts[1]
	if !strings.HasPrefix(src, "/") {
		return nil, nil, fmt.Errorf("runArgs: --volume %q: named volumes are not supported on Kubernetes (use a PVC declared on pod.Spec.Volumes)", s)
	}
	if !strings.HasPrefix(dst, "/") {
		return nil, nil, fmt.Errorf("runArgs: --volume %q: container path must be absolute", s)
	}
	readOnly := false
	if len(parts) == 3 {
		for _, opt := range strings.Split(parts[2], ",") {
			switch strings.TrimSpace(opt) {
			case "ro":
				readOnly = true
			case "rw", "":
				// default
			default:
				// Other options (z, Z, cached, delegated, consistent)
				// are docker-runtime specifics. Drop silently — the
				// Kubernetes mount has no equivalent.
			}
		}
	}
	name := sanitizeVolumeName(src)
	return &corev1.Volume{
			Name:         name,
			VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: src}},
		},
		&corev1.VolumeMount{Name: name, MountPath: dst, ReadOnly: readOnly},
		nil
}

func splitFlag(a string) (flag, val string, hasVal bool) {
	if eq := strings.IndexByte(a, '='); eq >= 0 {
		return a[:eq], a[eq+1:], true
	}
	return a, "", false
}

func isCombinedBoolShort(a string) bool {
	if len(a) < 3 || a[0] != '-' || a[1] == '-' {
		return false
	}
	for _, c := range a[1:] {
		if c != 'i' && c != 't' && c != 'd' {
			return false
		}
	}
	return true
}

func splitKV(s string) (k, v string) {
	if eq := strings.IndexByte(s, '='); eq >= 0 {
		return s[:eq], s[eq+1:]
	}
	return s, ""
}

func splitTmpfs(s string) (path, opts string) {
	if i := strings.IndexByte(s, ':'); i >= 0 {
		return s[:i], s[i+1:]
	}
	return s, ""
}

// isLabelValueValid is a Kubernetes label value validator. The full
// regex is "(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?" with a max
// length of 63. We approximate it: empty string is valid; otherwise
// reject any character that would fail.
func isLabelValueValid(v string) bool {
	if v == "" {
		return true
	}
	if len(v) > 63 {
		return false
	}
	first := v[0]
	last := v[len(v)-1]
	if !isAlphaNum(first) || !isAlphaNum(last) {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if !isAlphaNum(c) && c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	return true
}

func isAlphaNum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}
