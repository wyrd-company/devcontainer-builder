package dockercompose

import (
	"reflect"
	"testing"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/mount"
)

func TestTranslateService_NetworksGetAliases(t *testing.T) {
	proj := &types.Project{Name: "proj_abc"}
	svc := types.ServiceConfig{
		Name:  "app",
		Image: "alpine",
		Networks: map[string]*types.ServiceNetworkConfig{
			"default": {Aliases: []string{"app-alias"}},
		},
	}
	netNames := map[string]string{"default": "proj_abc_default"}

	tr, err := translateService(proj, svc, "proj_abc-app-1", nil, netNames, nil, nil)
	if err != nil {
		t.Fatalf("translateService: %v", err)
	}
	ep, ok := tr.NetConfig.EndpointsConfig["proj_abc_default"]
	if !ok {
		t.Fatalf("endpoint missing for proj_abc_default; got %#v", tr.NetConfig.EndpointsConfig)
	}
	wantAliases := []string{"app", "app-alias"}
	if !reflect.DeepEqual(ep.Aliases, wantAliases) {
		t.Errorf("aliases: got %v want %v", ep.Aliases, wantAliases)
	}
}

func TestTranslateService_NamedVolumeRenamed(t *testing.T) {
	proj := &types.Project{Name: "proj_abc"}
	svc := types.ServiceConfig{
		Name:  "app",
		Image: "alpine",
		Volumes: []types.ServiceVolumeConfig{
			{Type: types.VolumeTypeVolume, Source: "data", Target: "/var/data"},
			{Type: types.VolumeTypeBind, Source: "/host/dir", Target: "/in/container"},
		},
	}
	volNames := map[string]string{"data": "proj_abc_data"}

	tr, err := translateService(proj, svc, "proj_abc-app-1", nil, nil, volNames, nil)
	if err != nil {
		t.Fatalf("translateService: %v", err)
	}
	// Like docker compose, plain entries use the Bind API (Mode "rw").
	if want := []string{"proj_abc_data:/var/data:rw", "/host/dir:/in/container:rw"}; !reflect.DeepEqual(tr.HostConfig.Binds, want) || len(tr.HostConfig.Mounts) != 0 {
		t.Fatalf("binds=%v mounts=%+v, want binds %v", tr.HostConfig.Binds, tr.HostConfig.Mounts, want)
	}
}

// Mirrors docker compose's bindRequiresMountAPI and volumeRequiresMountAPI.
func TestTranslateVolumesMountAPIOptions(t *testing.T) {
	names := map[string]string{"data": "proj_data"}
	for _, tc := range []struct {
		name string
		vol  types.ServiceVolumeConfig
		bind string
	}{
		{"bind read-only", types.ServiceVolumeConfig{Type: types.VolumeTypeBind, Source: "/host", Target: "/in", ReadOnly: true}, "/host:/in:ro"},
		{"bind selinux", types.ServiceVolumeConfig{Type: types.VolumeTypeBind, Source: "/host", Target: "/in", Bind: &types.ServiceVolumeBind{CreateHostPath: true, SELinux: "z"}}, "/host:/in:rw,z"},
		{"bind no host path creation", types.ServiceVolumeConfig{Type: types.VolumeTypeBind, Source: "/host", Target: "/in", Bind: &types.ServiceVolumeBind{CreateHostPath: false}}, ""},
		{"bind propagation", types.ServiceVolumeConfig{Type: types.VolumeTypeBind, Source: "/host", Target: "/in", Bind: &types.ServiceVolumeBind{CreateHostPath: true, Propagation: "rshared"}}, ""},
		{"bind recursive", types.ServiceVolumeConfig{Type: types.VolumeTypeBind, Source: "/host", Target: "/in", Bind: &types.ServiceVolumeBind{CreateHostPath: true, Recursive: "readonly"}}, ""},
		{"declared volume", types.ServiceVolumeConfig{Type: types.VolumeTypeVolume, Source: "data", Target: "/in", Volume: &types.ServiceVolumeVolume{}}, "proj_data:/in:rw"},
		{"volume nocopy", types.ServiceVolumeConfig{Type: types.VolumeTypeVolume, Source: "data", Target: "/in", Volume: &types.ServiceVolumeVolume{NoCopy: true}}, ""},
		{"volume subpath", types.ServiceVolumeConfig{Type: types.VolumeTypeVolume, Source: "data", Target: "/in", Volume: &types.ServiceVolumeVolume{Subpath: "sub"}}, ""},
		{"volume labels", types.ServiceVolumeConfig{Type: types.VolumeTypeVolume, Source: "data", Target: "/in", Volume: &types.ServiceVolumeVolume{Labels: types.Mapping{"k": "v"}}}, ""},
		{"anonymous volume", types.ServiceVolumeConfig{Type: types.VolumeTypeVolume, Target: "/in"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binds, mounts, err := translateVolumes([]types.ServiceVolumeConfig{tc.vol}, names)
			if err != nil {
				t.Fatal(err)
			}
			if tc.bind != "" && (!reflect.DeepEqual(binds, []string{tc.bind}) || len(mounts) != 0) {
				t.Fatalf("binds=%v mounts=%+v, want %s", binds, mounts, tc.bind)
			}
			if tc.bind == "" && (len(binds) != 0 || len(mounts) != 1 || mounts[0].Target != "/in") {
				t.Fatalf("binds=%v mounts=%+v, want Mount API", binds, mounts)
			}
		})
	}
	_, mounts, _ := translateVolumes([]types.ServiceVolumeConfig{{Type: types.VolumeTypeBind, Source: "/host", Target: "/in", Bind: &types.ServiceVolumeBind{CreateHostPath: true, Propagation: "rshared"}}}, nil)
	if want := (mount.Mount{Type: mount.TypeBind, Source: "/host", Target: "/in", BindOptions: &mount.BindOptions{Propagation: "rshared"}}); !reflect.DeepEqual(mounts, []mount.Mount{want}) {
		t.Fatalf("propagation mount=%+v", mounts)
	}
}

