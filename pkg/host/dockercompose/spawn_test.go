package dockercompose

import (
	"reflect"
	"testing"

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
	if len(tr.HostConfig.Mounts) != 2 {
		t.Fatalf("mounts: got %d want 2", len(tr.HostConfig.Mounts))
	}
	want := map[string]mount.Mount{
		"/var/data":     {Type: mount.TypeVolume, Source: "proj_abc_data", Target: "/var/data"},
		"/in/container": {Type: mount.TypeBind, Source: "/host/dir", Target: "/in/container"},
	}
	for _, m := range tr.HostConfig.Mounts {
		w := want[m.Target]
		if w.Source != m.Source || w.Type != m.Type {
			t.Errorf("mount %s: got %+v want %+v", m.Target, m, w)
		}
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

func TestSortedEnv_DropsNil(t *testing.T) {
	val := "v"
	env := types.MappingWithEquals{"A": &val, "B": nil, "C": &val}
	got := sortedEnv(env)
	want := []string{"A=v", "C=v"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sortedEnv: got %v want %v", got, want)
	}
}
