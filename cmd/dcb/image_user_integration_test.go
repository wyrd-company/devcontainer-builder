//go:build docker_integration

// relationships:
//   references: [feature_install_integration_test, ../../testdata/smoke/root-entrypoint]

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDockerRootEntrypointWithRemoteUser(t *testing.T) {
	cli := hostRequireDocker(t, context.Background())
	t.Cleanup(func() { _ = cli.Close() })
	bin := hostBuildBinary(t)
	for _, generated := range []bool{false, true} {
		for _, source := range []string{"image", "dockerfile"} {
			t.Run(fmt.Sprintf("%s/generated=%v", source, generated), func(t *testing.T) {
				workspace := copyFixture(t, filepath.Join("..", "..", "testdata", "smoke", "root-entrypoint"))
				configDir := filepath.Join(workspace, ".devcontainer")
				configPath := filepath.Join(configDir, "devcontainer.json")
				body, err := os.ReadFile(configPath)
				if err != nil {
					t.Fatal(err)
				}
				var config map[string]any
				if err := json.Unmarshal(body, &config); err != nil {
					t.Fatal(err)
				}
				tag := fmt.Sprintf("dcb-test/image-user:%d", time.Now().UnixNano())
				baseTag := tag + "-base"
				t.Cleanup(func() {
					_ = hostRunCommandAllowError(bin, "down", "--workspace-folder", workspace)
					removeImage(t, tag)
					if generated && source == "dockerfile" {
						removeImage(t, baseTag)
					}
				})
				wantImageUser := ""
				if source == "dockerfile" {
					delete(config, "image")
					config["build"] = map[string]any{"dockerfile": "Dockerfile"}
					config["containerUser"] = "root"
					wantImageUser = "sample-user"
					if err := os.WriteFile(filepath.Join(configDir, "Dockerfile"), []byte("FROM alpine:3.20\nRUN adduser -D -u 1234 sample-user\nUSER sample-user\n"), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				writeConfig := func(value map[string]any) {
					t.Helper()
					body, err := json.Marshal(value)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(configPath, body, 0o644); err != nil {
						t.Fatal(err)
					}
				}
				writeConfig(config)
				if generated {
					workDir := filepath.Join(workspace, "generated")
					hostRunCommand(t, os.Environ(), bin, "generate", "--workspace-folder", workspace, "--work-dir", workDir)
					dfPath := filepath.Join(workDir, "Dockerfile.devcontainer-builder")
					if source == "dockerfile" {
						// Generate leaves Dockerfile builds for the caller to resolve.
						runDocker(t, "build", "-t", baseTag, configDir)
						df, err := os.ReadFile(dfPath)
						if err != nil {
							t.Fatal(err)
						}
						df = []byte(strings.ReplaceAll(string(df), "<unresolved-base-build-required>", baseTag))
						if err := os.WriteFile(dfPath, df, 0o644); err != nil {
							t.Fatal(err)
						}
					}
					buildArgs := []string{"build", "-t", tag, "-f", dfPath}
					if syntax := os.Getenv("DCB_TEST_DOCKERFILE_SYNTAX"); syntax != "" {
						buildArgs = append(buildArgs, "--build-arg", "BUILDKIT_SYNTAX="+syntax)
					}
					runDocker(t, append(buildArgs, filepath.Join(workDir, "context"))...)
					// Start the built image using its persisted feature/user metadata.
					writeConfig(map[string]any{"image": tag, "workspaceFolder": "/workspace", "workspaceMount": "type=volume,target=/workspace"})
				}
				hostRunCommand(t, os.Environ(), bin, "up", "--workspace-folder", workspace, "--image-name", tag, "--update-remote-user-uid-default", "never")
				if got := inspectImage(t, tag).User; got != wantImageUser && !(generated && source == "image" && got == "root") {
					t.Fatalf("image USER = %q, want base user %q", got, wantImageUser)
				}
				out := hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", workspace, "--", "sh", "-c",
					`id -u; awk '/^Uid:/{print $2}' /proc/1/status; cat /tmp/entrypoint-user /tmp/feature-user /tmp/config-user`)
				if got := strings.TrimSpace(string(out)); got != "1234\n0\n0\n1234\n1234" {
					t.Fatalf("exec, PID 1, entrypoint, feature hook, config hook users = %q", got)
				}
			})
		}
	}
}
