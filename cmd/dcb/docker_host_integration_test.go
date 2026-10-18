//go:build docker_integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	imagetypes "github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"

	"github.com/wyrd-company/devcontainer-builder/internal/dockertest"
)

func TestDockerHostIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Docker integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cli := hostRequireDocker(t, ctx)
	t.Cleanup(func() { _ = cli.Close() })

	imageRef := os.Getenv("DCB_HOST_TEST_IMAGE")
	if imageRef == "" {
		imageRef = "alpine:3.20"
	}
	hostEnsureImage(t, ctx, cli, imageRef)

	bin := hostBuildBinary(t)
	workspace := hostPrepareWorkspace(t, imageRef)
	hostRequireBindableWorkspace(t, ctx, cli, imageRef, workspace)
	t.Cleanup(func() {
		_ = hostRunCommandAllowError(bin, "down", "--workspace-folder", workspace)
	})

	cmdEnv := append(os.Environ(), "DCB_HOST_VALUE=local-value")

	upResult := hostRunCommand(t, cmdEnv, bin,
		"up",
		"--workspace-folder", workspace,
		"--update-remote-user-uid-default", "off",
	)

	var result map[string]any
	if err := json.Unmarshal(upResult, &result); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"containerId", "remoteUser", "remoteWorkspaceFolder"} {
		if value, ok := result[field].(string); !ok || value == "" {
			t.Fatalf("up missing %s: %v", field, result)
		}
	}

	info := hostInspectContainer(t, ctx, cli, workspace)
	hostWriteJSONArtifact(t, "docker-host-container-inspect.json", info)
	if !info.State.Running {
		t.Fatal("container is not running after up")
	}
	if got := info.Config.Labels["devcontainer.local_folder"]; got != workspace {
		t.Fatalf("local folder label = %q, want %q", got, workspace)
	}
	wantConfig := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	if got := info.Config.Labels["devcontainer.config_file"]; got != wantConfig {
		t.Fatalf("config file label = %q, want %q", got, wantConfig)
	}
	hostAssertWorkspaceMount(t, info.Mounts, workspace, "/workspaces/docker-host")

	execOut := hostRunCommand(t, cmdEnv, bin,
		"exec",
		"--workspace-folder", workspace,
		"--",
		"sh", "-lc",
		`printf '%s\n' "$PWD|$REMOTE_STATIC|$REMOTE_FROM_CONTAINER|$REMOTE_FROM_LOCAL|$REMOTE_WORKSPACE"; printf 'exec\n' >> lifecycle.log`,
	)
	wantExec := "/workspaces/docker-host|remote-value|container-value|local-value|/workspaces/docker-host"
	if got := strings.TrimSpace(string(execOut)); got != wantExec {
		t.Fatalf("exec output = %q, want %q", got, wantExec)
	}

	t.Run("exec-pass-through", func(t *testing.T) {
		for _, format := range []string{"text", "json"} {
			for _, code := range []int{0, 1, 7} {
				t.Run(fmt.Sprintf("%s/%d", format, code), func(t *testing.T) {
					command := exec.Command(bin, "exec", "--workspace-folder", workspace, "--log-format", format, "--log-level", "off", "--", "sh", "-c", fmt.Sprintf("printf 'out\\nunterminated'; printf 'err\\nunterminated' >&2; exit %d", code))
					var out, logs bytes.Buffer
					command.Stdout, command.Stderr = &out, &logs
					err := command.Run()
					actual := 0
					if err != nil {
						var exit *exec.ExitError
						if !errors.As(err, &exit) {
							t.Fatal(err)
						}
						actual = exit.ExitCode()
					}
					if actual != code {
						t.Fatalf("child exit=%d want %d", actual, code)
					}
					if out.String() != "out\nunterminated" {
						t.Fatalf("child stdout changed or result appended: %q", out.String())
					}
					if logs.String() != "err\nunterminated" {
						t.Fatalf("child stderr changed: %q", logs.String())
					}
				})
			}
		}
		command := exec.Command(bin, "exec", "--workspace-folder", workspace, "--container-id", "missing-container", "--log-format", "json", "--", "true")
		var out, logs bytes.Buffer
		command.Stdout, command.Stderr = &out, &logs
		err := command.Run()
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 || out.Len() != 0 {
			t.Fatalf("exec operation error=%v stdout=%q", err, out.String())
		}
		var diagnostic map[string]string
		if err := json.Unmarshal(logs.Bytes(), &diagnostic); err != nil || diagnostic["type"] != "diagnostic" || diagnostic["level"] != "error" {
			t.Fatalf("exec diagnostic=%q (%v)", logs.String(), err)
		}
	})

	inspectOut := hostRunCommand(t, cmdEnv, bin, "inspect", "--workspace-folder", workspace)
	hostWriteArtifact(t, "docker-host-cli-inspect.txt", inspectOut)
	for _, want := range []string{
		`"Running":true`,
		`"devcontainer.local_folder":"` + workspace + `"`,
		`"devcontainer.config_file":"` + wantConfig + `"`,
	} {
		if !strings.Contains(string(inspectOut), want) {
			t.Fatalf("inspect output missing %q:\n%s", want, inspectOut)
		}
	}

	firstID := info.ID
	hostRunCommand(t, cmdEnv, bin,
		"up",
		"--workspace-folder", workspace,
		"--update-remote-user-uid-default", "off",
	)
	info = hostInspectContainer(t, ctx, cli, workspace)
	hostWriteJSONArtifact(t, "docker-host-container-inspect-second-up.json", info)
	if info.ID != firstID {
		t.Fatalf("second up created %s, want reuse of %s", info.ID[:12], firstID[:12])
	}

	hostAssertLifecycleLog(t, filepath.Join(workspace, "lifecycle.log"))
	if got, err := os.ReadFile(filepath.Join(workspace, "workspace-write.txt")); err != nil || string(got) != "writable" {
		t.Fatalf("workspace write marker = %q, %v; want writable", got, err)
	}

	// Existing containers remain manageable without the source configuration.
	if err := os.Remove(wantConfig); err != nil {
		t.Fatal(err)
	}
	missingConfigExec := hostRunCommand(t, cmdEnv, bin, "exec", "--workspace-folder", workspace, "--", "pwd")
	if strings.TrimSpace(string(missingConfigExec)) != "/workspaces/docker-host" {
		t.Fatalf("exec without config=%q", missingConfigExec)
	}
	hostRunCommand(t, cmdEnv, bin, "inspect", "--workspace-folder", workspace)
	hostRunCommand(t, cmdEnv, bin, "down", "--workspace-folder", workspace)
	_, err := cli.ContainerInspect(ctx, firstID)
	if err == nil {
		t.Fatalf("container %s still exists after down", firstID[:12])
	}
	if !dockerclient.IsErrNotFound(err) {
		t.Fatalf("inspect after down: %v", err)
	}
}

