//go:build docker_integration

// relationships:
//
//	references: [docker_host_integration_test, ../../pkg/host/local/initialize]
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	imagetypes "github.com/docker/docker/api/types/image"
)

func TestInitializeCommandCLI(t *testing.T) {
	ctx := context.Background()
	cli := hostRequireDocker(t, ctx)
	t.Cleanup(func() { _ = cli.Close() })
	hostEnsureImage(t, ctx, cli, "alpine:3.20")
	bin := hostBuildBinary(t)
	for _, mode := range []string{"dockerfile", "compose"} {
		t.Run(mode, func(t *testing.T) {
			workspace := hostPrepareWorkspace(t, "alpine:3.20")
			config := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
			command := "printf initialized > initialized; printf x >> invocations"
			raw := map[string]any{"initializeCommand": command, "postCreateCommand": "test -f /tmp/initialized"}
			args := []string{"up", "--workspace-folder", workspace, "--update-remote-user-uid-default", "off"}
			if mode == "dockerfile" {
				raw["build"] = map[string]any{"dockerfile": "Dockerfile", "context": ".."}
				if err := os.WriteFile(filepath.Join(filepath.Dir(config), "Dockerfile"), []byte("FROM alpine:3.20\nCOPY initialized /tmp/initialized\n"), 0600); err != nil {
					t.Fatal(err)
				}
				tag := fmt.Sprintf("dcb-test/initialize:%d", time.Now().UnixNano())
				args = append(args, "--image-name", tag)
				t.Cleanup(func() { _, _ = cli.ImageRemove(ctx, tag, imagetypes.RemoveOptions{}) })
			} else {
				raw["dockerComposeFile"], raw["service"], raw["workspaceFolder"] = "compose.yml", "app", "/workspace"
				raw["postCreateCommand"] = "test -f /workspace/initialized"
				body := fmt.Sprintf("name: sample-init-%d\nservices:\n  app:\n    image: alpine:3.20\n    command: [sleep, infinity]\n    volumes: [\"..:/workspace\"]\n", time.Now().UnixNano())
				if err := os.WriteFile(filepath.Join(filepath.Dir(config), "compose.yml"), []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
			}
			data, _ := json.Marshal(raw)
			if err := os.WriteFile(config, data, 0600); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = hostRunCommandAllowError(bin, "down", "--workspace-folder", workspace) })
			for i := 1; i <= 2; i++ {
				hostRunCommand(t, os.Environ(), bin, args...)
				data, err := os.ReadFile(filepath.Join(workspace, "invocations"))
				if err != nil || len(data) != i {
					t.Fatalf("invocations=%q err=%v want=%d", data, err, i)
				}
			}
		})
	}
}
