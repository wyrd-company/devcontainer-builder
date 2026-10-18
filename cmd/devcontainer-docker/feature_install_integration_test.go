//go:build docker_integration

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wyrd-company/devcontainer-builder/pkg/buildkit"
)

// TestFeatureInstallIntegration builds a generic base image with a
// published feature (common-utils, non-default options) and a local
// feature, starts it with up, and asserts the effects inside the
// container.
func TestFeatureInstallIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Docker integration test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	cli := requireDocker(t, ctx)
	defer cli.Close()
	if buildkit.DefaultAddress() == "" {
		skipOrFail(t, "BuildKit is required for feature install integration; set BUILDKIT_HOST or expose /run/buildkit/buildkitd.sock")
	}

	bin := buildBinary(t)
	workspace := copyFixture(t, filepath.Join("..", "..", "testdata", "smoke", "published-feature"))
	tag := fmt.Sprintf("devcontainer-builder/feature-install-%d:latest", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = runCommandAllowError(bin, "down", "--workspace", workspace)
		_ = exec.Command("docker", "image", "rm", "-f", tag).Run()
	})

	runCommand(t, os.Environ(), bin,
		"up",
		"--workspace", workspace,
		"--tag", tag,
		"--update-remote-user-uid", "off",
	)

	img, err := cli.ImageInspect(ctx, tag)
	if err != nil {
		t.Fatalf("inspect image %s: %v", tag, err)
	}
	if img.Config == nil || img.Config.User != "devuser" {
		t.Fatalf("image user = %+v, want devuser", img.Config)
	}

	out := runCommand(t, os.Environ(), bin,
		"exec",
		"--workspace", workspace,
		"-t=false",
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