// TestDockerHostUpPullsMissingImage covers image-only up on an
// image the daemon does not have: up pulls it through the Engine API.
// The image is a test-owned tag in a test-owned registry; the test only
// removes what it created.
func TestDockerHostUpPullsMissingImage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Docker integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cli := hostRequireDocker(t, ctx)
	t.Cleanup(func() { _ = cli.Close() })

	registry := dockertest.StartRegistry(t, ctx, cli)
	dockertest.EnsureImage(t, ctx, cli, "alpine:3.20")
	imageRef := fmt.Sprintf("%s/dcb-test/pull:%d", registry, time.Now().UnixNano())
	hostPublishTestImage(t, ctx, cli, "alpine:3.20", imageRef)

	bin := hostBuildBinary(t)
	workspace := hostPrepareWorkspace(t, imageRef)
	t.Cleanup(func() {
		_ = hostRunCommandAllowError(bin, "down", "--workspace-folder", workspace)
	})

	hostRunCommand(t, append(os.Environ(), "DCB_HOST_VALUE=local-value"), bin,
		"up",
		"--workspace-folder", workspace,
		"--update-remote-user-uid-default", "off",
	)

	if _, err := cli.ImageInspect(ctx, imageRef); err != nil {
		t.Fatalf("image %s not in the daemon after up: %v", imageRef, err)
	}
	info := hostInspectContainer(t, ctx, cli, workspace)
	if !info.State.Running {
		t.Fatal("container is not running after up")
	}
	if info.Config.Image != imageRef {
		t.Fatalf("container image = %q, want %q", info.Config.Image, imageRef)
	}
}

// hostPublishTestImage tags source as the test-owned ref, pushes it to its
// registry, and removes the local tag so the daemon must pull ref. The
// source image is untouched; ref is removed again when the test ends.
func hostPublishTestImage(t *testing.T, ctx context.Context, cli *dockerclient.Client, source, ref string) {
	t.Helper()
	if err := cli.ImageTag(ctx, source, ref); err != nil {
		t.Fatalf("tag %s as %s: %v", source, ref, err)
	}
	t.Cleanup(func() {
		if _, err := cli.ImageRemove(context.Background(), ref, imagetypes.RemoveOptions{}); err != nil && !dockerclient.IsErrNotFound(err) {
			t.Logf("remove %s: %v", ref, err)
		}
	})
	rc, err := cli.ImagePush(ctx, ref, imagetypes.PushOptions{})
	if err != nil {
		t.Fatalf("push %s: %v", ref, err)
	}
	defer rc.Close()
	if err := jsonmessage.DisplayJSONMessagesStream(rc, io.Discard, 0, false, nil); err != nil {
		t.Fatalf("push %s: %v", ref, err)
	}
	if _, err := cli.ImageRemove(ctx, ref, imagetypes.RemoveOptions{}); err != nil {
		t.Fatalf("remove local tag %s: %v", ref, err)
	}
	if _, err := cli.ImageInspect(ctx, ref); !dockerclient.IsErrNotFound(err) {
		t.Fatalf("tag %s still present after remove (err = %v)", ref, err)
	}
	if _, err := cli.ImageInspect(ctx, source); err != nil {
		t.Fatalf("source %s lost while untagging %s: %v", source, ref, err)
	}
}

