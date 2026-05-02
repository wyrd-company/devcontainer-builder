package kubernetes

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// projectArgs is a test helper that runs Project with a runArgs list,
// returning the resulting pod & first container so individual cases
// don't have to rebuild the spec each time.
func projectArgs(t *testing.T, args ...string) *corev1.Pod {
	t.Helper()
	pj, err := Project(ProjectRequest{
		Spec:  &spec.DevContainer{RunArgs: args},
		Image: "x",
	})
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	return pj.Pod
}

func projectArgsErr(t *testing.T, args ...string) error {
	t.Helper()
	_, err := Project(ProjectRequest{
		Spec:  &spec.DevContainer{RunArgs: args},
		Image: "x",
	})
	return err
}

func TestRunArgs_PrivilegedAndCaps(t *testing.T) {
	pod := projectArgs(t, "--privileged", "--cap-add=NET_ADMIN", "--cap-drop", "MKNOD")
	sc := pod.Spec.Containers[0].SecurityContext
	if sc == nil || sc.Privileged == nil || !*sc.Privileged {
		t.Fatal("Privileged not set")
	}
	if sc.Capabilities == nil {
		t.Fatal("Capabilities nil")
	}
	if len(sc.Capabilities.Add) != 1 || string(sc.Capabilities.Add[0]) != "NET_ADMIN" {
		t.Errorf("Capabilities.Add = %+v", sc.Capabilities.Add)
	}
	if len(sc.Capabilities.Drop) != 1 || string(sc.Capabilities.Drop[0]) != "MKNOD" {
		t.Errorf("Capabilities.Drop = %+v", sc.Capabilities.Drop)
	}
}

func TestRunArgs_User(t *testing.T) {
	pod := projectArgs(t, "-u", "1000")
	sc := pod.Spec.Containers[0].SecurityContext
	if sc == nil || sc.RunAsUser == nil || *sc.RunAsUser != 1000 {
		t.Fatalf("RunAsUser = %v", sc)
	}

	// A named --user is rejected (Kubernetes runAsUser is numeric).
	if err := projectArgsErr(t, "--user=node"); err == nil {
		t.Error("expected error for non-numeric --user")
	}
}

func TestRunArgs_Memory(t *testing.T) {
	pod := projectArgs(t, "--memory=512m")
	c := pod.Spec.Containers[0]
	q, ok := c.Resources.Limits[corev1.ResourceMemory]
	if !ok {
		t.Fatal("Resources.Limits.memory not set")
	}
	want := *resource.NewQuantity(512*1024*1024, resource.BinarySI)
	if q.Cmp(want) != 0 {
		t.Errorf("memory = %s, want %s", q.String(), want.String())
	}
}

func TestRunArgs_Cpus(t *testing.T) {
	pod := projectArgs(t, "--cpus=1.5")
	q := pod.Spec.Containers[0].Resources.Limits[corev1.ResourceCPU]
	if q.MilliValue() != 1500 {
		t.Errorf("cpu milli = %d, want 1500", q.MilliValue())
	}
}

func TestRunArgs_ShmSize(t *testing.T) {
	pod := projectArgs(t, "--shm-size=256m")
	var found bool
	for _, v := range pod.Spec.Volumes {
		if v.Name == "shm" {
			found = true
			if v.EmptyDir == nil || v.EmptyDir.Medium != corev1.StorageMediumMemory {
				t.Errorf("shm volume = %+v, want EmptyDir Memory", v)
			}
			if v.EmptyDir.SizeLimit == nil || v.EmptyDir.SizeLimit.Value() != 256*1024*1024 {
				t.Errorf("shm sizeLimit = %v, want 256Mi", v.EmptyDir.SizeLimit)
			}
		}
	}
	if !found {
		t.Error("expected /dev/shm volume")
	}
}

func TestRunArgs_Env(t *testing.T) {
	pod := projectArgs(t, "-e", "FOO=bar", "-e", "BAZ=qux")
	got := envAsMap(pod.Spec.Containers[0].Env)
	if got["FOO"] != "bar" || got["BAZ"] != "qux" {
		t.Errorf("env = %+v", got)
	}
	// Bare KEY form (no =) is rejected: Kubernetes doesn't have a
	// "pass through from host environ" semantic.
	if err := projectArgsErr(t, "-e", "FOO"); err == nil {
		t.Error("expected error for bare KEY")
	}
}

