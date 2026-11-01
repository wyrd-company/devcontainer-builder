//go:build docker_integration

// relationships:
//
//	references: [docker_host_integration_test, ../../pkg/host/docker/up]
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
)

func TestDockerContainerNameCLI(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cli := hostRequireDocker(t, ctx)
	defer cli.Close()
	image := "alpine:3.20"
	hostEnsureImage(t, ctx, cli, image)
	bin := hostBuildBinary(t)
	for _, form := range []string{"separate", "equals"} {
		t.Run(form, func(t *testing.T) {
			workspace := t.TempDir()
			name := fmt.Sprintf("sample-name-%d-%s", time.Now().UnixNano(), form)
			args := []string{"--name", name}
			if form == "equals" {
				args = []string{"--name=" + name}
			}
			config := map[string]any{"image": image, "workspaceMount": "type=volume,source=" + name + ",target=/workspace", "workspaceFolder": "/workspace", "runArgs": args}
			data, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(workspace, ".devcontainer.json"), data, 0600); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				_ = hostRunCommandAllowError(bin, "down", "--workspace-folder", workspace)
				_ = cli.ContainerRemove(context.Background(), name, container.RemoveOptions{Force: true})
				_ = cli.VolumeRemove(context.Background(), name, true)
			})
			up := func(extra ...string) string {
				result := hostRunCommand(t, os.Environ(), bin, append([]string{"up", "--workspace-folder", workspace, "--skip-post-create", "--update-remote-user-uid-default", "off"}, extra...)...)
				var response struct {
					ContainerID string `json:"containerId"`
				}
				if err := json.Unmarshal(result, &response); err != nil || response.ContainerID == "" {
					t.Fatalf("up=%s, %v", result, err)
				}
				info, err := cli.ContainerInspect(ctx, response.ContainerID)
				if err != nil {
					t.Fatal(err)
				}
				if info.Name != "/"+name {
					t.Fatalf("name=%q, want /%s", info.Name, name)
				}
				if info.Config.Labels["devcontainer.local_folder"] != workspace {
					t.Fatalf("labels=%v", info.Config.Labels)
				}
				return response.ContainerID
			}
			first := up()
			if second := up(); second != first {
				t.Fatalf("reuse=%s, want %s", second, first)
			}
			replaced := up("--remove-existing-container")
			if replaced == first {
				t.Fatal("explicit recreation reused container")
			}
			if _, err := cli.ContainerInspect(ctx, first); err == nil {
				t.Fatal("old container still exists")
			}
			if err := cli.ContainerRemove(ctx, replaced, container.RemoveOptions{Force: true}); err != nil {
				t.Fatal(err)
			}
			unrelated, err := cli.ContainerCreate(ctx, &container.Config{Image: image, Cmd: []string{"true"}}, nil, nil, nil, name)
			if err != nil {
				t.Fatal(err)
			}
			defer cli.ContainerRemove(context.Background(), unrelated.ID, container.RemoveOptions{Force: true})
			output, err := exec.CommandContext(ctx, bin, "up", "--workspace-folder", workspace, "--skip-post-create").CombinedOutput()
			var failure struct {
				Message string `json:"message"`
			}
			decodeErr := json.Unmarshal(output, &failure)
			if err == nil || decodeErr != nil || !strings.Contains(failure.Message, fmt.Sprintf("container name %q is already in use", name)) {
				t.Fatalf("conflict output=%s, error=%v", output, err)
			}
			if _, err := cli.ContainerInspect(ctx, unrelated.ID); err != nil {
				t.Fatalf("unrelated container removed: %v", err)
			}
		})
	}
}
