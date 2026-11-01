//go:build docker_integration

// relationships:
//
//	references: [docker_host_integration_test, ../../pkg/spec/mounts]
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDockerObjectMountsCLI(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cli := hostRequireDocker(t, ctx)
	defer cli.Close()
	image := "alpine:3.20"
	hostEnsureImage(t, ctx, cli, image)
	bin := hostBuildBinary(t)
	workspace := t.TempDir()
	volume := fmt.Sprintf("sample-mount-%d", time.Now().UnixNano())
	config := map[string]any{
		"image": image, "workspaceMount": "type=volume,source=" + volume + ",target=/workspace", "workspaceFolder": "/workspace",
		"mounts": []any{"type=bind,source=/tmp,target=/sample-data,readonly", map[string]any{"type": "volume", "target": "/object-data"}},
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".devcontainer.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = hostRunCommandAllowError(bin, "down", "--workspace-folder", workspace)
		_ = cli.VolumeRemove(context.Background(), volume, true)
	})
	result := hostRunCommand(t, os.Environ(), bin, "up", "--workspace-folder", workspace, "--skip-post-create", "--update-remote-user-uid-default", "off")
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
	foundBind, foundObject := false, false
	for _, m := range info.Mounts {
		switch m.Destination {
		case "/sample-data":
			foundBind = true
			if m.RW {
				t.Fatal("readonly string mount is writable")
			}
		case "/object-data":
			foundObject = true
			if !m.RW {
				t.Fatal("valid object mount is not writable")
			}
		}
	}
	if !foundBind || !foundObject {
		t.Fatalf("mounts=%+v", info.Mounts)
	}
	hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", workspace, "--", "sh", "-c", `touch /object-data/sample && if touch /sample-data/sample-mount-write-probe; then exit 1; fi`)
}
