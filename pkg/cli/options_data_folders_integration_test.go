//go:build docker_integration

// relationships:
//
// references: [options_data_folders, lifecycle, ../host/docker/up, ../host/dockercompose/up]
package cli

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	hostdocker "github.com/wyrd-company/devcontainer-builder/pkg/host/docker"
)

func TestDataFoldersDockerIntegration(t *testing.T) {
	cli, err := newDockerClient()
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	for _, mode := range []string{"image", "compose"} {
		t.Run(mode, func(t *testing.T) {
			workspace := t.TempDir()
			cfg := filepath.Join(workspace, ".devcontainer.json")
			source := `"image":"alpine:3.20"`
			if mode == "compose" {
				hash := sha256.Sum256([]byte(workspace))
				compose := fmt.Sprintf("name: sample-%x\nservices:\n  app:\n    image: alpine:3.20\n", hash[:6])
				if err := os.WriteFile(filepath.Join(workspace, "compose.yml"), []byte(compose), 0o644); err != nil {
					t.Fatal(err)
				}
				source = `"dockerComposeFile":"compose.yml","service":"app","workspaceFolder":"/"`
			}
			if err := os.WriteFile(cfg, []byte(`{`+source+`,"updateRemoteUserUID":false,"postCreateCommand":"true"}`), 0o644); err != nil {
				t.Fatal(err)
			}
			common := []string{"--workspace-folder", workspace, "--config", cfg}
			t.Cleanup(func() { invoke(t, 0, append([]string{"down"}, common...)...) })
			for _, command := range []string{"up", "run-user-commands", "set-up"} {
				folder := "/tmp/state 'folder/" + command
				args := append([]string{command, "--container-data-folder", folder + "/../" + command + "/", "--container-system-data-folder", "/tmp/system", "--container-session-data-folder", "/tmp/session"}, common...)
				// Create a container before exercising existing-container commands.
				if command != "up" {
					result, _ := invoke(t, 0, append([]string{"up", "--skip-post-create"}, common...)...)
					args = append(args, "--container-id", result["containerId"].(string))
				}
				result, _ := invoke(t, 0, args...)
				id := result["containerId"].(string)
				prep, err := (&application{}).dockerHost(cli).Exec(context.Background(), hostdocker.ExecRequest{ContainerID: id, Cmd: []string{"/bin/sh", "-c", "mkdir -p /tmp/existing /usr/local/bin && ln -sf /bin/false /usr/local/bin/git && printf '#!/bin/sh\\nexit 0\\n' > /tmp/existing/install.sh"}})
				if err != nil || prep.ExitCode != 0 {
					t.Fatalf("prepare dotfiles target: %+v %v", prep, err)
				}
				_, logs := invoke(t, 0, append(args, "--dotfiles-repository", "/tmp/source", "--dotfiles-target-path", "/tmp/existing", "--dotfiles-install-command", "install.sh")...)
				check := "test -f " + quoteDataFolder(folder+"/.postCreateCommandMarker") + " && test -f " + quoteDataFolder(folder+"/.dotfilesMarker") + " && test ! -e /root/.devcontainer/.postCreateCommandMarker && test ! -e /tmp/system && test ! -e /tmp/session"
				res, err := (&application{}).dockerHost(cli).Exec(context.Background(), hostdocker.ExecRequest{ContainerID: id, Cmd: []string{"/bin/sh", "-c", check}})
				if err != nil || res.ExitCode != 0 {
					t.Fatalf("%s %s marker not routed: %+v %v; logs=%s", mode, command, res, err, logs)
				}
			}
		})
	}
}

func quoteDataFolder(value string) string { return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'" }
