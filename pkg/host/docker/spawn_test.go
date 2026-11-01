package docker

import (
	"reflect"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/mount"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestParseMountString(t *testing.T) {
	cases := []struct {
		in   string
		want *mount.Mount
	}{
		{
			"type=bind,source=/host/proj,target=/workspaces/proj",
			&mount.Mount{Type: mount.TypeBind, Source: "/host/proj", Target: "/workspaces/proj"},
		},
		{
			"type=volume,src=node-modules,dst=/workspaces/proj/node_modules",
			&mount.Mount{Type: mount.TypeVolume, Source: "node-modules", Target: "/workspaces/proj/node_modules"},
		},
		{
			"source=/x,target=/y", // type omitted -> defaults to bind
			&mount.Mount{Type: mount.TypeBind, Source: "/x", Target: "/y"},
		},
		{
			"type=bind,source=/x,target=/y,readonly", // bare "readonly" flag
			&mount.Mount{Type: mount.TypeBind, Source: "/x", Target: "/y", ReadOnly: true},
		},
	}
	for _, c := range cases {
		got := parseMountString(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("parseMountString(%q):\n got  %+v\n want %+v", c.in, got, c.want)
		}
	}
}

func TestParseMountString_NoTargetReturnsNil(t *testing.T) {
	if parseMountString("type=bind,source=/x") != nil {
		t.Error("missing target should yield nil mount")
	}
}

func TestParseMountObject_ReadonlyBool(t *testing.T) {
	got := parseMountObject(map[string]any{
		"type":     "bind",
		"source":   "/host/secrets",
		"target":   "/run/secrets",
		"readonly": true,
	})
	want := &mount.Mount{Type: mount.TypeBind, Source: "/host/secrets", Target: "/run/secrets", ReadOnly: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseMountObject readonly:\n got  %+v\n want %+v", got, want)
	}
}

func TestBuildContainerConfig_DefaultsAndLabels(t *testing.T) {
	dc := &spec.DevContainer{
		ContainerEnv:  map[string]any{"DEMO": "true"},
		ContainerUser: "vscode",
	}
	cfg, host, _, err := buildContainerConfig(dc, nil, "img:latest", "/workspaces/proj", "/host/proj", "/host/proj/.devcontainer/devcontainer.json")
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}

	if cfg.Image != "img:latest" {
		t.Errorf("image: got %q", cfg.Image)
	}
	if cfg.WorkingDir != "" {
		t.Errorf("workdir = %q, want the image's (empty)", cfg.WorkingDir)
	}
	if cfg.User != "vscode" {
		t.Errorf("user: got %q", cfg.User)
	}
	if cfg.Labels[LabelLocalFolder] != "/host/proj" {
		t.Errorf("local_folder label missing or wrong: %+v", cfg.Labels)
	}
	if cfg.Labels[LabelConfigFile] == "" {
		t.Errorf("config_file label not set: %+v", cfg.Labels)
	}

	// Default workspace bind mount should be present.
	if len(host.Mounts) != 1 || host.Mounts[0].Source != "/host/proj" || host.Mounts[0].Target != "/workspaces/proj" {
		t.Errorf("expected default workspace bind mount, got %+v", host.Mounts)
	}

	// Env should include the DEMO=true container env.
	found := false
	for _, e := range cfg.Env {
		if e == "DEMO=true" {
			found = true
		}
	}
	if !found {
		t.Errorf("containerEnv not propagated to cfg.Env: %v", cfg.Env)
	}

	// Keepalive entrypoint expected.
	if len(cfg.Entrypoint) != 1 || cfg.Entrypoint[0] != "/bin/sh" {
		t.Errorf("entrypoint: got %v", cfg.Entrypoint)
	}
}

func TestBuildContainerConfig_OverrideCommandFalsePreservesImageCommand(t *testing.T) {
	override := false
	dc := &spec.DevContainer{OverrideCommand: &override}
	cfg, _, _, err := buildContainerConfig(dc, nil, "img:latest", "/workspaces/proj", "/host/proj", "")
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}
	if len(cfg.Entrypoint) != 0 || len(cfg.Cmd) != 0 || cfg.StopSignal != "" {
		t.Fatalf("overrideCommand=false should leave image entrypoint/cmd alone, got entrypoint=%v cmd=%v stop=%q", cfg.Entrypoint, cfg.Cmd, cfg.StopSignal)
	}
}

