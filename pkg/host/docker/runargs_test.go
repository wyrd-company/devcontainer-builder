package docker

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// applyForTest is a thin convenience that runs applyRunArgs against a
// freshly-zeroed Config/HostConfig pair so tests can read the exact
// fields runArgs touched without baseline noise from buildContainerConfig.
func applyForTest(t *testing.T, args []string) (*container.Config, *container.HostConfig) {
	t.Helper()
	cfg := &container.Config{}
	host := &container.HostConfig{}
	if _, err := applyRunArgs(args, cfg, host); err != nil {
		t.Fatalf("applyRunArgs(%v): %v", args, err)
	}
	return cfg, host
}

func TestApplyRunArgs_BooleanFlags(t *testing.T) {
	_, host := applyForTest(t, []string{"--init", "--privileged", "--read-only", "-P"})
	if host.Init == nil || !*host.Init {
		t.Errorf("--init: got %+v", host.Init)
	}
	if !host.Privileged {
		t.Errorf("--privileged not set")
	}
	if !host.ReadonlyRootfs {
		t.Errorf("--read-only not set")
	}
	if !host.PublishAllPorts {
		t.Errorf("-P not set")
	}
}

func TestApplyRunArgs_CombinedShortBooleans(t *testing.T) {
	for _, combo := range []string{"-it", "-ti", "-itd", "-dit", "-itP"} {
		cfg, host := applyForTest(t, []string{combo})
		if !cfg.OpenStdin || !cfg.Tty {
			t.Fatalf("%s: stdin=%v tty=%v", combo, cfg.OpenStdin, cfg.Tty)
		}
		if combo == "-itP" && !host.PublishAllPorts {
			t.Fatal("publish all lost")
		}
	}
}

func TestApplyRunArgs_BooleanAssignments(t *testing.T) {
	cfg, host := applyForTest(t, []string{"--rm", "-it", "--privileged", "--read-only", "--init=true", "--privileged=false", "--read-only=false", "--publish-all=true"})
	if !host.AutoRemove || !cfg.OpenStdin || !cfg.Tty || host.Init == nil || !*host.Init || host.Privileged || host.ReadonlyRootfs || !host.PublishAllPorts {
		t.Fatalf("config=%+v host=%+v", cfg, host)
	}
	cfg, host = applyForTest(t, []string{"--rm=false", "-i=false", "-t=false", "--init=false", "-P=false"})
	if host.AutoRemove || cfg.OpenStdin || cfg.Tty || host.Init == nil || *host.Init || host.PublishAllPorts {
		t.Fatal("false assignments lost")
	}
	for _, flag := range []string{"--init", "--privileged", "--read-only", "--publish-all", "--rm", "-i", "-t", "-d", "--sig-proxy"} {
		if _, _, err := tryApply([]string{flag + "=invalid"}); err == nil {
			t.Errorf("%s accepted invalid boolean", flag)
		}
	}
}

func TestApplyRunArgs_AttachedShortValues(t *testing.T) {
	cfg, host := applyForTest(t, []string{"-hwork", "-u1000:1000", "-w/work", "-eSAMPLE=value", "-lkind=sample", "-p9080:8080", "-v/data:/data", "-m64m", "-c512"})
	if cfg.Hostname != "work" || cfg.User != "1000:1000" || cfg.WorkingDir != "/work" || !reflect.DeepEqual(cfg.Env, []string{"SAMPLE=value"}) || cfg.Labels["kind"] != "sample" || len(host.PortBindings) != 1 || len(host.Binds) != 1 || host.Memory != 64*1024*1024 || host.CPUShares != 512 {
		t.Fatalf("config=%+v host=%+v", cfg, host)
	}
	cfg, _ = applyForTest(t, []string{"-iteSAMPLE=value"})
	if !cfg.OpenStdin || !cfg.Tty || !reflect.DeepEqual(cfg.Env, []string{"SAMPLE=value"}) {
		t.Fatalf("mixed cluster: %+v", cfg)
	}
}

func TestApplyRunArgs_EqualsAndSeparateForms(t *testing.T) {
	// --network=host and --network host should be equivalent.
	_, hostA := applyForTest(t, []string{"--network=host"})
	_, hostB := applyForTest(t, []string{"--network", "host"})
	if hostA.NetworkMode != "host" || hostB.NetworkMode != "host" {
		t.Errorf("network mode: A=%q B=%q", hostA.NetworkMode, hostB.NetworkMode)
	}
}