func hostRequireDocker(t *testing.T, ctx context.Context) *dockerclient.Client {
	t.Helper()
	isolateDockerConfig(t)
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.FromEnv,
		dockerclient.WithAPIVersionNegotiation(),
	)
	if err != nil {
		hostSkipOrFail(t, "docker client: %v", err)
	}
	if _, err := cli.Ping(ctx); err != nil {
		_ = cli.Close()
		hostSkipOrFail(t, "docker daemon is required for Docker integration tests: %v", err)
	}
	return cli
}

func hostSkipOrFail(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("CI") == "true" {
		t.Fatalf(format, args...)
	}
	t.Skipf(format, args...)
}

func hostEnsureImage(t *testing.T, ctx context.Context, cli *dockerclient.Client, ref string) {
	t.Helper()
	if _, err := cli.ImageInspect(ctx, ref); err == nil {
		return
	} else if !dockerclient.IsErrNotFound(err) {
		t.Fatalf("inspect image %s: %v", ref, err)
	}

	rc, err := cli.ImagePull(ctx, ref, imagetypes.PullOptions{})
	if err != nil {
		t.Fatalf("pull image %s: %v", ref, err)
	}
	defer rc.Close()
	if _, err := io.Copy(io.Discard, rc); err != nil {
		t.Fatalf("read pull stream for %s: %v", ref, err)
	}
	if _, err := cli.ImageInspect(ctx, ref); err != nil {
		t.Fatalf("inspect pulled image %s: %v", ref, err)
	}
}

func hostRequireBindableWorkspace(t *testing.T, ctx context.Context, cli *dockerclient.Client, imageRef, workspace string) {
	t.Helper()
	resp, err := cli.ContainerCreate(
		ctx,
		&container.Config{Image: imageRef, Cmd: []string{"true"}},
		&container.HostConfig{Mounts: []mount.Mount{{
			Type:   mount.TypeBind,
			Source: workspace,
			Target: "/workspace",
		}}},
		nil,
		nil,
		"",
	)
	if err != nil {
		if strings.Contains(err.Error(), "bind source path does not exist") {
			hostSkipOrFail(t, "docker daemon cannot access workspace bind source %s: %v", workspace, err)
		}
		t.Fatalf("preflight bind mount for %s: %v", workspace, err)
	}
	t.Cleanup(func() {
		if err := cli.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true, RemoveVolumes: true}); err != nil && !dockerclient.IsErrNotFound(err) {
			t.Logf("remove preflight container %s: %v", resp.ID[:12], err)
		}
	})
}

func hostBuildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "dcb")
	hostRunCommand(t, os.Environ(), "go", "build", "-o", bin, ".")
	return bin
}

