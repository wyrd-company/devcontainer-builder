//go:build docker_integration

// relationships:
//
//	references: [options_lifecycle, lifecycle, ../host/runner/controls]
package cli

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	hostdocker "github.com/wyrd-company/devcontainer-builder/pkg/host/docker"
)

// The real CLI and Engine boundary prove all seven command-option handlers.
func TestLifecycleOptionsDockerIntegration(t *testing.T) {
	cli, err := newDockerClient()
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	ctx := context.Background()
	for _, mode := range []string{"image", "compose"} {
		t.Run(mode, func(t *testing.T) {
			workspace := t.TempDir()
			cfg := filepath.Join(workspace, ".devcontainer.json")
			base := `"image":"alpine:3.20"`
			if mode == "compose" {
				// A per-test project name keeps parallel task runs independent.
				workspaceID := sha256.Sum256([]byte(workspace))
				project := fmt.Sprintf("sample-%x", workspaceID[:6])
				compose := fmt.Sprintf("name: %s\nservices:\n  app:\n    image: alpine:3.20\n", project)
				if err := os.WriteFile(filepath.Join(workspace, "compose.yml"), []byte(compose), 0o644); err != nil {
					t.Fatal(err)
				}
				base = `"dockerComposeFile":"compose.yml","service":"app","workspaceFolder":"/"`
			}
			config := `{` + base + `,"waitFor":"postStartCommand","updateRemoteUserUID":false,"onCreateCommand":"printf c >> /tmp/events","updateContentCommand":"printf u >> /tmp/events","postCreateCommand":"printf p >> /tmp/events","postStartCommand":"printf s >> /tmp/events","postAttachCommand":"printf a >> /tmp/events"}`
			if err := os.WriteFile(cfg, []byte(config), 0o644); err != nil {
				t.Fatal(err)
			}
			args := []string{"--workspace-folder", workspace, "--config", cfg}
			t.Cleanup(func() {
				// down also cleans the Compose project's own network.
				invoke(t, 0, append([]string{"down"}, args...)...)
			})
			if mode == "image" {
				// An absent build input proves this check precedes building.
				invalidBuild := strings.TrimSuffix(config, "}") + `,"build":{"dockerfile":"missing.Dockerfile"}}`
				if err := os.WriteFile(cfg, []byte(invalidBuild), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			missing, _ := invoke(t, 1, append([]string{"up", "--expect-existing-container"}, args...)...)
			if missing["message"] != "The expected container does not exist." {
				t.Fatalf("missing=%v", missing)
			}
			if err := os.WriteFile(cfg, []byte(config), 0o644); err != nil {
				t.Fatal(err)
			}
			result, _ := invoke(t, 0, append([]string{"up", "--skip-post-create"}, args...)...)
			id := result["containerId"].(string)
			// The local dotfiles target needs no clone. Its git sentinel fails if called.
			runShell := func(t *testing.T, script string) string {
				t.Helper()
				res, err := (&application{}).dockerHost(cli).Exec(ctx, hostdocker.ExecRequest{ContainerID: id, Cmd: []string{"/bin/sh", "-c", script}})
				if err != nil || res.ExitCode != 0 {
					t.Fatalf("shell=%q result=%+v err=%v", script, res, err)
				}
				return string(res.Stdout)
			}
			reset := func(t *testing.T) {
				t.Helper()
				runShell(t, "rm -f /tmp/events /root/.devcontainer/.*CommandMarker /root/.devcontainer/.dotfilesMarker")
			}
			check := func(t *testing.T, want string) {
				t.Helper()
				if got := runShell(t, "cat /tmp/events"); got != want {
					t.Fatalf("events=%q want=%q", got, want)
				}
			}
			for _, command := range []string{"up", "run-user-commands", "set-up"} {
				t.Run("skip-"+command, func(t *testing.T) {
					reset(t)
					commandArgs := append([]string{command, "--skip-non-blocking-commands"}, args...)
					if command == "set-up" {
						commandArgs = append(commandArgs, "--container-id", id)
					}
					got, _ := invoke(t, 0, commandArgs...)
					checkLifecycleResponse(t, command, got, "skipNonBlocking")
					check(t, "cups")
				})
			}
			for _, command := range []string{"up", "run-user-commands"} {
				t.Run("prebuild-"+command, func(t *testing.T) {
					reset(t)
					commandArgs := append([]string{command, "--prebuild"}, args...)
					got, _ := invoke(t, 0, commandArgs...)
					checkLifecycleResponse(t, command, got, "prebuild")
					got, _ = invoke(t, 0, commandArgs...)
					checkLifecycleResponse(t, command, got, "prebuild")
					check(t, "cuu")
				})
			}
			t.Run("personalization", func(t *testing.T) {
				reset(t)
				runShell(t, "mkdir -p /tmp/personal; printf '#!/bin/sh\\nexit 99\\n' > /usr/local/bin/git; chmod +x /usr/local/bin/git; printf 'printf d >> /tmp/events\\n' > /tmp/personal/install.sh")
				got, _ := invoke(t, 0, append([]string{"run-user-commands", "--stop-for-personalization", "--dotfiles-repository", "/tmp/personal", "--dotfiles-target-path", "/tmp/personal", "--dotfiles-install-command", "install.sh"}, args...)...)
				checkLifecycleResponse(t, "run-user-commands", got, "stopForPersonalization")
				check(t, "cupd")
			})
			t.Run("default", func(t *testing.T) {
				reset(t)
				up, _ := invoke(t, 0, append([]string{"up"}, args...)...)
				checkLifecycleResponse(t, "up", up, "")
				check(t, "cupsa")
				reset(t)
				got, _ := invoke(t, 0, append([]string{"run-user-commands"}, args...)...)
				checkLifecycleResponse(t, "run-user-commands", got, "done")
				check(t, "cupsa")
			})
			if mode == "compose" {
				t.Run("expect-drift", func(t *testing.T) {
					composePath := filepath.Join(workspace, "compose.yml")
					data, err := os.ReadFile(composePath)
					if err != nil {
						t.Fatal(err)
					}
					data = append(data, []byte("    environment:\n      EXAMPLE: changed\n")...)
					if err := os.WriteFile(composePath, data, 0o644); err != nil {
						t.Fatal(err)
					}
					got, _ := invoke(t, 0, append([]string{"up", "--expect-existing-container", "--skip-post-create"}, args...)...)
					if got["containerId"] != id {
						t.Fatalf("existing container recreated after drift: %v", got)
					}
				})
			}
			t.Run("expect-stopped", func(t *testing.T) {
				reset(t)
				stopTimeout := 0 // Existing Engine stop API; no new application timeout.
				if err := cli.ContainerStop(ctx, id, container.StopOptions{Timeout: &stopTimeout}); err != nil {
					t.Fatal(err)
				}
				got, _ := invoke(t, 0, append([]string{"up", "--expect-existing-container"}, args...)...)
				if got["containerId"] != id {
					t.Fatalf("existing container replaced: %v", got)
				}
				check(t, "cupsa")
			})
			t.Run("expect-remove", func(t *testing.T) {
				got, _ := invoke(t, 0, append([]string{"up", "--expect-existing-container", "--remove-existing-container", "--skip-post-create"}, args...)...)
				if got["containerId"] == id {
					t.Fatal("explicit replacement did not replace the existing container")
				}
			})
		})
	}
}

func checkLifecycleResponse(t *testing.T, command string, got map[string]any, want string) {
	t.Helper()
	if command == "run-user-commands" {
		if got["result"] != want {
			t.Fatalf("response=%v want result %s", got, want)
		}
	} else if _, present := got["result"]; present {
		t.Fatalf("%s must preserve its response fields: %v", command, got)
	}
}