func TestApplyRunArgs_NamespaceFlags(t *testing.T) {
	_, host := applyForTest(t, []string{
		"--ipc=host", "--pid=host", "--uts=host",
		"--userns=host", "--cgroupns=host",
		"--cgroup-parent=/foo", "--runtime=runc",
	})
	if host.IpcMode != "host" || host.PidMode != "host" || host.UTSMode != "host" {
		t.Errorf("ns: %+v", host)
	}
	if host.UsernsMode != "host" || host.CgroupnsMode != "host" {
		t.Errorf("userns/cgroupns: %+v", host)
	}
	if host.CgroupParent != "/foo" || host.Runtime != "runc" {
		t.Errorf("cgroup parent / runtime: %+v", host)
	}
}

func TestApplyRunArgs_Restart(t *testing.T) {
	_, host := applyForTest(t, []string{"--restart=on-failure:5"})
	if host.RestartPolicy.Name != container.RestartPolicyOnFailure {
		t.Errorf("policy name: %q", host.RestartPolicy.Name)
	}
	if host.RestartPolicy.MaximumRetryCount != 5 {
		t.Errorf("retries: %d", host.RestartPolicy.MaximumRetryCount)
	}
	if _, _, err := tryApply([]string{"--restart", "always:3"}); err == nil {
		t.Error("retry count with always should be rejected")
	}
}

func TestApplyRunArgs_ConfigFields(t *testing.T) {
	cfg, _ := applyForTest(t, []string{
		"--hostname=foo", "-u", "1000:1000", "--workdir=/w",
		"--mac-address=02:42:ac:11:00:02", "--stop-signal=SIGINT",
		"--stop-timeout", "30",
	})
	if cfg.Hostname != "foo" || cfg.User != "1000:1000" || cfg.WorkingDir != "/w" {
		t.Errorf("cfg: %+v", cfg)
	}
	if cfg.MacAddress != "02:42:ac:11:00:02" || cfg.StopSignal != "SIGINT" {
		t.Errorf("mac/stop-signal: %+v", cfg)
	}
	if cfg.StopTimeout == nil || *cfg.StopTimeout != 30 {
		t.Errorf("stop-timeout: %+v", cfg.StopTimeout)
	}
}

func TestApplyRunArgs_ResourceLimits(t *testing.T) {
	_, host := applyForTest(t, []string{
		"--memory=512m", "--memory-swap=1g", "--memory-reservation=256m",
		"--shm-size=64m", "--cpus=1.5", "--cpu-shares=512",
		"--cpuset-cpus=0-1", "--cpuset-mems=0",
		"--pids-limit=100", "--oom-score-adj=-500",
	})
	if host.Memory != 512*1024*1024 {
		t.Errorf("memory: %d", host.Memory)
	}
	if host.MemorySwap != 1024*1024*1024 {
		t.Errorf("memory-swap: %d", host.MemorySwap)
	}
	if host.MemoryReservation != 256*1024*1024 {
		t.Errorf("memory-reservation: %d", host.MemoryReservation)
	}
	if host.ShmSize != 64*1024*1024 {
		t.Errorf("shm-size: %d", host.ShmSize)
	}
	if host.NanoCPUs != 1_500_000_000 {
		t.Errorf("cpus: %d", host.NanoCPUs)
	}
	if host.CPUShares != 512 {
		t.Errorf("cpu-shares: %d", host.CPUShares)
	}
	if host.CpusetCpus != "0-1" || host.CpusetMems != "0" {
		t.Errorf("cpuset: %+v", host)
	}
	if host.PidsLimit == nil || *host.PidsLimit != 100 {
		t.Errorf("pids-limit: %+v", host.PidsLimit)
	}
	if host.OomScoreAdj != -500 {
		t.Errorf("oom-score-adj: %d", host.OomScoreAdj)
	}
}

func TestApplyRunArgs_MemorySwapUnlimited(t *testing.T) {
	_, host := applyForTest(t, []string{"--memory-swap=-1"})
	if host.MemorySwap != -1 {
		t.Errorf("memory-swap=-1 should set -1, got %d", host.MemorySwap)
	}
}

