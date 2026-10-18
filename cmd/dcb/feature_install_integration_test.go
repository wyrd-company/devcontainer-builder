//go:build docker_integration

// relationships:
//
// references: [CONTRIBUTING]
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
)

// TestFeatureInstallIntegration builds a generic base image with a
// published feature (common-utils, non-default options) and a local
// feature, starts it with up, and asserts the effects inside the
// container. It builds on the Docker daemon's BuildKit, or on
// BUILDKIT_HOST when set.
func TestFeatureInstallIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Docker integration test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	cli := hostRequireDocker(t, ctx)
	defer cli.Close()

	bin := hostBuildBinary(t)
	workspace := copyFixture(t, filepath.Join("..", "..", "testdata", "smoke", "published-feature"))
	tag := fmt.Sprintf("devcontainer-builder/feature-install-%d:latest", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = hostRunCommandAllowError(bin, "down", "--workspace-folder", workspace)
		_ = exec.Command("docker", "image", "rm", "-f", tag).Run()
	})

	up := hostRunCommand(t, os.Environ(), bin,
		"up",
		"--workspace-folder", workspace,
		"--image-name", tag,
		"--update-remote-user-uid-default", "off",
	)

	// Existing lifecycle markers must also work when remoteUser differs
	// from PID 1 and its environment cannot be read by the remote user.
	hostRunCommand(t, os.Environ(), bin, "run-user-commands", "--workspace-folder", workspace)
	var result struct {
		ContainerID string `json:"containerId"`
	}
	if err := json.Unmarshal(up, &result); err != nil {
		t.Fatal(err)
	}
	hostRunCommand(t, os.Environ(), bin, "set-up", "--workspace-folder", workspace, "--container-id", result.ContainerID)
	img, err := cli.ImageInspect(ctx, tag)
	if err != nil {
		t.Fatalf("inspect image %s: %v", tag, err)
	}
	if img.Config == nil || img.Config.User != "" {
		t.Fatalf("image user = %+v, want the base image's default root user", img.Config)
	}

	out := hostRunCommand(t, os.Environ(), bin,
		"exec",
		"--workspace-folder", workspace,
		"--tty=false",
		"--",
		"sh", "-c",
		`id -un; id -u devuser; cat /usr/local/share/dcb-smoke-install.txt; cat /tmp/dcb-smoke-entrypoint.txt; cat /tmp/dcb-smoke-hooks.log`,
	)
	want := strings.Join([]string{
		// Remote user and the non-default userUid option of common-utils,
		// which creates the user from _REMOTE_USER.
		"devuser",
		"1234",
		// The local feature installs after common-utils (untagged
		// installsAfter) and sees its option and the builtin variables.
		// _REMOTE_USER_HOME is resolved before any feature installs, as
		// upstream does, so it is empty for a user a feature creates.
		"hello world|devuser||1234",
		// The local feature's entrypoint ran at container start.
		"started",
		// Feature hooks run before devcontainer.json hooks.
		"marker",
		"devcontainer.json",
	}, "\n")
	if got := strings.TrimSpace(string(out)); got != want {
		t.Fatalf("container state:\n%s\nwant:\n%s", got, want)
	}
}

// copyFixture copies a fixture workspace under the repository's .tmp/
// directory so the docker daemon can bind-mount it.
func copyFixture(t *testing.T, src string) string {
	t.Helper()
	tmp, err := filepath.Abs(filepath.Join("..", "..", ".tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	workspace, err := os.MkdirTemp(tmp, "feature-install-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	if out, err := exec.Command("cp", "-R", src+"/.", workspace).CombinedOutput(); err != nil {
		t.Fatalf("copy fixture: %v\n%s", err, out)
	}
	return workspace
}

// TestDockerFeatureWiringCLI keeps the build-to-host agreement observable for
// both host modes: the installed image alone cannot run these contributions.
func TestDockerFeatureWiringCLI(t *testing.T) {
	cli := hostRequireDocker(t, context.Background())
	t.Cleanup(func() { _ = cli.Close() })
	hostEnsureImage(t, context.Background(), cli, "alpine:3.20")
	bin := hostBuildBinary(t)
	for _, mode := range []string{"image", "compose"} {
		t.Run(mode, func(t *testing.T) {
			workspace := hostPrepareWorkspace(t, "alpine:3.20")
			configDir := filepath.Join(workspace, ".devcontainer")
			featureDir := filepath.Join(configDir, "features", "marker")
			if err := os.MkdirAll(featureDir, 0o755); err != nil {
				t.Fatal(err)
			}
			files := map[string]string{
				"devcontainer-feature.json": `{"id":"marker","version":"1.0.0","entrypoint":"/usr/local/bin/example-start","postCreateCommand":"echo feature >> /tmp/example-hooks"}`,
				"install.sh":                "#!/bin/sh\nset -e\nprintf '#!/bin/sh\\necho started > /tmp/example-started\\n' > /usr/local/bin/example-start\nchmod +x /usr/local/bin/example-start\n",
			}
			for name, body := range files {
				if err := os.WriteFile(filepath.Join(featureDir, name), []byte(body), 0o755); err != nil {
					t.Fatal(err)
				}
			}
			body := `{"image":"alpine:3.20","features":{"./features/marker":{}},"postCreateCommand":"echo config >> /tmp/example-hooks"}`
			if mode == "compose" {
				body = `{"dockerComposeFile":"compose.yml","service":"app","overrideCommand":false,"features":{"./features/marker":{}},"postCreateCommand":"echo config >> /tmp/example-hooks"}`
				if err := os.WriteFile(filepath.Join(configDir, "compose.yml"), []byte("services:\n  app:\n    image: alpine:3.20\n    command: [sleep, infinity]\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(configDir, "devcontainer.json"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			tag := fmt.Sprintf("dcb-test/feature-wiring-%s:%d", mode, time.Now().UnixNano())
			t.Cleanup(func() {
				_ = hostRunCommandAllowError(bin, "down", "--workspace-folder", workspace)
				removeImage(t, tag)
			})
			hostRunCommand(t, os.Environ(), bin, "up", "--workspace-folder", workspace, "--image-name", tag, "--update-remote-user-uid-default", "off")
			out := hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", workspace, "--", "sh", "-c", "cat /tmp/example-started /tmp/example-hooks")
			if got := strings.TrimSpace(string(out)); got != "started\nfeature\nconfig" {
				t.Fatalf("feature contributions = %q, want entrypoint then feature/config hooks", got)
			}
		})
	}
}
