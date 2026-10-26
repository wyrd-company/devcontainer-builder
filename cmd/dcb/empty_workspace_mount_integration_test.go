//go:build docker_integration

// relationships:
//   references: [docker_host_integration_test, ../../pkg/host/docker/spawn]

package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
)

func TestEmptyWorkspaceMountCLI(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cli := hostRequireDocker(t, ctx)
	t.Cleanup(func() { _ = cli.Close() })
	hostEnsureImage(t, ctx, cli, "alpine:3.20")
	bin := hostBuildBinary(t)
	workspace := t.TempDir()
	config := `{"image":"alpine:3.20","workspaceFolder":"/tmp","workspaceMount":"","userEnvProbe":"none","updateRemoteUserUID":false,"mounts":["type=tmpfs,target=/sample-data"]}`
	if err := os.WriteFile(filepath.Join(workspace, ".devcontainer.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	// The local workspace need not exist on the daemon host.
	result := hostRunCommand(t, nil, bin, "read-configuration", "--workspace-folder", workspace)
	var read struct {
		Workspace map[string]any `json:"workspace"`
	}
	if err := json.Unmarshal(result, &read); err != nil {
		t.Fatal(err)
	}
	if value, ok := read.Workspace["workspaceMount"]; !ok || value != "" {
		t.Fatalf("workspace = %v", read.Workspace)
	}
	result = hostRunCommand(t, nil, bin, "up", "--workspace-folder", workspace, "--mount", "type=bind,source=/tmp,target=/sample-cache", "--skip-post-create")
	var up struct {
		ContainerID string `json:"containerId"`
	}
	if err := json.Unmarshal(result, &up); err != nil || up.ContainerID == "" {
		t.Fatalf("up = %s, err = %v", result, err)
	}
	info, err := cli.ContainerInspect(ctx, up.ContainerID)
	if err != nil {
		t.Fatal(err)
	}
	if info.Config.Labels["devcontainer.local_folder"] != workspace {
		t.Fatal("container ownership label mismatch")
	}
	t.Cleanup(func() {
		_ = cli.ContainerRemove(context.Background(), up.ContainerID, container.RemoveOptions{Force: true, RemoveVolumes: true})
	})
	seen := map[string]bool{}
	for _, m := range info.Mounts {
		seen[m.Destination] = true
		if m.Source == workspace || m.Destination == "/tmp" || m.Destination == "/workspaces/"+filepath.Base(workspace) {
			t.Fatalf("unexpected workspace bind: %+v", m)
		}
	}
	if !seen["/sample-data"] || !seen["/sample-cache"] || len(info.Mounts) != 2 {
		t.Fatalf("mounts = %+v", info.Mounts)
	}
}