func TestApplyRunArgs_RepeatableLists(t *testing.T) {
	_, host := applyForTest(t, []string{
		"--cap-add=SYS_PTRACE", "--cap-add", "NET_ADMIN",
		"--cap-drop=AUDIT_WRITE",
		"--security-opt=seccomp=unconfined",
		"--add-host=host.docker.internal:host-gateway",
		"--dns=8.8.8.8", "--dns-search=example.com", "--dns-option=ndots:1",
		"--group-add=docker",
	})
	wantCapAdd := []string{"SYS_PTRACE", "NET_ADMIN"}
	if !reflect.DeepEqual([]string(host.CapAdd), wantCapAdd) {
		t.Errorf("cap-add: %+v", host.CapAdd)
	}
	if !reflect.DeepEqual([]string(host.CapDrop), []string{"AUDIT_WRITE"}) {
		t.Errorf("cap-drop: %+v", host.CapDrop)
	}
	if !reflect.DeepEqual(host.SecurityOpt, []string{"seccomp=unconfined"}) {
		t.Errorf("security-opt: %+v", host.SecurityOpt)
	}
	if !reflect.DeepEqual(host.ExtraHosts, []string{"host.docker.internal:host-gateway"}) {
		t.Errorf("extra-hosts: %+v", host.ExtraHosts)
	}
	if !reflect.DeepEqual(host.DNS, []string{"8.8.8.8"}) || !reflect.DeepEqual(host.DNSSearch, []string{"example.com"}) {
		t.Errorf("dns: %+v %+v", host.DNS, host.DNSSearch)
	}
	if !reflect.DeepEqual(host.DNSOptions, []string{"ndots:1"}) {
		t.Errorf("dns-options: %+v", host.DNSOptions)
	}
	if !reflect.DeepEqual(host.GroupAdd, []string{"docker"}) {
		t.Errorf("group-add: %+v", host.GroupAdd)
	}
}

func TestApplyRunArgs_VolumesAndMounts(t *testing.T) {
	_, host := applyForTest(t, []string{
		"-v", "/host:/cont:ro",
		"--volume", "/host2:/cont2",
		"--mount", "type=bind,source=/m,target=/m",
		"--tmpfs", "/run:size=64m",
	})
	if !reflect.DeepEqual(host.Binds, []string{"/host:/cont:ro", "/host2:/cont2"}) {
		t.Errorf("binds: %+v", host.Binds)
	}
	if len(host.Mounts) != 1 || host.Mounts[0].Source != "/m" || host.Mounts[0].Target != "/m" {
		t.Errorf("mounts: %+v", host.Mounts)
	}
	if host.Tmpfs["/run"] != "size=64m" {
		t.Errorf("tmpfs: %+v", host.Tmpfs)
	}
}

func TestApplyRunArgs_EnvAndLabels(t *testing.T) {
	cfg, _ := applyForTest(t, []string{
		"-e", "FOO=bar", "--env", "BAZ=qux",
		"-l", "owner=team", "--label", "tier=dev",
	})
	gotEnv := append([]string{}, cfg.Env...)
	sort.Strings(gotEnv)
	if !reflect.DeepEqual(gotEnv, []string{"BAZ=qux", "FOO=bar"}) {
		t.Errorf("env: %+v", cfg.Env)
	}
	if cfg.Labels["owner"] != "team" || cfg.Labels["tier"] != "dev" {
		t.Errorf("labels: %+v", cfg.Labels)
	}
}

func TestApplyRunArgs_EnvironmentInheritance(t *testing.T) {
	t.Setenv("SAMPLE_INHERITED", "host=value")
	t.Setenv("SAMPLE_EMPTY", "")
	t.Setenv("SAMPLE_UNSET", "temporary")
	if err := os.Unsetenv("SAMPLE_UNSET"); err != nil {
		t.Fatal(err)
	}
	cfg, _ := applyForTest(t, []string{"--env", "SAMPLE_INHERITED", "-eSAMPLE_EMPTY", "--env=SAMPLE_UNSET"})
	if !reflect.DeepEqual(cfg.Env, []string{"SAMPLE_INHERITED=host=value", "SAMPLE_EMPTY=", "SAMPLE_UNSET"}) {
		t.Fatalf("env=%q", cfg.Env)
	}
}