func TestRunArgs_AddHostAndDNS(t *testing.T) {
	pod := projectArgs(t,
		"--add-host=svc.local:10.0.0.1",
		"--add-host=other.local:10.0.0.1",
		"--dns=8.8.8.8",
		"--dns-search=internal",
		"--dns-option=ndots:2",
	)
	if len(pod.Spec.HostAliases) != 1 {
		t.Fatalf("HostAliases = %+v, want one entry merging both hosts", pod.Spec.HostAliases)
	}
	if pod.Spec.HostAliases[0].IP != "10.0.0.1" || len(pod.Spec.HostAliases[0].Hostnames) != 2 {
		t.Errorf("HostAlias entry = %+v", pod.Spec.HostAliases[0])
	}
	if pod.Spec.DNSConfig == nil || pod.Spec.DNSConfig.Nameservers[0] != "8.8.8.8" {
		t.Errorf("DNSConfig.Nameservers = %+v", pod.Spec.DNSConfig)
	}
	if pod.Spec.DNSConfig.Searches[0] != "internal" {
		t.Errorf("DNSConfig.Searches = %+v", pod.Spec.DNSConfig.Searches)
	}
	if len(pod.Spec.DNSConfig.Options) != 1 || pod.Spec.DNSConfig.Options[0].Name != "ndots" {
		t.Errorf("DNSConfig.Options = %+v", pod.Spec.DNSConfig.Options)
	}
}

func TestRunArgs_Restart(t *testing.T) {
	cases := []struct {
		in   string
		want corev1.RestartPolicy
	}{
		{"no", corev1.RestartPolicyNever},
		{"always", corev1.RestartPolicyAlways},
		{"unless-stopped", corev1.RestartPolicyAlways},
		{"on-failure", corev1.RestartPolicyOnFailure},
		{"on-failure:5", corev1.RestartPolicyOnFailure},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			pod := projectArgs(t, "--restart="+tc.in)
			if pod.Spec.RestartPolicy != tc.want {
				t.Errorf("RestartPolicy = %q, want %q", pod.Spec.RestartPolicy, tc.want)
			}
		})
	}
}

func TestRunArgs_NamespaceFlags(t *testing.T) {
	pod := projectArgs(t, "--network=host", "--ipc=host", "--pid=host")
	if !pod.Spec.HostNetwork {
		t.Error("HostNetwork not set")
	}
	if !pod.Spec.HostIPC {
		t.Error("HostIPC not set")
	}
	if !pod.Spec.HostPID {
		t.Error("HostPID not set")
	}
}

func TestRunArgs_NamespaceFlagsRejectUnsupported(t *testing.T) {
	cases := []string{
		"--network=bridge",
		"--ipc=private",
		"--uts=host",
		"--userns=keep-id",
		"--cgroupns=private",
		"--cgroup-parent=/kubepods.slice",
	}
	for _, flag := range cases {
		t.Run(flag, func(t *testing.T) {
			parts := splitFlagForTest(flag)
			err := projectArgsErr(t, parts...)
			if err == nil {
				t.Errorf("expected error for %s", flag)
			}
		})
	}
}

func TestRunArgs_PublishHostPort(t *testing.T) {
	pod := projectArgs(t, "-p", "8080:80/tcp")
	ports := pod.Spec.Containers[0].Ports
	if len(ports) != 1 {
		t.Fatalf("ports = %+v", ports)
	}
	if ports[0].HostPort != 8080 || ports[0].ContainerPort != 80 || ports[0].Protocol != corev1.ProtocolTCP {
		t.Errorf("port = %+v", ports[0])
	}
	// iface:host:container is rejected because pod hostPort doesn't
	// pin to a specific NIC.
	if err := projectArgsErr(t, "-p", "127.0.0.1:8080:80"); err == nil {
		t.Error("expected error for iface:host:container")
	}
}

func TestRunArgs_VolumeShortForm(t *testing.T) {
	pod := projectArgs(t, "-v", "/host/data:/data:ro")
	c := pod.Spec.Containers[0]
	if len(c.VolumeMounts) != 1 || c.VolumeMounts[0].MountPath != "/data" || !c.VolumeMounts[0].ReadOnly {
		t.Errorf("VolumeMounts = %+v", c.VolumeMounts)
	}
	if len(pod.Spec.Volumes) != 1 || pod.Spec.Volumes[0].HostPath == nil {
		t.Errorf("Volumes = %+v", pod.Spec.Volumes)
	}

	// Named-volume short-form is rejected.
	if err := projectArgsErr(t, "-v", "node-modules:/app/node_modules"); err == nil {
		t.Error("expected error for named volume short-form")
	}
}

func TestRunArgs_Sysctl(t *testing.T) {
	pod := projectArgs(t, "--sysctl=net.core.somaxconn=1024")
	sc := pod.Spec.SecurityContext
	if sc == nil || len(sc.Sysctls) != 1 {
		t.Fatalf("Sysctls = %+v", sc)
	}
	if sc.Sysctls[0].Name != "net.core.somaxconn" || sc.Sysctls[0].Value != "1024" {
		t.Errorf("Sysctl = %+v", sc.Sysctls[0])
	}
}

