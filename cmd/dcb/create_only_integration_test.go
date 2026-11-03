//go:build docker_integration

// relationships:
//
//	references: [../../pkg/host/docker/up, ../../pkg/host/dockercompose/up]
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	hostdocker "github.com/wyrd-company/devcontainer-builder/pkg/host/docker"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/dockercompose"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// Observe the real Engine state and image entrypoint across create, repeat, and up.
func TestCreateOnlyCLI(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	cli := hostRequireDocker(t, ctx)
	defer cli.Close()
	hostEnsureImage(t, ctx, cli, "alpine:3.20")
	bin := hostBuildBinary(t)
	for _, mode := range []string{"image", "compose"} {
		t.Run(mode, func(t *testing.T) {
			workspace := t.TempDir()
			config := filepath.Join(workspace, "devcontainer.json")
			dc := map[string]any{"workspaceFolder": "/workspace", "initializeCommand": "echo initialize >> initialize.log", "postCreateCommand": "echo lifecycle >> /workspace/lifecycle.log", "userEnvProbe": "none"}
			if mode == "image" {
				// Exercise the build path and an image-owned startup command.
				writeCreateFile(t, filepath.Join(workspace, "Dockerfile"), "FROM alpine:3.20\nENTRYPOINT [\"sh\",\"-c\",\"echo init >> /workspace/init.log; exec sleep infinity\"]\n")
				dc["build"] = map[string]any{"dockerfile": "Dockerfile"}
				dc["overrideCommand"] = false
				dc["workspaceMount"] = "type=bind,source=" + workspace + ",target=/workspace"
				dc["runArgs"] = []string{"--name", "sample-create-" + filepath.Base(workspace), "--label", "sample.setting=retained", "--env", "SAMPLE_SETTING=retained"}
			} else {
				dc["dockerComposeFile"] = "compose.yml"
				dc["service"] = "primary"
				writeCreateFile(t, filepath.Join(workspace, "compose.yml"), "services:\n  primary:\n    image: alpine:3.20\n    entrypoint: [sh, -c, 'echo init >> /workspace/init.log; exec sleep infinity']\n    volumes:\n      - type: bind\n        source: .\n        target: /workspace\n        bind:\n          create_host_path: false\n    depends_on:\n      helper:\n        condition: service_healthy\n  helper:\n    image: alpine:3.20\n    command: [sleep, infinity]\n    healthcheck:\n      test: [CMD, 'true']\n      interval: 1s\n")
			}
			raw, _ := json.Marshal(dc)
			writeCreateFile(t, config, string(raw))
			t.Cleanup(func() { _ = hostRunCommandAllowError(bin, "down", "--workspace-folder", workspace, "--config", config) })
			args := []string{"up", "--workspace-folder", workspace, "--config", config, "--mount-workspace-git-root=false", "--update-remote-user-uid-default", "off"}
			var first string
			for range 2 {
				out := hostRunCommand(t, os.Environ(), bin, append(args, "--no-start")...)
				var result map[string]any
				if err := json.Unmarshal(out, &result); err != nil {
					t.Fatal(err)
				}
				id, _ := result["containerId"].(string)
				if result["outcome"] != "success" || result["started"] != false || id == "" {
					t.Fatalf("result=%s", out)
				}
				if first != "" && first != id {
					t.Fatalf("recreated %s as %s", first, id)
				}
				first = id
				info, err := cli.ContainerInspect(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if info.State.Status != "created" || info.State.Running {
					t.Fatalf("state=%+v", info.State)
				}
				if mode == "image" && (info.Config.Labels["sample.setting"] != "retained" || !strings.Contains(strings.Join(info.Config.Env, "\n"), "SAMPLE_SETTING=retained")) {
					t.Fatalf("creation settings lost: %+v", info.Config)
				}
				for _, name := range []string{"init.log", "lifecycle.log"} {
					if _, err := os.Stat(filepath.Join(workspace, name)); !os.IsNotExist(err) {
						t.Fatalf("%s ran before start: %v", name, err)
					}
				}
			}
			out := hostRunCommand(t, os.Environ(), bin, args...)
			var result map[string]any
			if err := json.Unmarshal(out, &result); err != nil {
				t.Fatal(err)
			}
			if result["containerId"] != first {
				t.Fatalf("later up recreated: %s", out)
			}
			for _, name := range []string{"init.log", "lifecycle.log"} {
				data, err := os.ReadFile(filepath.Join(workspace, name))
				if err != nil || len(strings.Fields(string(data))) != 1 {
					t.Fatalf("%s=%q err=%v", name, data, err)
				}
			}
			data, err := os.ReadFile(filepath.Join(workspace, "initialize.log"))
			if err != nil || len(strings.Fields(string(data))) != 3 {
				t.Fatalf("initialize per invocation=%q err=%v", data, err)
			}
		})
	}
}

func TestCreateOnlyLibrary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cli := hostRequireDocker(t, ctx)
	defer cli.Close()
	hostEnsureImage(t, ctx, cli, "alpine:3.20")
	for _, mode := range []string{"image", "compose"} {
		t.Run(mode, func(t *testing.T) {
			workspace := t.TempDir()
			config := filepath.Join(workspace, "devcontainer.json")
			content := `{"image":"alpine:3.20","workspaceFolder":"/workspace","postCreateCommand":"true","userEnvProbe":"none"}`
			if mode == "compose" {
				content = `{"dockerComposeFile":"compose.yml","service":"primary","workspaceFolder":"/","postCreateCommand":"true","userEnvProbe":"none"}`
				writeCreateFile(t, filepath.Join(workspace, "compose.yml"), "services:\n  primary:\n    image: alpine:3.20\n    command: [sleep, infinity]\n")
			}
			writeCreateFile(t, config, content)
			dc, err := spec.Load(config)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "image" {
				host := hostdocker.New(cli, hostdocker.Options{})
				defer host.Remove(ctx, workspace)
				req := hostdocker.UpRequest{Spec: dc, SpecPath: config, LocalWorkspace: workspace, Image: dc.Image, NoStart: true}
				first, err := host.Up(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				repeat, err := host.Up(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				if first.ContainerID != repeat.ContainerID || !first.CreatedThisCall || repeat.CreatedThisCall || first.StartedThisCall || repeat.StartedThisCall || len(first.PhasesRun) != 0 {
					t.Fatalf("first=%+v repeat=%+v", first, repeat)
				}
				req.NoStart = false
				up, err := host.Up(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				if up.ContainerID != first.ContainerID || !up.StartedThisCall || up.CreatedThisCall || len(up.PhasesRun) == 0 {
					t.Fatalf("up=%+v", up)
				}
			} else {
				host := dockercompose.New(cli, dockercompose.Options{})
				defer host.Remove(ctx, workspace)
				req := dockercompose.UpRequest{Spec: dc, SpecPath: config, LocalWorkspace: workspace, NoStart: true}
				first, err := host.Up(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				repeat, err := host.Up(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				if first.PrimaryContainerID != repeat.PrimaryContainerID || len(first.CreatedThisCall) != 1 || len(repeat.CreatedThisCall) != 0 || len(first.StartedThisCall) != 0 || len(repeat.StartedThisCall) != 0 || len(first.PhasesRun) != 0 {
					t.Fatalf("first=%+v repeat=%+v", first, repeat)
				}
				req.NoStart = false
				up, err := host.Up(ctx, req)
				if err != nil {
					t.Fatal(err)
				}
				if up.PrimaryContainerID != first.PrimaryContainerID || len(up.StartedThisCall) != 1 || len(up.CreatedThisCall) != 0 || len(up.PhasesRun) == 0 {
					t.Fatalf("up=%+v", up)
				}
			}
		})
	}
}

func writeCreateFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestCreateOnlyMissingBindCLI(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cli := hostRequireDocker(t, ctx)
	defer cli.Close()
	hostEnsureImage(t, ctx, cli, "alpine:3.20")
	bin := hostBuildBinary(t)
	for _, mode := range []string{"image", "compose"} {
		t.Run(mode, func(t *testing.T) {
			workspace := t.TempDir()
			missing := filepath.Join(workspace, "missing")
			dc := map[string]any{"image": "alpine:3.20", "mounts": []string{"type=bind,source=" + missing + ",target=/sample"}}
			if mode == "compose" {
				delete(dc, "image")
				dc["dockerComposeFile"] = "compose.yml"
				dc["service"] = "primary"
				writeCreateFile(t, filepath.Join(workspace, "compose.yml"), "services:\n  primary:\n    image: alpine:3.20\n    command: [sleep, infinity]\n")
			}
			config := filepath.Join(workspace, "devcontainer.json")
			data, _ := json.Marshal(dc)
			writeCreateFile(t, config, string(data))
			t.Cleanup(func() { _ = hostRunCommandAllowError(bin, "down", "--workspace-folder", workspace, "--config", config) })
			err := hostRunCommandAllowError(bin, "up", "--no-start", "--workspace-folder", workspace, "--config", config, "--mount-workspace-git-root=false")
			if err == nil || !strings.Contains(err.Error(), "bind source path does not exist") {
				t.Fatalf("missing bind error=%v", err)
			}
			if _, err := os.Stat(missing); !os.IsNotExist(err) {
				t.Fatalf("missing bind was created: %v", err)
			}
		})
	}
}