func TestApplyRunArgs_EnvironmentOrder(t *testing.T) {
	t.Setenv("SAMPLE_UNSET", "temporary")
	if err := os.Unsetenv("SAMPLE_UNSET"); err != nil {
		t.Fatal(err)
	}
	cfg, _ := applyForTest(t, []string{"-eSAMPLE_UNSET=first", "-eSAMPLE_UNSET", "-eSAMPLE_UNSET=last"})
	if !reflect.DeepEqual(cfg.Env, []string{"SAMPLE_UNSET=first", "SAMPLE_UNSET", "SAMPLE_UNSET=last"}) {
		t.Fatalf("env order=%q", cfg.Env)
	}
	for _, arg := range []string{"", "=value"} {
		if _, _, err := tryApply([]string{"--env", arg}); err == nil {
			t.Errorf("empty key %q accepted", arg)
		}
	}
}

func TestApplyRunArgs_PublishAndExpose(t *testing.T) {
	cfg, host := applyForTest(t, []string{
		"-p", "8080:80/tcp",
		"--publish", "127.0.0.1:9000:9000",
		"--expose", "5000",
	})
	port80, _ := nat.NewPort("tcp", "80")
	if _, ok := cfg.ExposedPorts[port80]; !ok {
		t.Errorf("80/tcp not exposed: %+v", cfg.ExposedPorts)
	}
	if len(host.PortBindings[port80]) != 1 || host.PortBindings[port80][0].HostPort != "8080" {
		t.Errorf("8080:80 binding: %+v", host.PortBindings)
	}
	port9000, _ := nat.NewPort("tcp", "9000")
	if len(host.PortBindings[port9000]) != 1 || host.PortBindings[port9000][0].HostIP != "127.0.0.1" {
		t.Errorf("127.0.0.1:9000:9000 binding: %+v", host.PortBindings)
	}
	port5000, _ := nat.NewPort("tcp", "5000")
	if _, ok := cfg.ExposedPorts[port5000]; !ok {
		t.Errorf("--expose 5000 not exposed: %+v", cfg.ExposedPorts)
	}
}

func TestApplyRunArgs_Devices(t *testing.T) {
	_, host := applyForTest(t, []string{
		"--device=/dev/foo",
		"--device", "/dev/bar:/dev/baz:rw",
		"--device-cgroup-rule=c 1:1 rwm",
	})
	if len(host.Devices) != 2 {
		t.Fatalf("devices: %+v", host.Devices)
	}
	if host.Devices[0].PathOnHost != "/dev/foo" || host.Devices[0].PathInContainer != "/dev/foo" {
		t.Errorf("device[0]: %+v", host.Devices[0])
	}
	if host.Devices[1].PathInContainer != "/dev/baz" || host.Devices[1].CgroupPermissions != "rw" {
		t.Errorf("device[1]: %+v", host.Devices[1])
	}
	if !reflect.DeepEqual(host.DeviceCgroupRules, []string{"c 1:1 rwm"}) {
		t.Errorf("device-cgroup-rules: %+v", host.DeviceCgroupRules)
	}
}

func TestApplyRunArgs_GPUsAll(t *testing.T) {
	_, host := applyForTest(t, []string{"--gpus", "all"})
	if len(host.DeviceRequests) != 1 || host.DeviceRequests[0].Count != -1 {
		t.Errorf("gpus all: %+v", host.DeviceRequests)
	}
	if !reflect.DeepEqual(host.DeviceRequests[0].Capabilities, [][]string{{"gpu"}}) {
		t.Errorf("gpus capabilities: %+v", host.DeviceRequests[0].Capabilities)
	}
}

func TestApplyRunArgs_GPUsCount(t *testing.T) {
	_, host := applyForTest(t, []string{"--gpus", "2"})
	if len(host.DeviceRequests) != 1 || host.DeviceRequests[0].Count != 2 {
		t.Errorf("gpus 2: %+v", host.DeviceRequests)
	}
}

