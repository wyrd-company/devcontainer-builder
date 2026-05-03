//go:build docker_integration

package main

import (
	"context"
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
)

func TestDockerHostIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Docker integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cli := requireDocker(t, ctx)
	defer cli.Close()

	imageRef := os.Getenv("DCB_HOST_TEST_IMAGE")
	if imageRef == "" {
		imageRef = "alpine:3.20"
	}
	ensureImage(t, ctx, cli, imageRef)

	bin := buildBinary(t)
	workspace := prepareWorkspace(t, imageRef)
	requireBindableWorkspace(t, ctx, cli, imageRef, workspace)
	t.Cleanup(func() {
		_ = runCommandAllowError(bin, "down", "--workspace", workspace)
	})

	cmdEnv := append(os.Environ(), "DCB_HOST_VALUE=local-value")

	runCommand(t, cmdEnv, bin,
		"up",
		"--workspace", workspace,
		"--image", imageRef,
		"--skip-build",
		"--update-remote-user-uid", "off",
	)

	info := inspectContainer(t, ctx, cli, workspace)
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
	assertWorkspaceMount(t, info.Mounts, workspace, "/workspaces/docker-host")

	execOut := runCommand(t, cmdEnv, bin,
		"exec",
		"--workspace", workspace,
		"-t=false",
		"--",
		"sh", "-lc",
		`printf '%s\n' "$PWD|$REMOTE_STATIC|$REMOTE_FROM_CONTAINER|$REMOTE_FROM_LOCAL|$REMOTE_WORKSPACE"; printf 'exec\n' >> lifecycle.log`,
	)
	wantExec := "/workspaces/docker-host|remote-value|container-value|local-value|/workspaces/docker-host"
	if got := strings.TrimSpace(string(execOut)); got != wantExec {
		t.Fatalf("exec output = %q, want %q", got, wantExec)
	}

	inspectOut := runCommand(t, cmdEnv, bin, "inspect", "--workspace", workspace)
	for _, want := range []string{
		"running:    true",
		"devcontainer.local_folder=" + workspace,
		"devcontainer.config_file=" + wantConfig,
	} {
		if !strings.Contains(string(inspectOut), want) {
			t.Fatalf("inspect output missing %q:\n%s", want, inspectOut)
		}
	}

	firstID := info.ID
	runCommand(t, cmdEnv, bin,
		"up",
		"--workspace", workspace,
		"--image", imageRef,
		"--skip-build",
		"--update-remote-user-uid", "off",
	)
	info = inspectContainer(t, ctx, cli, workspace)
	if info.ID != firstID {
		t.Fatalf("second up created %s, want reuse of %s", info.ID[:12], firstID[:12])
	}

	assertLifecycleLog(t, filepath.Join(workspace, "lifecycle.log"))
	if got, err := os.ReadFile(filepath.Join(workspace, "workspace-write.txt")); err != nil || string(got) != "writable" {
		t.Fatalf("workspace write marker = %q, %v; want writable", got, err)
	}

	runCommand(t, cmdEnv, bin, "down", "--workspace", workspace)
	_, err := cli.ContainerInspect(ctx, firstID)
	if err == nil {
		t.Fatalf("container %s still exists after down", firstID[:12])
	}
	if !dockerclient.IsErrNotFound(err) {
		t.Fatalf("inspect after down: %v", err)
	}
}

func requireDocker(t *testing.T, ctx context.Context) *dockerclient.Client {
	t.Helper()
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.FromEnv,
		dockerclient.WithAPIVersionNegotiation(),
	)
	if err != nil {
		skipOrFail(t, "docker client: %v", err)
	}
	if _, err := cli.Ping(ctx); err != nil {
		_ = cli.Close()
		skipOrFail(t, "docker daemon is required for Docker integration tests: %v", err)
	}
	return cli
}

func skipOrFail(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("CI") == "true" {
		t.Fatalf(format, args...)
	}
	t.Skipf(format, args...)
}

func ensureImage(t *testing.T, ctx context.Context, cli *dockerclient.Client, ref string) {
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

func requireBindableWorkspace(t *testing.T, ctx context.Context, cli *dockerclient.Client, imageRef, workspace string) {
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
			skipOrFail(t, "docker daemon cannot access workspace bind source %s: %v", workspace, err)
		}
		t.Fatalf("preflight bind mount for %s: %v", workspace, err)
	}
	t.Cleanup(func() {
		if err := cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true, RemoveVolumes: true}); err != nil && !dockerclient.IsErrNotFound(err) {
			t.Logf("remove preflight container %s: %v", resp.ID[:12], err)
		}
	})
}

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "devcontainer-docker")
	runCommand(t, os.Environ(), "go", "build", "-o", bin, ".")
	return bin
}

func prepareWorkspace(t *testing.T, imageRef string) string {
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

func inspectContainer(t *testing.T, ctx context.Context, cli *dockerclient.Client, workspace string) container.InspectResponse {
	t.Helper()
	out := runCommand(t, os.Environ(), "go", "run", ".", "inspect", "--workspace", workspace)
	if !strings.Contains(string(out), "running:    true") {
		t.Fatalf("inspect before API lookup did not report running container:\n%s", out)
	}

	// The package under test owns discovery. Ask the CLI to prove discovery
	// first, then use the API for field assertions.
	info, err := lookupByWorkspace(ctx, cli, workspace)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func lookupByWorkspace(ctx context.Context, cli *dockerclient.Client, workspace string) (container.InspectResponse, error) {
	args := filtersForWorkspace(workspace)
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

func filtersForWorkspace(workspace string) container.ListOptions {
	args := filters.NewArgs(filters.Arg("label", "devcontainer.local_folder="+workspace))
	return container.ListOptions{All: true, Filters: args}
}

func assertWorkspaceMount(t *testing.T, mounts []container.MountPoint, source, target string) {
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

func assertLifecycleLog(t *testing.T, path string) {
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

func runCommand(t *testing.T, env []string, name string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = "."
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		t.Logf("%s %s\n%s", name, strings.Join(args, " "), out)
	}
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return out
}

func runCommandAllowError(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, out)
	}
	return nil
}
