// relationships:
//
//	references: [projectname, project, ../../../../reference/cli/src/spec-node/dockerCompose.ts]
package dockercompose

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestProjectName(t *testing.T) {
	for _, c := range []struct {
		workspace, file, want string
	}{
		{"/tmp/Sample.Project", "/tmp/Sample.Project/.devcontainer/compose.yml", "sampleproject_devcontainer"},
		{"/tmp/123-project", "/tmp/123-project/.devcontainer/compose.yml", "123-project_devcontainer"},
		{"/tmp/sample", "/tmp/sample/Other_Config/compose.yml", "other_config"},
		{"/tmp/sample", "/tmp/Another.Project/compose.yml", "anotherproject"},
	} {
		if got := ProjectName(c.workspace, c.file); got != c.want {
			t.Errorf("ProjectName(%q, %q) = %q, want %q", c.workspace, c.file, got, c.want)
		}
	}
}

func TestToProjectName(t *testing.T) {
	if got := toProjectName("_123-My.Project! "); got != "_123-myproject" {
		t.Fatalf("toProjectName = %q", got)
	}
}

func TestLoadProjectNamePrecedence(t *testing.T) {
	for _, c := range []struct {
		name, envName, dotenv, composeName, dir, want string
	}{
		{"environment", "ENV-Project", "COMPOSE_PROJECT_NAME=dotenv-project\n", "authored-project", ".devcontainer", "env-project"},
		{"dotenv", "", "COMPOSE_PROJECT_NAME= Dot.Env_Project \n", "authored-project", ".devcontainer", "dotenv_project"},
		{"empty environment", "!?", "COMPOSE_PROJECT_NAME=dotenv-project\n", "authored-project", ".devcontainer", "dotenv-project"},
		{"custom name", "", "", "Authored-Project", ".devcontainer", "authored-project"},
		{"literal devcontainer", "", "", "devcontainer", ".devcontainer", "devcontainer"},
		{"interpolated devcontainer", "", "", "${SAMPLE_NAME}", ".devcontainer", "devcontainer"},
		{"default devcontainer", "", "", "", ".devcontainer", "sampleproject_devcontainer"},
		{"other directory", "", "", "", "Other.Config", "otherconfig"},
		{"dotenv literal first assignment", "", "COMPOSE_PROJECT_NAME=first-project\nCOMPOSE_PROJECT_NAME=second-project\n", "authored-project", ".devcontainer", "first-project"},
		{"dotenv unmatched assignment", "", " COMPOSE_PROJECT_NAME=ignored\nexport COMPOSE_PROJECT_NAME=ignored\n", "authored-project", ".devcontainer", "authored-project"},
	} {
		t.Run(c.name, func(t *testing.T) {
			workspace := filepath.Join(t.TempDir(), "Sample.Project")
			dir := filepath.Join(workspace, c.dir)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			writeNameFile(t, filepath.Join(workspace, ".env"), c.dotenv)
			// .env belongs to cliHost.cwd = workspace, not the first file's directory.
			writeNameFile(t, filepath.Join(dir, ".env"), "COMPOSE_PROJECT_NAME=wrong-directory\n")
			body := "services:\n  app:\n    image: alpine:3.20\n    environment:\n      PROJECT: ${COMPOSE_PROJECT_NAME}\n    volumes: [cache:/cache]\nvolumes:\n  cache: {}\n"
			if c.composeName != "" {
				body = "name: " + c.composeName + "\n" + body
			}
			writeNameFile(t, filepath.Join(dir, "compose.yml"), body)
			dc := &spec.DevContainer{Path: filepath.Join(dir, "devcontainer.json"), DockerComposeFile: spec.StringOrSlice{"compose.yml"}, Service: "app"}
			env := map[string]string{"COMPOSE_PROJECT_NAME": c.envName, "SAMPLE_NAME": "devcontainer"}
			original := map[string]string{"COMPOSE_PROJECT_NAME": c.envName, "SAMPLE_NAME": "devcontainer"}
			proj, err := LoadProject(context.Background(), LoadInputs{Spec: dc, Workspace: workspace, LocalEnv: env})
			if err != nil {
				t.Fatal(err)
			}
			if proj.Name != c.want {
				t.Fatalf("project name = %q, want %q", proj.Name, c.want)
			}
			if proj.Volumes["cache"].Name != c.want+"_cache" || proj.Networks["default"].Name != c.want+"_default" {
				t.Fatalf("resource names = %v, %v", proj.Volumes, proj.Networks)
			}
			if got := proj.Services["app"].Environment["PROJECT"]; got == nil || *got != c.want {
				t.Fatalf("COMPOSE_PROJECT_NAME interpolation = %v, want %q", got, c.want)
			}
			if !reflect.DeepEqual(env, original) {
				t.Fatalf("LoadProject mutated caller environment: %v", env)
			}
		})
	}
}

func TestLoadProjectNameMerge(t *testing.T) {
	workspace := t.TempDir()
	writeNameFile(t, filepath.Join(workspace, "base.yml"), "name: base-project\nservices:\n  app:\n    image: alpine:3.20\n")
	writeNameFile(t, filepath.Join(workspace, "override.yml"), "name: devcontainer\n")
	dc := &spec.DevContainer{Path: filepath.Join(workspace, "devcontainer.json"), DockerComposeFile: spec.StringOrSlice{"base.yml", "override.yml"}, Service: "app"}
	proj, err := LoadProject(context.Background(), LoadInputs{Spec: dc, Workspace: workspace})
	if err != nil || proj.Name != "devcontainer" {
		t.Fatalf("merged name = %v, %v", proj, err)
	}
}

func TestProjectNameOverrideDirectory(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, ".env"), 0o755); err != nil {
		t.Fatal(err)
	}
	if name, err := projectNameOverride(workspace, nil); err != nil || name != "" {
		t.Fatalf("directory .env = %q, %v", name, err)
	}
}

func TestProjectNameOverrideReadError(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Symlink(".env", filepath.Join(workspace, ".env")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if name, err := projectNameOverride(workspace, map[string]string{"COMPOSE_PROJECT_NAME": "env-project"}); err != nil || name != "env-project" {
		t.Fatalf("environment override should not read .env: %q, %v", name, err)
	}
	writeNameFile(t, filepath.Join(workspace, "compose.yml"), "services:\n  app:\n    image: alpine:3.20\n")
	dc := &spec.DevContainer{Path: filepath.Join(workspace, "devcontainer.json"), DockerComposeFile: spec.StringOrSlice{"compose.yml"}, Service: "app"}
	if _, err := LoadProject(context.Background(), LoadInputs{Spec: dc, Workspace: workspace}); !errors.Is(err, syscall.ELOOP) {
		t.Fatalf(".env read error = %v, want symlink loop", err)
	}
}

func writeNameFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
