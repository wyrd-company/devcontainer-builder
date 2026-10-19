//go:build docker_integration

// relationships:
//
//	references: [options_lockfile, ../features/lockfile]
package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	imagetypes "github.com/docker/docker/api/types/image"
	dockerclient "github.com/docker/docker/client"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
)

// The real CLI, shell feature installer and Docker build prove both option
// handlers on each command, including publication after the entire up succeeds.
func TestLockfileOptionsDockerIntegration(t *testing.T) {
	cli, err := newDockerClient()
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	for _, command := range []string{"build", "up"} {
		t.Run(command, func(t *testing.T) {
			workspace := lockfileFixture(t)
			config := filepath.Join(workspace, ".devcontainer.json")
			path := features.LockfilePath(config)
			args := []string{command, "--workspace-folder", workspace, "--work-dir", t.TempDir()}
			// Cleanup is registered before assertions or failure injection.
			t.Cleanup(func() {
				if command == "up" {
					invoke(t, 0, "down", "--workspace-folder", workspace)
				}
				if _, err := cli.ImageRemove(context.Background(), defaultImage(workspace), imagetypes.RemoveOptions{}); err != nil && !dockerclient.IsErrNotFound(err) {
					t.Errorf("remove owned image: %v", err)
				}
			})
			invoke(t, 0, args...)
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatalf("default created lockfile: %v", err)
			}
			invoke(t, 0, append(args, "--experimental-lockfile")...)
			canonical := "{\n  \"features\": {}\n}\n"
			data, err := os.ReadFile(path)
			if err != nil || string(data) != canonical {
				t.Fatalf("lockfile=%s err=%v", data, err)
			}
			// Whitespace is not drift; frozen leaves the original bytes untouched.
			cosmetic := " { \"features\" : {} } \n\t"
			if err := os.WriteFile(path, []byte(cosmetic), 0o644); err != nil {
				t.Fatal(err)
			}
			invoke(t, 0, append(args, "--experimental-frozen-lockfile", "--experimental-lockfile")...)
			if data, _ := os.ReadFile(path); string(data) != cosmetic {
				t.Fatal("frozen rewrote cosmetic differences")
			}
			stale := `{"features":{"registry.example/collection/stale:1":{"version":"1.0.0","resolved":"unused","integrity":"unused"}}}`
			if err := os.WriteFile(path, []byte(stale), 0o644); err != nil {
				t.Fatal(err)
			}
			result, _ := invoke(t, 1, append(args, "--experimental-frozen-lockfile")...)
			if result["message"] != "Lockfile does not match." {
				t.Fatalf("drift=%v", result)
			}
			if data, _ := os.ReadFile(path); string(data) != stale {
				t.Fatal("frozen changed stale lockfile")
			}
			// A failing build or post-create command must preserve the old file.
			if command == "build" {
				if err := os.WriteFile(filepath.Join(workspace, "sample", "install.sh"), []byte("#!/bin/sh\nexit 31\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			} else {
				invoke(t, 0, "down", "--workspace-folder", workspace)
				data, err := os.ReadFile(config)
				if err != nil {
					t.Fatal(err)
				}
				data = []byte(strings.TrimSuffix(string(data), "}") + `,"postCreateCommand":"exit 31"}`)
				if err := os.WriteFile(config, data, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			invoke(t, 1, append(args, "--experimental-lockfile")...)
			if data, _ := os.ReadFile(path); string(data) != stale {
				t.Fatal("failed command published lockfile")
			}
			// A successful retry updates an existing file without the create flag.
			if command == "build" {
				if err := os.WriteFile(filepath.Join(workspace, "sample", "install.sh"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
					t.Fatal(err)
				}
			} else {
				invoke(t, 0, "down", "--workspace-folder", workspace)
				if err := os.WriteFile(config, []byte(`{"image":"alpine:3.20","features":{"./sample":{}},"updateRemoteUserUID":false}`), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			invoke(t, 0, args...)
			if data, _ := os.ReadFile(path); string(data) != canonical {
				t.Fatalf("retry lockfile=%s", data)
			}
		})
	}
}