func TestTranslateService_RejectsUnsupportedVolumeType(t *testing.T) {
	proj := &types.Project{Name: "proj_abc"}
	svc := types.ServiceConfig{
		Name:  "app",
		Image: "alpine",
		Volumes: []types.ServiceVolumeConfig{
			{Type: "tmpfs", Target: "/tmp/x"},
		},
	}
	_, err := translateService(proj, svc, "proj_abc-app-1", nil, nil, nil, nil)
	if err == nil {
		t.Fatal("translateService: expected error for tmpfs volume type")
	}
}

func TestTranslateService_PortsExposeAndPublish(t *testing.T) {
	proj := &types.Project{Name: "proj_abc"}
	svc := types.ServiceConfig{
		Name:  "app",
		Image: "alpine",
		Ports: []types.ServicePortConfig{
			{Target: 80, Published: "8080", Protocol: "tcp"},
			{Target: 9000, Protocol: "tcp"}, // expose-only
		},
	}
	tr, err := translateService(proj, svc, "proj_abc-app-1", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("translateService: %v", err)
	}
	if len(tr.Config.ExposedPorts) != 2 {
		t.Errorf("exposed ports: got %d want 2", len(tr.Config.ExposedPorts))
	}
	if len(tr.HostConfig.PortBindings) != 1 {
		t.Errorf("port bindings: got %d want 1 (only the published one)", len(tr.HostConfig.PortBindings))
	}
}

func TestTranslateService_ConfiguresHealthcheck(t *testing.T) {
	interval := types.Duration(200 * time.Millisecond)
	timeout := types.Duration(time.Second)
	retries := uint64(7)
	svc := types.ServiceConfig{
		Name:  "db",
		Image: "busybox",
		HealthCheck: &types.HealthCheckConfig{
			Test:     types.HealthCheckTest{"CMD-SHELL", "test -f /tmp/ready"},
			Interval: &interval,
			Timeout:  &timeout,
			Retries:  &retries,
		},
	}
	got, err := translateService(&types.Project{Name: "proj"}, svc, "proj-db-1", nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("translateService: %v", err)
	}
	if got.Config.Healthcheck == nil {
		t.Fatal("healthcheck was not translated")
	}
	if got.Config.Healthcheck.Test[0] != "CMD-SHELL" || got.Config.Healthcheck.Test[1] != "test -f /tmp/ready" {
		t.Fatalf("healthcheck test = %v", got.Config.Healthcheck.Test)
	}
	if got.Config.Healthcheck.Interval != 200*time.Millisecond {
		t.Fatalf("interval = %s, want 200ms", got.Config.Healthcheck.Interval)
	}
	if got.Config.Healthcheck.Timeout != time.Second {
		t.Fatalf("timeout = %s, want 1s", got.Config.Healthcheck.Timeout)
	}
	if got.Config.Healthcheck.Retries != 7 {
		t.Fatalf("retries = %d, want 7", got.Config.Healthcheck.Retries)
	}
}

func TestSortedEnv_DropsNil(t *testing.T) {
	val := "v"
	env := types.MappingWithEquals{"A": &val, "B": nil, "C": &val}
	got := sortedEnv(env)
	want := []string{"A=v", "C=v"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sortedEnv: got %v want %v", got, want)
	}
}
