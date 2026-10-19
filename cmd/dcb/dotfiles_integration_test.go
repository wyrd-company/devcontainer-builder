//go:build docker_integration

// relationships:
//
// references: [CONTRIBUTING]
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

// The dotfiles repository is a bare repository that the test creates in the
// workspace and bind-mounts at dotfilesMount. Its files belong to the host
// user, so the remote environment points git at a configuration that marks
// them safe. Upstream passes the remote environment to its clone the same way.
const (
	dotfilesMount = "/srv/dotfiles-source"
	dotfilesRepo  = dotfilesMount + "/repo.git"
)

// dotfilesInstallScripts append to a counter so that a repeated install shows.
var dotfilesInstallScripts = map[string]string{
	"install.sh": "#!/bin/sh\necho default >> \"$HOME/dotfiles-runs\"\n",
	"custom.sh":  "#!/bin/sh\necho custom >> \"$HOME/dotfiles-runs\"\n",
}

type dotfilesFixture struct {
	workspace, tag, bin string
}

// newDotfilesFixture writes an alpine configuration. features is the
// devcontainer.json features value; compose selects a Compose configuration.
func newDotfilesFixture(t *testing.T, bin, features string, compose bool) dotfilesFixture {
	t.Helper()
	workspace := hostPrepareWorkspace(t, "alpine:3.20")
	configDir := filepath.Join(workspace, ".devcontainer")
	if err := os.MkdirAll(filepath.Join(workspace, "dotfiles-source"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "dotfiles-source", "gitconfig"), []byte("[safe]\n\tdirectory = *\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	featureDir := filepath.Join(configDir, "git")
	if err := os.MkdirAll(featureDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"devcontainer-feature.json": `{"id":"git","version":"1.0.0"}`,
		"install.sh":                "#!/bin/sh\nset -e\napk add --no-cache git\n",
	} {
		if err := os.WriteFile(filepath.Join(featureDir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	remoteEnv := `{"GIT_CONFIG_GLOBAL":"` + dotfilesMount + `/gitconfig"}`
	body := `{"image":"alpine:3.20","updateRemoteUserUID":false,"features":` + features + `,"remoteEnv":` + remoteEnv +
		`,"mounts":[{"type":"bind","source":"${localWorkspaceFolder}/dotfiles-source","target":"` + dotfilesMount + `"}]}`
	if compose {
		body = `{"dockerComposeFile":"compose.yml","service":"app","features":` + features + `,"remoteEnv":` + remoteEnv + `}`
		yml := "services:\n  app:\n    image: alpine:3.20\n    command: [sleep, infinity]\n    volumes:\n      - ../dotfiles-source:" + dotfilesMount + "\n"
		if err := os.WriteFile(filepath.Join(configDir, "compose.yml"), []byte(yml), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(configDir, "devcontainer.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	f := dotfilesFixture{workspace: workspace, tag: fmt.Sprintf("dcb-test/dotfiles:%d", time.Now().UnixNano()), bin: bin}
	t.Cleanup(func() {
		_ = hostRunCommandAllowError(bin, "down", "--workspace-folder", workspace)
		removeImage(t, f.tag)
	})
	return f
}

// createRepo makes the bare dotfiles repository with the generic scripts.
func (f dotfilesFixture) createRepo(t *testing.T) {
	t.Helper()
	src := t.TempDir()
	for name, body := range dotfilesInstallScripts {
		if err := os.WriteFile(filepath.Join(src, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	env := testgit.Env()
	for _, args := range [][]string{
		{"-C", src, "init", "-q", "-b", "main"},
		{"-C", src, "add", "."},
		{"-C", src, "commit", "-q", "-m", "dotfiles"},
		{"clone", "-q", "--bare", src, filepath.Join(f.workspace, "dotfiles-source", "repo.git")},
	} {
		cmd := exec.Command("git", args...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
}

// run invokes dcb and returns stdout, stderr and the exit code.
func (f dotfilesFixture) run(t *testing.T, args ...string) (map[string]any, string, int) {
	t.Helper()
	cmd := exec.Command(f.bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if decodeErr := json.Unmarshal(stdout.Bytes(), &result); decodeErr != nil {
		t.Fatalf("%v: result %q: %v\nstderr: %s", args, stdout.String(), decodeErr, stderr.String())
	}
	return result, stderr.String(), code
}

func (f dotfilesFixture) mustRun(t *testing.T, args ...string) (map[string]any, string) {
	t.Helper()
	result, stderr, code := f.run(t, args...)
	if code != 0 || result["outcome"] != "success" {
		t.Fatalf("%v: exit %d result %v\nstderr: %s", args, code, result, stderr)
	}
	return result, stderr
}

// state reads the install counter and whether the marker and clone exist.
func (f dotfilesFixture) state(t *testing.T) string {
	t.Helper()
	out := hostRunCommand(t, os.Environ(), f.bin, "exec", "--workspace-folder", f.workspace, "--", "sh", "-c",
		`printf 'runs=%s marker=%s clone=%s' "$(cat ~/dotfiles-runs 2>/dev/null | tr '\n' ,)" "$(test -e ~/.devcontainer/.dotfilesMarker && echo yes || echo no)" "$(test -d ~/dotfiles/.git && echo yes || echo no)"`)
	return strings.TrimSpace(string(out))
}

func dotfilesBinary(t *testing.T) string {
	t.Helper()
	cli := hostRequireDocker(t, context.Background())
	t.Cleanup(func() { _ = cli.Close() })
	hostEnsureImage(t, context.Background(), cli, "alpine:3.20")
	return hostBuildBinary(t)
}

// TestDotfilesUpAcceptance is the UAT scenario as one command: an image
// configuration, a published feature from --additional-features, and a
// dotfiles repository with an explicit install command.
func TestDotfilesUpAcceptance(t *testing.T) {
	bin := dotfilesBinary(t)
	f := newDotfilesFixture(t, bin, `{}`, false)
	f.createRepo(t)
	f.mustRun(t, "up", "--workspace-folder", f.workspace, "--image-name", f.tag,
		"--additional-features", `{"ghcr.io/devcontainers/features/common-utils:2":{}}`,
		"--dotfiles-repository", dotfilesRepo,
		"--dotfiles-install-command", "custom.sh")
	if got := f.state(t); got != "runs=custom, marker=yes clone=yes" {
		t.Fatalf("dotfiles state = %q", got)
	}
	// common-utils installs zsh and Oh My Zsh for the remote user.
	hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", f.workspace, "--", "sh", "-c", "command -v zsh && test -d ~/.oh-my-zsh")
	label := string(runCommand(t, "docker", "image", "inspect", "-f", `{{index .Config.Labels "devcontainer.metadata"}}`, f.tag))
	if !strings.Contains(label, `"id":"ghcr.io/devcontainers/features/common-utils:2"`) {
		t.Fatalf("image metadata lacks the additional feature: %s", label)
	}
}

// TestDotfilesLifecycle installs once per container through every command
// that takes the dotfiles flags, in both host modes.
func TestDotfilesLifecycle(t *testing.T) {
	bin := dotfilesBinary(t)
	for _, mode := range []string{"image", "compose"} {
		t.Run(mode, func(t *testing.T) {
			f := newDotfilesFixture(t, bin, `{"./git":{}}`, mode == "compose")
			f.createRepo(t)
			flags := []string{"--dotfiles-repository", dotfilesRepo}
			result, _ := f.mustRun(t, append([]string{"up", "--workspace-folder", f.workspace, "--image-name", f.tag, "--skip-post-create"}, flags...)...)
			if got := f.state(t); got != "runs= marker=no clone=no" {
				t.Fatalf("up --skip-post-create installed dotfiles: %q", got)
			}
			id := result["containerId"].(string)
			f.mustRun(t, append([]string{"run-user-commands", "--workspace-folder", f.workspace, "--skip-post-create"}, flags...)...)
			if got := f.state(t); got != "runs= marker=no clone=no" {
				t.Fatalf("run-user-commands --skip-post-create installed dotfiles: %q", got)
			}
			f.mustRun(t, append([]string{"run-user-commands", "--workspace-folder", f.workspace}, flags...)...)
			if got := f.state(t); got != "runs=default, marker=yes clone=yes" {
				t.Fatalf("run-user-commands with the default install script: %q", got)
			}
			_, stderr := f.mustRun(t, append([]string{"up", "--workspace-folder", f.workspace, "--image-name", f.tag}, flags...)...)
			f.mustRun(t, append([]string{"set-up", "--workspace-folder", f.workspace, "--container-id", id, "--dotfiles-install-command", "custom.sh"}, flags...)...)
			if got := f.state(t); got != "runs=default, marker=yes clone=yes" {
				t.Fatalf("a second install ran: %q", got)
			}
			if !strings.Contains(stderr, "skipping dotfiles install (marker found)") {
				t.Fatalf("second up did not report the marker:\n%s", stderr)
			}
		})
	}
}

// TestDotfilesFailureRetries: as upstream, a failed install does not fail up;
// dcb warns and releases the marker, so the next up installs.
func TestDotfilesFailureRetries(t *testing.T) {
	bin := dotfilesBinary(t)
	f := newDotfilesFixture(t, bin, `{"./git":{}}`, false)
	flags := []string{"--dotfiles-repository", dotfilesRepo, "--dotfiles-target-path", "~/personal"}
	_, stderr := f.mustRun(t, append([]string{"up", "--workspace-folder", f.workspace, "--image-name", f.tag, "--skip-post-create"}, flags...)...)
	if strings.Contains(stderr, "installing dotfiles") || strings.Contains(stderr, "dotfiles install") {
		t.Fatalf("up --skip-post-create attempted dotfiles:\n%s", stderr)
	}
	if got := f.state(t); got != "runs= marker=no clone=no" {
		t.Fatalf("after up --skip-post-create: %q", got)
	}
	// The repository does not exist yet, so the clone fails.
	_, stderr = f.mustRun(t, append([]string{"up", "--workspace-folder", f.workspace, "--image-name", f.tag, "--log-format", "json"}, flags...)...)
	warned := false
	for _, line := range strings.Split(strings.TrimSpace(stderr), "\n") {
		var record map[string]string
		if json.Unmarshal([]byte(line), &record) == nil && record["type"] == "diagnostic" && record["level"] == "warn" &&
			strings.HasPrefix(record["text"], "warning: dotfiles install from "+dotfilesRepo+" failed: exit 128") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("no typed warning for the failed install:\n%s", stderr)
	}
	if got := f.state(t); got != "runs= marker=no clone=no" {
		t.Fatalf("after the failed install: %q", got)
	}
	f.createRepo(t)
	f.mustRun(t, append([]string{"up", "--workspace-folder", f.workspace, "--image-name", f.tag}, flags...)...)
	out := hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", f.workspace, "--", "sh", "-c",
		"cat ~/dotfiles-runs; test -d ~/personal/.git && test -e ~/.devcontainer/.dotfilesMarker && echo retried")
	if got := strings.TrimSpace(string(out)); got != "default\nretried" {
		t.Fatalf("retry on the next up: %q", got)
	}
}

func TestCreateRepoIgnoresInheritedRepository(t *testing.T) {
	testgit.AssertIsolated(t, func() {
		workspace := t.TempDir()
		if err := os.Mkdir(filepath.Join(workspace, "dotfiles-source"), 0o755); err != nil {
			t.Fatal(err)
		}
		f := dotfilesFixture{workspace: workspace}
		f.createRepo(t)
		cmd := exec.Command("git", "--git-dir="+filepath.Join(workspace, "dotfiles-source", "repo.git"), "show", "HEAD:install.sh")
		cmd.Env = testgit.Env()
		out, err := cmd.CombinedOutput()
		if err != nil || string(out) != dotfilesInstallScripts["install.sh"] {
			t.Fatalf("fixture repository script = %q: %v", out, err)
		}
	})
}