func TestSubstituteSpec_AppliesToCommands(t *testing.T) {
	dc := &spec.DevContainer{
		WorkspaceFolder: "/workspaces/${localWorkspaceFolderBasename}",
		PostCreate:      spec.Command{Shell: "echo ${localEnv:GREETING}"},
	}
	out := substituteSpec(dc, substituteCtxForTest())
	if out.WorkspaceFolder != "/workspaces/proj" {
		t.Errorf("workspace folder substitution: %q", out.WorkspaceFolder)
	}
	if out.PostCreate.Shell != "echo hello" {
		t.Errorf("postCreate substitution: %q", out.PostCreate.Shell)
	}
	// Original spec must not be mutated.
	if dc.WorkspaceFolder != "/workspaces/${localWorkspaceFolderBasename}" {
		t.Error("substituteSpec mutated the input")
	}
}

func TestBuildContainerConfig_FeatureEntrypointsRunBeforeKeepalive(t *testing.T) {
	resolved := []features.Resolved{
		{Metadata: features.Metadata{ID: "alpha", Entrypoint: "/usr/local/share/alpha-init.sh"}},
		{Metadata: features.Metadata{ID: "beta"}},
		{Metadata: features.Metadata{ID: "gamma", Entrypoint: "/usr/local/share/gamma-init.sh"}},
	}
	cfg, _, _, err := buildContainerConfig(&spec.DevContainer{}, resolved, "img:latest", "/workspaces/proj", "/host/proj", "")
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}
	want := strings.Join([]string{
		"echo Container started",
		`trap "exit 0" 15`,
		"/usr/local/share/alpha-init.sh",
		"/usr/local/share/gamma-init.sh",
		`exec "$@"`,
		`while sleep 1 & wait $!; do :; done`,
	}, "\n")
	if len(cfg.Cmd) != 3 || cfg.Cmd[0] != "-c" || cfg.Cmd[1] != want || cfg.Cmd[2] != "-" {
		t.Fatalf("cmd = %q, want [-c %q -]", cfg.Cmd, want)
	}
}

func TestBuildContainerConfig_FeatureEntrypointsWrapImageCommand(t *testing.T) {
	override := false
	resolved := []features.Resolved{{Metadata: features.Metadata{ID: "alpha", Entrypoint: "/usr/local/share/alpha-init.sh"}}}
	cfg, _, _, err := buildContainerConfig(&spec.DevContainer{OverrideCommand: &override}, resolved, "img:latest", "/workspaces/proj", "/host/proj", "")
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}
	if len(cfg.Entrypoint) != 1 || cfg.Entrypoint[0] != "/bin/sh" {
		t.Fatalf("entrypoint = %v, want /bin/sh wrapper", cfg.Entrypoint)
	}
	appendImageCommand(cfg, []string{"/docker-entrypoint.sh"}, []string{"serve", "--port", "80"})
	if got := cfg.Cmd[2:]; strings.Join(got, " ") != "- /docker-entrypoint.sh serve --port 80" {
		t.Fatalf("args after script = %q", got)
	}
	if !strings.Contains(cfg.Cmd[1], "/usr/local/share/alpha-init.sh\nexec \"$@\"") {
		t.Fatalf("script = %q", cfg.Cmd[1])
	}
}

func TestBuildContainerConfig_SpecContainerEnvWinsOverFeature(t *testing.T) {
	dc := &spec.DevContainer{ContainerEnv: map[string]any{"SHARED": "spec"}}
	resolved := []features.Resolved{{Metadata: features.Metadata{
		ContainerEnv: map[string]string{"SHARED": "feature", "FEATURE_ONLY": "yes"},
	}}}
	cfg, _, _, err := buildContainerConfig(dc, resolved, "img:latest", "/workspaces/proj", "/host/proj", "")
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}
	env := strings.Join(cfg.Env, "\n")
	if !strings.Contains(env, "SHARED=spec") || strings.Contains(env, "SHARED=feature") || !strings.Contains(env, "FEATURE_ONLY=yes") {
		t.Fatalf("env = %v", cfg.Env)
	}
}
