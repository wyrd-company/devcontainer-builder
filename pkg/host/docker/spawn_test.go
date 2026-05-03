package docker

import (
	"reflect"
	"testing"

	"github.com/docker/docker/api/types/mount"

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
	cfg, host, err := buildContainerConfig(dc, nil, "img:latest", "/workspaces/proj", "/host/proj", "/host/proj/.devcontainer/devcontainer.json")
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}

	if cfg.Image != "img:latest" {
		t.Errorf("image: got %q", cfg.Image)
	}
	if cfg.WorkingDir != "/workspaces/proj" {
		t.Errorf("workdir: got %q", cfg.WorkingDir)
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
	cfg, _, err := buildContainerConfig(dc, nil, "img:latest", "/workspaces/proj", "/host/proj", "")
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
