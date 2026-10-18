package dockercompose

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestLoadProject_MergesAndAppliesOverride(t *testing.T) {
	dir := t.TempDir()
	composePath := filepath.Join(dir, "docker-compose.yml")
	composeBody := `services:
  app:
    image: alpine:3.20
  sidecar:
    image: busybox:1.36
`
	if err := os.WriteFile(composePath, []byte(composeBody), 0o644); err != nil {
		t.Fatal(err)
	}

	specPath := filepath.Join(dir, "devcontainer.json")
	specBody := `{"dockerComposeFile": "docker-compose.yml", "service": "app"}`
	if err := os.WriteFile(specPath, []byte(specBody), 0o644); err != nil {
		t.Fatal(err)
	}
	dc, err := spec.Load(specPath)
	if err != nil {
		t.Fatalf("spec.Load: %v", err)
	}

	override, err := OverrideYAML(OverrideInputs{
		Spec:               dc,
		LocalWorkspace:     dir,
		ContainerWorkspace: "/workspaces/proj",
		PrimaryImage:       "registry/derived:tag",
		RemoteEnv:          map[string]string{"FOO": "bar"},
		OverrideCommand:    true,
	})
	if err != nil {
		t.Fatalf("OverrideYAML: %v", err)
	}

	proj, err := LoadProject(context.Background(), LoadInputs{
		Spec:      dc,
		Workspace: dir,
		Override:  override,
	})
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}

	app, err := proj.GetService("app")
	if err != nil {
		t.Fatalf("GetService(app): %v", err)
	}
	if app.Image != "registry/derived:tag" {
		t.Errorf("primary image: got %q want %q", app.Image, "registry/derived:tag")
	}
	// Upstream's override leaves working_dir to the Compose file and image.
	if app.WorkingDir != "" {
		t.Errorf("working_dir: got %q want the service's own (unset)", app.WorkingDir)
	}
	if v, ok := app.Environment["FOO"]; !ok || v == nil || *v != "bar" {
		t.Errorf("env FOO: got %v want bar", app.Environment["FOO"])
	}
	gotMount := false
	for _, vol := range app.Volumes {
		if vol.Type == "bind" && vol.Source == dir && vol.Target == "/workspaces/proj" {
			gotMount = true
		}
	}
	if !gotMount {
		t.Errorf("workspace bind mount not present in primary service: %#v", app.Volumes)
	}
	if len(app.Command) == 0 {
		t.Errorf("override command not applied; got empty command")
	}

	// Sidecar is untouched.
	side, err := proj.GetService("sidecar")
	if err != nil {
		t.Fatalf("GetService(sidecar): %v", err)
	}
	if side.Image != "busybox:1.36" {
		t.Errorf("sidecar image changed: got %q want busybox:1.36", side.Image)
	}
}

func TestLoadProject_PrimaryNotFound(t *testing.T) {
	dir := t.TempDir()
	composePath := filepath.Join(dir, "docker-compose.yml")
	if err := os.WriteFile(composePath, []byte("services:\n  other:\n    image: alpine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(dir, "devcontainer.json")
	if err := os.WriteFile(specPath, []byte(`{"dockerComposeFile": "docker-compose.yml", "service": "missing"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	dc, err := spec.Load(specPath)
	if err != nil {
		t.Fatalf("spec.Load: %v", err)
	}
	_, err = LoadProject(context.Background(), LoadInputs{Spec: dc, Workspace: dir})
	if err == nil {
		t.Fatal("LoadProject: expected error, got nil")
	}
	if !errors.Is(err, ErrPrimaryNotFound) {
		t.Errorf("LoadProject: error = %v, want wrap of ErrPrimaryNotFound", err)
	}
}

func TestLoadProject_RejectsSingleMode(t *testing.T) {
	dc := &spec.DevContainer{Image: "alpine"}
	_, err := LoadProject(context.Background(), LoadInputs{Spec: dc})
	if err == nil {
		t.Fatal("LoadProject: expected error for non-compose spec")
	}
	if !strings.Contains(err.Error(), "not in compose mode") {
		t.Errorf("LoadProject error = %q, want substring %q", err.Error(), "not in compose mode")
	}
}