func TestApplyRunArgs_GPUsDeviceList(t *testing.T) {
	// Inner commas need CSV quoting to stay attached to their key,
	// mirroring docker CLI semantics.
	_, host := applyForTest(t, []string{"--gpus", `"device=GPU-a,GPU-b","capabilities=gpu,utility"`})
	if len(host.DeviceRequests) != 1 {
		t.Fatalf("gpus device list: %+v", host.DeviceRequests)
	}
	req := host.DeviceRequests[0]
	if !reflect.DeepEqual(req.DeviceIDs, []string{"GPU-a", "GPU-b"}) {
		t.Errorf("device ids: %+v", req.DeviceIDs)
	}
	if !reflect.DeepEqual(req.Capabilities, [][]string{{"gpu", "utility"}}) {
		t.Errorf("capabilities: %+v", req.Capabilities)
	}
}

func TestApplyRunArgs_Sysctls(t *testing.T) {
	_, host := applyForTest(t, []string{"--sysctl", "net.ipv4.ip_forward=1"})
	if host.Sysctls["net.ipv4.ip_forward"] != "1" {
		t.Errorf("sysctl: %+v", host.Sysctls)
	}
}

func TestApplyRunArgs_Ulimits(t *testing.T) {
	_, host := applyForTest(t, []string{"--ulimit", "nofile=1024:2048"})
	if len(host.Ulimits) != 1 || host.Ulimits[0].Name != "nofile" {
		t.Errorf("ulimit: %+v", host.Ulimits)
	}
	if host.Ulimits[0].Soft != 1024 || host.Ulimits[0].Hard != 2048 {
		t.Errorf("ulimit values: %+v", host.Ulimits[0])
	}
}

func TestApplyRunArgs_Name(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{nil, ""},
		{[]string{"--name", "sample-dev", "--init"}, "sample-dev"},
		{[]string{"--name=sample-dev", "--init"}, "sample-dev"},
		{[]string{"--name=first", "--name", "second", "--init"}, "second"},
	} {
		cfg, host := &container.Config{}, &container.HostConfig{}
		name, err := applyRunArgs(tc.args, cfg, host)
		if err != nil || name != tc.want {
			t.Fatalf("applyRunArgs(%v) = %q, %v; want %q", tc.args, name, err, tc.want)
		}
		if cfg.Hostname != "" {
			t.Fatalf("name became hostname: %q", cfg.Hostname)
		}
		if len(tc.args) > 0 && (host.Init == nil || !*host.Init) {
			t.Fatal("flag after name was not parsed")
		}
	}
}

func TestApplyRunArgs_UnknownFlagErrors(t *testing.T) {
	if _, _, err := tryApply([]string{"--made-up"}); err == nil {
		t.Error("unknown flag should error")
	}
}

func TestApplyRunArgs_PositionalRejected(t *testing.T) {
	if _, _, err := tryApply([]string{"image:tag"}); err == nil {
		t.Error("positional arg should error")
	}
	if _, _, err := tryApply([]string{"--", "anything"}); err == nil {
		t.Error("-- separator should error")
	}
}

func TestApplyRunArgs_EntrypointOverridden(t *testing.T) {
	for _, args := range [][]string{{"--entrypoint", "/bin/bash"}, {"--entrypoint="}} {
		cfg := &container.Config{Entrypoint: []string{"/bin/sh"}}
		if _, err := applyRunArgs(args, cfg, &container.HostConfig{}); err != nil {
			t.Fatal(err)
		}
		if len(cfg.Entrypoint) != 1 || cfg.Entrypoint[0] != "/bin/sh" {
			t.Fatal("keepalive entrypoint replaced")
		}
	}
	if _, _, err := tryApply([]string{"--entrypoint"}); err == nil {
		t.Fatal("missing entrypoint accepted")
	}
}

func TestApplyRunArgs_MissingValue(t *testing.T) {
	if _, _, err := tryApply([]string{"--network"}); err == nil {
		t.Error("flag without value should error")
	}
}

func TestBuildContainerConfig_RunArgsMergesWithStructuredFields(t *testing.T) {
	// Upstream appends structured CapAdd after runArgs without replacement.
	dc := &spec.DevContainer{
		CapAdd:  []string{"SYS_PTRACE"},
		RunArgs: []string{"--cap-add=NET_ADMIN", "--network=host"},
	}
	cfg, host, _, err := buildContainerConfig(dc, nil, "img:latest", "/w/proj", "/h/proj", "")
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}
	want := []string{"NET_ADMIN", "SYS_PTRACE"}
	if !reflect.DeepEqual([]string(host.CapAdd), want) {
		t.Errorf("cap-add merge: %+v", host.CapAdd)
	}
	if host.NetworkMode != "host" {
		t.Errorf("network mode: %q", host.NetworkMode)
	}
	if cfg.Image != "img:latest" {
		t.Errorf("image lost: %q", cfg.Image)
	}
}

