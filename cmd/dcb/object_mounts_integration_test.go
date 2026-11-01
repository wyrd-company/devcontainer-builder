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
		"mounts": []any{
			"type=bind,source=/tmp,target=/string-readonly,readonly",
			map[string]any{"type": "bind", "source": "/tmp", "target": "/object-readonly,readonly"},
			map[string]any{"type": "bind", "source": "/tmp", "target": "/object-ro,ro"},
			map[string]any{"type": "bind", "source": "/tmp", "target": "/object-bind"},
			map[string]any{"type": "volume", "target": "/object-data"},
		},
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
	wants := map[string]bool{"/string-readonly": false, "/object-readonly": false, "/object-ro": false, "/object-bind": true, "/object-data": true}
	for _, m := range info.Mounts {
		if writable, ok := wants[m.Destination]; ok {
			if m.RW != writable {
				t.Fatalf("mount %s writable=%v, want %v", m.Destination, m.RW, writable)
			}
			delete(wants, m.Destination)
		}
	}
	if len(wants) != 0 {
		t.Fatalf("missing mounts %v; actual=%+v", wants, info.Mounts)
	}
	t.Run("object_and_string_readonly_deny_writes", func(t *testing.T) {
		script := fmt.Sprintf(`set -eu
probe=%s
trap 'rm -f "/object-bind/$probe"' EXIT
touch /object-data/sample
printf original > "/object-bind/$probe"
for target in /string-readonly /object-readonly /object-ro; do
 test "$(cat "$target/$probe")" = original
 if printf changed > "$target/$probe"; then exit 1; fi
done
test "$(cat "/object-bind/$probe")" = original`, volume)
		hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", workspace, "--", "sh", "-c", script)
	})
}