func hostPrepareWorkspace(t *testing.T, imageRef string) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := os.MkdirTemp(root, ".tmp-docker-host-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(workspace); err != nil {
			t.Logf("remove temp workspace %s: %v", workspace, err)
		}
	})
	devcontainerDir := filepath.Join(workspace, ".devcontainer")
	if err := os.MkdirAll(devcontainerDir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{
  "name": "docker-host",
  "image": %q,
  "workspaceFolder": "/workspaces/docker-host",
  "updateRemoteUserUID": false,
  "containerEnv": {
    "BASE_ENV": "container-value",
    "FROM_LOCAL": "${localEnv:DCB_HOST_VALUE}",
    "FROM_ID": "${devcontainerId}",
    "WORKSPACE_IN_CONTAINER": "${containerWorkspaceFolder}",
    "WORKSPACE_ON_HOST": "${localWorkspaceFolder}"
  },
  "remoteEnv": {
    "REMOTE_STATIC": "remote-value",
    "REMOTE_FROM_CONTAINER": "${containerEnv:BASE_ENV}",
    "REMOTE_FROM_LOCAL": "${localEnv:DCB_HOST_VALUE}",
    "REMOTE_FROM_ID": "${devcontainerId}",
    "REMOTE_WORKSPACE": "${containerWorkspaceFolder}"
  },
  "onCreateCommand": "printf 'onCreate:%s:%s:%s:%s\n' \"$REMOTE_STATIC\" \"$REMOTE_FROM_CONTAINER\" \"$REMOTE_FROM_LOCAL\" \"$REMOTE_WORKSPACE\" >> lifecycle.log; test \"$FROM_LOCAL\" = 'local-value'; test -n \"$FROM_ID\"; test \"$WORKSPACE_IN_CONTAINER\" = '/workspaces/docker-host'; test \"$WORKSPACE_ON_HOST\" = '${localWorkspaceFolder}'; test -w .; printf writable > workspace-write.txt",
  "postCreateCommand": "printf 'postCreate\n' >> lifecycle.log",
  "postStartCommand": "printf 'postStart\n' >> lifecycle.log",
  "postAttachCommand": "printf 'postAttach\n' >> lifecycle.log"
}
`, imageRef, "%s", "%s", "%s", "%s")
	if err := os.WriteFile(filepath.Join(devcontainerDir, "devcontainer.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return workspace
}

func hostInspectContainer(t *testing.T, ctx context.Context, cli *dockerclient.Client, workspace string) container.InspectResponse {
	t.Helper()
	out := hostRunCommand(t, os.Environ(), "go", "run", ".", "inspect", "--workspace-folder", workspace)
	if !strings.Contains(string(out), `"Running":true`) {
		t.Fatalf("inspect before API lookup did not report running container:\n%s", out)
	}

	// The package under test owns discovery. Ask the CLI to prove discovery
	// first, then use the API for field assertions.
	info, err := hostLookupByWorkspace(ctx, cli, workspace)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func hostLookupByWorkspace(ctx context.Context, cli *dockerclient.Client, workspace string) (container.InspectResponse, error) {
	args := hostFiltersForWorkspace(workspace)
	list, err := cli.ContainerList(ctx, args)
	if err != nil {
		return container.InspectResponse{}, err
	}
	if len(list) == 0 {
		return container.InspectResponse{}, errors.New("container not found for workspace")
	}
	resp, err := cli.ContainerInspect(ctx, list[0].ID)
	if err != nil {
		return container.InspectResponse{}, err
	}
	return resp, nil
}

func hostFiltersForWorkspace(workspace string) container.ListOptions {
	args := filters.NewArgs(filters.Arg("label", "devcontainer.local_folder="+workspace))
	return container.ListOptions{All: true, Filters: args}
}

func hostAssertWorkspaceMount(t *testing.T, mounts []container.MountPoint, source, target string) {
	t.Helper()
	for _, m := range mounts {
		if m.Type == mount.TypeBind && m.Source == source && m.Destination == target {
			if !m.RW {
				t.Fatalf("workspace mount %s -> %s is not writable", source, target)
			}
			return
		}
	}
	t.Fatalf("missing workspace bind mount %s -> %s in %+v", source, target, mounts)
}

func hostAssertLifecycleLog(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	want := []string{
		"onCreate:remote-value:container-value:local-value:/workspaces/docker-host",
		"postCreate",
		"postStart",
		"postAttach",
		"exec",
		"postAttach",
	}
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Fatalf("lifecycle log:\ngot:\n%s\nwant:\n%s", strings.Join(lines, "\n"), strings.Join(want, "\n"))
	}
}

func hostRunCommand(t *testing.T, env []string, name string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = "."
	cmd.Env = env
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if (name != "go" || (len(args) > 0 && args[0] == "run")) && !(len(args) > 0 && args[0] == "exec") {
		var result map[string]any
		if decodeErr := json.Unmarshal(out, &result); decodeErr != nil || result["outcome"] != "success" || bytes.Count(out, []byte("\n")) != 1 {
			t.Fatalf("CLI result: %s, stderr: %s, error: %v", out, stderr.String(), err)
		}
	}
	if err != nil {
		t.Fatalf("%s: %v\nstdout: %s\nstderr: %s", name, err, out, stderr.String())
	}
	if len(out) > 0 {
		t.Logf("%s %s\n%s", name, strings.Join(args, " "), out)
	}
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return out
}

func hostRunCommandAllowError(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, out)
	}
	return nil
}

func hostWriteJSONArtifact(t *testing.T, name string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Logf("marshal artifact %s: %v", name, err)
		return
	}
	hostWriteArtifact(t, name, append(data, '\n'))
}

func hostWriteArtifact(t *testing.T, name string, data []byte) {
	t.Helper()
	dir := os.Getenv("DCB_ARTIFACT_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Logf("create artifact dir %s: %v", dir, err)
		return
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Logf("write artifact %s: %v", path, err)
	}
}