func TestRunArgs_GroupAdd(t *testing.T) {
	pod := projectArgs(t, "--group-add", "1000", "--group-add", "1001")
	sc := pod.Spec.SecurityContext
	if sc == nil || len(sc.SupplementalGroups) != 2 {
		t.Fatalf("SupplementalGroups = %+v", sc)
	}
	if sc.SupplementalGroups[0] != 1000 || sc.SupplementalGroups[1] != 1001 {
		t.Errorf("SupplementalGroups = %+v", sc.SupplementalGroups)
	}
	// Named groups are rejected.
	if err := projectArgsErr(t, "--group-add", "wheel"); err == nil {
		t.Error("expected error for non-numeric group")
	}
}

func TestRunArgs_GPUs(t *testing.T) {
	pod := projectArgs(t, "--gpus=2")
	c := pod.Spec.Containers[0]
	q, ok := c.Resources.Limits["nvidia.com/gpu"]
	if !ok {
		t.Fatalf("Limits = %+v", c.Resources.Limits)
	}
	if q.Value() != 2 {
		t.Errorf("nvidia.com/gpu = %s, want 2", q.String())
	}

	// Driver= controls the resource name.
	pod = projectArgs(t, "--gpus=count=1,driver=amd")
	if _, ok := pod.Spec.Containers[0].Resources.Limits["amd.com/gpu"]; !ok {
		t.Errorf("expected amd.com/gpu, got %+v", pod.Spec.Containers[0].Resources.Limits)
	}

	// Device IDs are rejected (host-local indices).
	if err := projectArgsErr(t, "--gpus=device=GPU-uuid"); err == nil {
		t.Error("expected error for --gpus device=")
	}
}

func TestRunArgs_RejectInit(t *testing.T) {
	if err := projectArgsErr(t, "--init"); err == nil {
		t.Error("expected error for --init")
	}
}

func TestRunArgs_RejectEntrypoint(t *testing.T) {
	if err := projectArgsErr(t, "--entrypoint=/bin/bash"); err == nil {
		t.Error("expected error for --entrypoint")
	}
}

func TestRunArgs_TolerateNoOpDockerFlags(t *testing.T) {
	// These mirror the docker host's no-op set: -i / -t / -d / --rm /
	// --sig-proxy. They should produce a valid pod with no extra
	// fields.
	pod := projectArgs(t, "-it", "--rm", "--sig-proxy=false")
	if len(pod.Spec.Containers[0].Env) != 0 {
		t.Errorf("expected no env from no-op flags, got %+v", pod.Spec.Containers[0].Env)
	}
}

func TestRunArgs_LabelValueValidation(t *testing.T) {
	pod := projectArgs(t, "-l", "team=infra")
	if pod.Labels["team"] != "infra" {
		t.Errorf("Labels = %+v", pod.Labels)
	}
	// A path-like label value is rejected (Kubernetes labels can't
	// contain '/', etc.).
	if err := projectArgsErr(t, "-l", "path=/host/proj"); err == nil {
		t.Error("expected error for path-like label value")
	}
}

func TestRunArgs_RuntimeClass(t *testing.T) {
	pod := projectArgs(t, "--runtime=gvisor")
	if pod.Spec.RuntimeClassName == nil || *pod.Spec.RuntimeClassName != "gvisor" {
		t.Errorf("RuntimeClassName = %v, want gvisor", pod.Spec.RuntimeClassName)
	}
}

func TestRunArgs_Hostname(t *testing.T) {
	pod := projectArgs(t, "--hostname=devbox")
	if pod.Spec.Hostname != "devbox" {
		t.Errorf("Hostname = %q", pod.Spec.Hostname)
	}
}

func TestRunArgs_StopTimeout(t *testing.T) {
	pod := projectArgs(t, "--stop-timeout=42")
	if pod.Spec.TerminationGracePeriodSeconds == nil || *pod.Spec.TerminationGracePeriodSeconds != 42 {
		t.Errorf("TerminationGracePeriod = %v", pod.Spec.TerminationGracePeriodSeconds)
	}
}

func TestRunArgs_Annotation(t *testing.T) {
	pod := projectArgs(t, "--annotation", "owner=alice")
	if pod.Annotations["owner"] != "alice" {
		t.Errorf("Annotations = %+v", pod.Annotations)
	}
}

// splitFlagForTest mimics how a runArgs parser receives "--foo=bar"
// vs "--foo bar" — pass through unchanged.
func splitFlagForTest(s string) []string {
	if !strings.Contains(s, "=") {
		return []string{s}
	}
	return []string{s}
}