func TestBuildContainerConfig_RunArgsParseError(t *testing.T) {
	dc := &spec.DevContainer{
		RunArgs: []string{"--made-up-flag"},
	}
	_, _, _, err := buildContainerConfig(dc, nil, "img:latest", "/w/proj", "/h/proj", "")
	if err == nil {
		t.Fatal("expected error on unsupported flag")
	}
}

// tryApply runs applyRunArgs and returns the resulting cfg/host plus any
// error, so tests checking error cases don't have to plumb the boilerplate.
func tryApply(args []string) (*container.Config, *container.HostConfig, error) {
	cfg := &container.Config{}
	host := &container.HostConfig{}
	_, err := applyRunArgs(args, cfg, host)
	return cfg, host, err
}

// File parsing and precedence follow Docker CLI opts.ReadKV{Env,}Strings.
func TestApplyRunArgs_FilePrecedence(t *testing.T) {
	dir := t.TempDir()
	first, second := filepath.Join(dir, "first"), filepath.Join(dir, "second")
	t.Setenv("SAMPLE_INHERIT", "host-value")
	t.Setenv("SAMPLE_LABEL", "not-inherited")
	for path, data := range map[string]string{first: "# comment\nSAMPLE=file-first\nSAMPLE_INHERIT\nSAMPLE_LABEL\n", second: "SAMPLE=file-second\nOTHER=value=with=equals\n"} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg, _ := applyForTest(t, []string{"-eSAMPLE=explicit", "--env-file", first, "--env-file=" + second, "-lSAMPLE=explicit", "--label-file", first, "--label-file=" + second})
	want := []string{"SAMPLE=file-first", "SAMPLE_INHERIT=host-value", "SAMPLE_LABEL=not-inherited", "SAMPLE=file-second", "OTHER=value=with=equals", "SAMPLE=explicit"}
	if !reflect.DeepEqual(cfg.Env, want) {
		t.Fatalf("env got %q want %q", cfg.Env, want)
	}
	labels := map[string]string{"SAMPLE": "explicit", "OTHER": "value=with=equals"}
	if !reflect.DeepEqual(cfg.Labels, labels) {
		t.Fatalf("labels got %v want %v", cfg.Labels, labels)
	}
}

func TestApplyRunArgs_FileErrors(t *testing.T) {
	for _, flag := range []string{"--env-file", "--label-file"} {
		t.Run(flag, func(t *testing.T) {
			for _, mode := range []string{"missing-value", "missing-file", "invalid-file"} {
				t.Run(mode, func(t *testing.T) {
					args := []string{flag}
					if mode != "missing-value" {
						path := filepath.Join(t.TempDir(), "values")
						if mode == "invalid-file" {
							if err := os.WriteFile(path, []byte("=invalid\n"), 0600); err != nil {
								t.Fatal(err)
							}
						}
						args = append(args, path)
					}
					_, _, _, err := buildContainerConfig(&spec.DevContainer{Image: "alpine", RunArgs: args}, nil, "alpine", "", "", "")
					if err == nil || !strings.Contains(err.Error(), flag) {
						t.Fatalf("expected contextual %s error, got %v", flag, err)
					}
				})
			}
		})
	}
}

func TestBuildContainerConfig_FileOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "values")
	if err := os.WriteFile(path, []byte("SAMPLE=file\ndevcontainer.local_folder=file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dc := &spec.DevContainer{Image: "alpine", ContainerEnv: map[string]any{"SAMPLE": "config"}, RunArgs: []string{"--env-file", path, "--label-file", path}}
	cfg, _, _, err := buildContainerConfig(dc, nil, "alpine", "", "/workspace", "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Labels[LabelLocalFolder] != "/workspace" {
		t.Fatalf("identity label: %v", cfg.Labels)
	}
	if cfg.Env[len(cfg.Env)-1] != "SAMPLE=config" {
		t.Fatalf("explicit config env must follow files: %q", cfg.Env)
	}
}
