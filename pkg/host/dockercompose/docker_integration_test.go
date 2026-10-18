//go:build docker_integration

package dockercompose

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	imagetypes "github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	dockerclient "github.com/docker/docker/client"

	"github.com/wyrd-company/devcontainer-builder/pkg/builder"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestDockerComposeIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Docker integration test in short mode")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	cli := requireComposeDocker(t, ctx)
	t.Cleanup(func() {
		_ = cli.Close()
	})

	ensureComposeImage(t, ctx, cli, "alpine:3.20")
	ensureComposeImage(t, ctx, cli, "busybox:1.36")

	workspace := prepareComposeWorkspace(t)
	helperTag := fmt.Sprintf("devcontainer-builder/compose-helper-%d:latest", time.Now().UnixNano())
	primaryTag := fmt.Sprintf("devcontainer-builder/compose-primary-%d:latest", time.Now().UnixNano())
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		removeComposeImage(t, cleanupCtx, cli, helperTag)
		removeComposeImage(t, cleanupCtx, cli, primaryTag)
	})
	writeComposeFixture(t, workspace, primaryTag, helperTag, "one")
	requireBindableComposePath(t, ctx, cli, "alpine:3.20", workspace)

	specPath := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	dc, err := spec.Load(specPath)
	if err != nil {
		t.Fatalf("spec.Load: %v", err)
	}
	assertProfileFiltering(t, ctx, dc, workspace)

	primary, err := BuildPrimaryImage(ctx, BuildPrimaryInputs{
		Spec:      dc,
		Workspace: workspace,
		Tag:       primaryTag + "-derived",
		BuilderOptions: builder.Options{
			Progress: "plain",
			Stdout:   testLogWriter{t: t},
			Stderr:   testLogWriter{t: t},
		},
	})
	if err != nil {
		t.Fatalf("BuildPrimaryImage: %v", err)
	}
	if !primary.Built || primary.Image != primaryTag {
		t.Fatalf("primary build result = %+v, want Compose service image %s", primary, primaryTag)
	}

	h := New(cli, Options{
		Logger:            t.Logf,
		Stdout:            testLogWriter{t: t},
		Stderr:            testLogWriter{t: t},
		StopTimeout:       time.Second,
		DependencyTimeout: 25 * time.Second,
	})
	t.Cleanup(func() {
		if err := h.Remove(context.Background(), workspace); err != nil && !errors.Is(err, ErrProjectNotFound) {
			t.Logf("remove compose project: %v", err)
		}
	})

	req := UpRequest{
		Spec:               dc,
		SpecPath:           specPath,
		LocalWorkspace:     workspace,
		ContainerWorkspace: "/workspace",
		PrimaryImage:       primary.Image,
		LocalEnv: map[string]string{
			"DCB_COMPOSE_VALUE": "from-local-env",
		},
	}
	first, err := h.Up(ctx, req)
	if err != nil {
		t.Fatalf("compose up: %v", err)
	}
	assertCreatedServices(t, first, []string{"app", "healthy", "helper", "setup"})
	assertComposeRuntime(t, ctx, cli, h, workspace, specPath, first)

	again, err := h.Up(ctx, req)
	if err != nil {
		t.Fatalf("second compose up: %v", err)
	}
	if len(again.CreatedThisCall) != 0 || len(again.RecreatedThisCall) != 0 {
		t.Fatalf("second up created=%v recreated=%v, want reuse", again.CreatedThisCall, again.RecreatedThisCall)
	}
	if again.PrimaryContainerID != first.PrimaryContainerID {
		t.Fatalf("second up primary = %s, want reuse of %s", short(again.PrimaryContainerID), short(first.PrimaryContainerID))
	}

	writeComposeFixture(t, workspace, primaryTag, helperTag, "two")
	changed, err := h.Up(ctx, req)
	if err != nil {
		t.Fatalf("compose up after config change: %v", err)
	}
	if changed.PrimaryContainerID == first.PrimaryContainerID {
		t.Fatalf("config change reused primary %s; want recreation", short(first.PrimaryContainerID))
	}
	if !hasString(changed.RecreatedThisCall, "app") {
		t.Fatalf("recreated services = %v, want app", changed.RecreatedThisCall)
	}

	assertWatch(t, ctx, h, dc, workspace, changed.PrimaryContainerID)

	if err := h.Remove(ctx, workspace); err != nil {
		t.Fatalf("remove compose project: %v", err)
	}
	assertProjectRemoved(t, ctx, cli, first.ProjectName)
}

func assertProfileFiltering(t *testing.T, ctx context.Context, dc *spec.DevContainer, workspace string) {
	t.Helper()
	proj, err := LoadProject(ctx, LoadInputs{Spec: dc, Workspace: workspace})
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}
	filtered, err := applyProfiles(proj, nil, []string{dc.Service})
	if err != nil {
		t.Fatalf("applyProfiles without profiles: %v", err)
	}
	if hasString(filtered.ServiceNames(), "debug") {
		t.Fatalf("profile-gated service was active without its profile: %v", filtered.ServiceNames())
	}
	filtered, err = applyProfiles(proj, []string{"debug"}, []string{dc.Service})
	if err != nil {
		t.Fatalf("applyProfiles debug: %v", err)
	}
	if !hasString(filtered.ServiceNames(), "debug") {
		t.Fatalf("debug profile services = %v, want debug", filtered.ServiceNames())
	}
}

func assertComposeRuntime(
	t *testing.T,
	ctx context.Context,
	cli *dockerclient.Client,
	h *Host,
	workspace, specPath string,
	res *UpResult,
) {
	t.Helper()
	if res.PrimaryContainerID == "" {
		t.Fatal("primary container ID is empty")
	}
	info, err := h.Inspect(ctx, workspace)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if info.Name != res.ProjectName {
		t.Fatalf("project name = %q, want %q", info.Name, res.ProjectName)
	}
	writeJSONArtifact(t, "docker-compose-project-inspect.json", info)
	services := map[string]ContainerInfo{}
	for _, c := range info.Containers {
		services[c.Service] = c
		assertComposeLabels(t, c, res.ProjectName, workspace, specPath)
	}
	for _, name := range []string{"app", "healthy", "helper", "setup"} {
		if _, ok := services[name]; !ok {
			t.Fatalf("service %q missing from inspected project: %+v", name, info.Containers)
		}
	}
	if _, ok := services["debug"]; ok {
		t.Fatalf("profile-gated service was created without the profile")
	}
	if !services["app"].Primary {
		t.Fatalf("app was not marked primary: %+v", services["app"].Labels)
	}
	for _, name := range []string{"healthy", "helper", "setup"} {
		labels := services[name].Labels
		if labels[LabelLocalFolder] != "" || labels[LabelConfigFile] != "" || labels[LabelPrimary] != "" {
			t.Fatalf("sidecar %q has devcontainer discovery labels: %+v", name, labels)
		}
	}
	if deps := services["app"].Labels[LabelDependencies]; !strings.Contains(deps, "healthy:service_healthy:true") ||
		!strings.Contains(deps, "setup:service_completed_successfully:true") {
		t.Fatalf("app dependency label = %q", deps)
	}

	primary, err := h.InspectPrimary(ctx, workspace)
	if err != nil {
		t.Fatalf("InspectPrimary: %v", err)
	}
	if primary.ID != res.PrimaryContainerID {
		t.Fatalf("InspectPrimary ID = %s, want %s", short(primary.ID), short(res.PrimaryContainerID))
	}

	appInspect, err := cli.ContainerInspect(ctx, res.PrimaryContainerID)
	if err != nil {
		t.Fatalf("inspect app container: %v", err)
	}
	writeJSONArtifact(t, "docker-compose-primary-container-inspect.json", appInspect)
	assertMount(t, appInspect.Mounts, mount.TypeBind, workspace, "/workspace")
	assertMount(t, appInspect.Mounts, mount.TypeBind, filepath.Join(workspace, "host-bind"), "/host-bind")
	assertMount(t, appInspect.Mounts, mount.TypeVolume, res.ProjectName+"_app-cache", "/cache")
	assertNetworkExists(t, ctx, cli, res.ProjectName+"_default", res.ProjectName)
	assertVolumeExists(t, ctx, cli, res.ProjectName+"_app-cache", res.ProjectName)

	appExec := execOK(t, ctx, h, ExecRequest{
		ContainerID: res.PrimaryContainerID,
		Cmd: []string{
			"sh", "-lc",
			"test \"$PWD\" = /workspace && test -f /usr/local/share/compose-primary && test -f lifecycle.log && printf app-ok",
		},
		WorkDir: "/workspace",
	})
	if strings.TrimSpace(string(appExec.Stdout)) != "app-ok" {
		t.Fatalf("app exec stdout = %q, want app-ok", appExec.Stdout)
	}
	helperExec := execOK(t, ctx, h, ExecRequest{
		ContainerID: res.ContainerIDs["helper"],
		Cmd:         []string{"sh", "-lc", "test -f /usr/local/share/compose-helper && printf helper-ok"},
	})
	if strings.TrimSpace(string(helperExec.Stdout)) != "helper-ok" {
		t.Fatalf("helper exec stdout = %q, want helper-ok", helperExec.Stdout)
	}

	body, err := os.ReadFile(filepath.Join(workspace, "lifecycle.log"))
	if err != nil {
		t.Fatalf("read lifecycle log: %v", err)
	}
	if !strings.Contains(string(body), "onCreate:from-local-env") ||
		!strings.Contains(string(body), "postStart") ||
		!strings.Contains(string(body), "postAttach") {
		t.Fatalf("lifecycle log missing expected entries:\n%s", body)
	}
}

func assertWatch(t *testing.T, ctx context.Context, h *Host, dc *spec.DevContainer, workspace, primaryID string) {
	t.Helper()
	before, err := h.client.ContainerInspect(ctx, primaryID)
	if err != nil {
		t.Fatalf("inspect before watch restart: %v", err)
	}

	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	events := make(chan watchEvent, 4)
	errCh := make(chan error, 1)
	go func() {
		errCh <- h.Watch(watchCtx, WatchRequest{
			Spec:           dc,
			LocalWorkspace: workspace,
			PollInterval:   100 * time.Millisecond,
			OnEvent: func(service string, action types.WatchAction, changed []string) {
				events <- watchEvent{service: service, action: action, changed: changed}
			},
		})
	}()

	time.Sleep(500 * time.Millisecond)
	syncPath := filepath.Join(workspace, "watch-sync", "message.txt")
	if err := os.WriteFile(syncPath, []byte("synced"), 0o644); err != nil {
		t.Fatalf("write watched file: %v", err)
	}
	waitWatchEvent(t, events, types.WatchActionSync)
	out := execOK(t, ctx, h, ExecRequest{
		ContainerID: primaryID,
		Cmd:         []string{"sh", "-lc", "cat /tmp/watch-sync/message.txt"},
	})
	if strings.TrimSpace(string(out.Stdout)) != "synced" {
		t.Fatalf("synced file = %q, want synced", out.Stdout)
	}

	restartPath := filepath.Join(workspace, "watch-restart", "restart.txt")
	if err := os.WriteFile(restartPath, []byte(time.Now().Format(time.RFC3339Nano)), 0o644); err != nil {
		t.Fatalf("write restart trigger: %v", err)
	}
	waitWatchEvent(t, events, types.WatchActionRestart)
	waitRestarted(t, ctx, h.client, primaryID, before.State.StartedAt)

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Watch returned error after cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Watch did not stop after cancellation")
	}
}

type watchEvent struct {
	service string
	action  types.WatchAction
	changed []string
}

func waitWatchEvent(t *testing.T, events <-chan watchEvent, action types.WatchAction) {
	t.Helper()
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for {
		select {
		case ev := <-events:
			if ev.service == "app" && ev.action == action {
				return
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for watch action %s", action)
		}
	}
}

func waitRestarted(t *testing.T, ctx context.Context, cli *dockerclient.Client, id, oldStartedAt string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		insp, err := cli.ContainerInspect(ctx, id)
		if err != nil {
			t.Fatalf("inspect while waiting for restart: %v", err)
		}
		if insp.State != nil && insp.State.Running && insp.State.StartedAt != oldStartedAt {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("container %s did not restart from startedAt %q", short(id), oldStartedAt)
}

func prepareComposeWorkspace(t *testing.T) string {
	t.Helper()
	root := repoRoot(t)
	workspace, err := os.MkdirTemp(root, ".tmp-compose-host-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(workspace); err != nil {
			t.Logf("remove temp workspace %s: %v", workspace, err)
		}
	})
	for _, dir := range []string{
		filepath.Join(workspace, ".devcontainer"),
		filepath.Join(workspace, "app-image"),
		filepath.Join(workspace, "helper-image"),
		filepath.Join(workspace, "host-bind"),
		filepath.Join(workspace, "watch-sync"),
		filepath.Join(workspace, "watch-restart"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(workspace, "app-image", "Dockerfile"), `FROM alpine:3.20
RUN echo primary > /usr/local/share/compose-primary
`)
	writeFile(t, filepath.Join(workspace, "helper-image", "Dockerfile"), `FROM busybox:1.36
RUN mkdir -p /usr/local/share && echo helper > /usr/local/share/compose-helper
`)
	writeFile(t, filepath.Join(workspace, "watch-sync", "message.txt"), "initial")
	writeFile(t, filepath.Join(workspace, "watch-restart", "restart.txt"), "initial")
	writeFile(t, filepath.Join(workspace, ".devcontainer", "devcontainer.json"), `{
  "name": "compose-integration",
  "dockerComposeFile": "docker-compose.yml",
  "service": "app",
  "workspaceFolder": "/workspace",
  "remoteEnv": {
    "FROM_LOCAL": "${localEnv:DCB_COMPOSE_VALUE}"
  },
  "onCreateCommand": "test -f /host-bind/setup-done && test -f /host-bind/healthy-ready && mkdir -p /tmp/watch-sync && printf 'onCreate:%s\n' \"$FROM_LOCAL\" >> lifecycle.log",
  "postStartCommand": "printf 'postStart\n' >> lifecycle.log",
  "postAttachCommand": "printf 'postAttach\n' >> lifecycle.log"
}
`)
	return workspace
}

func writeComposeFixture(t *testing.T, workspace, primaryTag, helperTag, appVersion string) {
	t.Helper()
	appImage := filepath.ToSlash(filepath.Join(workspace, "app-image"))
	helperImage := filepath.ToSlash(filepath.Join(workspace, "helper-image"))
	hostBind := filepath.ToSlash(filepath.Join(workspace, "host-bind"))
	watchSync := filepath.ToSlash(filepath.Join(workspace, "watch-sync"))
	watchRestart := filepath.ToSlash(filepath.Join(workspace, "watch-restart"))
	body := fmt.Sprintf(`services:
  app:
    image: %q
    build:
      context: %q
    environment:
      APP_VERSION: %q
    depends_on:
      healthy:
        condition: service_healthy
      setup:
        condition: service_completed_successfully
    volumes:
      - app-cache:/cache
      - type: bind
        source: %q
        target: /host-bind
    develop:
      watch:
        - action: sync
          path: %q
          target: /tmp/watch-sync
        - action: restart
          path: %q

  healthy:
    image: busybox:1.36
    command: ["sh", "-c", "sleep 1; echo healthy > /host-bind/healthy-ready; while true; do sleep 1; done"]
    volumes:
      - type: bind
        source: %q
        target: /host-bind
    healthcheck:
      test: ["CMD-SHELL", "test -f /host-bind/healthy-ready"]
      interval: 200ms
      timeout: 1s
      retries: 30

  setup:
    image: busybox:1.36
    command: ["sh", "-c", "sleep 2; echo setup > /host-bind/setup-done"]
    volumes:
      - type: bind
        source: %q
        target: /host-bind

  helper:
    build:
      context: %q
    image: %q
    command: ["sh", "-c", "while true; do sleep 1; done"]

  debug:
    image: busybox:1.36
    profiles: ["debug"]
    command: ["sh", "-c", "while true; do sleep 1; done"]

volumes:
  app-cache:
`, primaryTag, appImage, appVersion, hostBind, watchSync, watchRestart, hostBind, hostBind, helperImage, helperTag)
	writeFile(t, filepath.Join(workspace, ".devcontainer", "docker-compose.yml"), body)
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertCreatedServices(t *testing.T, res *UpResult, want []string) {
	t.Helper()
	got := append([]string(nil), res.CreatedThisCall...)
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("created services = %v, want %v", got, want)
	}
}

func assertComposeLabels(t *testing.T, c ContainerInfo, projectName, workspace, specPath string) {
	t.Helper()
	for _, key := range []string{LabelProject, LabelService, LabelOneoff, LabelContainerN, LabelConfigHash, LabelWorkingDir, LabelConfigFiles, LabelVersion} {
		if c.Labels[key] == "" {
			t.Fatalf("container %s missing label %s: %+v", short(c.ID), key, c.Labels)
		}
	}
	if c.Labels[LabelProject] != projectName {
		t.Fatalf("container %s project label = %q, want %q", short(c.ID), c.Labels[LabelProject], projectName)
	}
	if c.Labels[LabelWorkingDir] != workspace {
		t.Fatalf("container %s working dir label = %q, want %q", short(c.ID), c.Labels[LabelWorkingDir], workspace)
	}
	if c.Labels[LabelConfigFiles] != specPath {
		t.Fatalf("container %s config files label = %q, want %q", short(c.ID), c.Labels[LabelConfigFiles], specPath)
	}
}

func assertMount(t *testing.T, mounts []container.MountPoint, typ mount.Type, source, target string) {
	t.Helper()
	for _, m := range mounts {
		actualSource := m.Source
		if typ == mount.TypeVolume {
			actualSource = m.Name
		}
		if m.Type == typ && actualSource == source && m.Destination == target {
			return
		}
	}
	t.Fatalf("missing %s mount %s -> %s in %+v", typ, source, target, mounts)
}

func assertNetworkExists(t *testing.T, ctx context.Context, cli *dockerclient.Client, name, projectName string) {
	t.Helper()
	args := filters.NewArgs(filters.Arg("name", name), filters.Arg("label", LabelProject+"="+projectName))
	list, err := cli.NetworkList(ctx, network.ListOptions{Filters: args})
	if err != nil {
		t.Fatalf("list networks: %v", err)
	}
	for _, n := range list {
		if n.Name == name {
			return
		}
	}
	t.Fatalf("network %s with project label %s not found", name, projectName)
}

func assertVolumeExists(t *testing.T, ctx context.Context, cli *dockerclient.Client, name, projectName string) {
	t.Helper()
	args := filters.NewArgs(filters.Arg("name", name), filters.Arg("label", LabelProject+"="+projectName))
	list, err := cli.VolumeList(ctx, volume.ListOptions{Filters: args})
	if err != nil {
		t.Fatalf("list volumes: %v", err)
	}
	for _, v := range list.Volumes {
		if v.Name == name {
			return
		}
	}
	t.Fatalf("volume %s with project label %s not found", name, projectName)
}

func assertProjectRemoved(t *testing.T, ctx context.Context, cli *dockerclient.Client, projectName string) {
	t.Helper()
	args := filters.NewArgs(filters.Arg("label", LabelProject+"="+projectName))
	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		t.Fatalf("list containers after remove: %v", err)
	}
	if len(containers) != 0 {
		t.Fatalf("containers still exist after remove: %+v", containers)
	}
	nets, err := cli.NetworkList(ctx, network.ListOptions{Filters: args})
	if err != nil {
		t.Fatalf("list networks after remove: %v", err)
	}
	if len(nets) != 0 {
		t.Fatalf("networks still exist after remove: %+v", nets)
	}
	vols, err := cli.VolumeList(ctx, volume.ListOptions{Filters: args})
	if err != nil {
		t.Fatalf("list volumes after remove: %v", err)
	}
	if len(vols.Volumes) != 0 {
		t.Fatalf("volumes still exist after remove: %+v", vols.Volumes)
	}
}

func execOK(t *testing.T, ctx context.Context, h *Host, req ExecRequest) *ExecResult {
	t.Helper()
	res, err := h.Exec(ctx, req)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exec %v exit code = %d\nstdout:\n%s\nstderr:\n%s", req.Cmd, res.ExitCode, res.Stdout, res.Stderr)
	}
	return res
}

func requireComposeDocker(t *testing.T, ctx context.Context) *dockerclient.Client {
	t.Helper()
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.FromEnv,
		dockerclient.WithAPIVersionNegotiation(),
	)
	if err != nil {
		skipOrFailCompose(t, "docker client: %v", err)
	}
	if _, err := cli.Ping(ctx); err != nil {
		_ = cli.Close()
		skipOrFailCompose(t, "docker daemon is required for Docker integration tests: %v", err)
	}
	return cli
}

func ensureComposeImage(t *testing.T, ctx context.Context, cli *dockerclient.Client, ref string) {
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
}

func requireBindableComposePath(t *testing.T, ctx context.Context, cli *dockerclient.Client, imageRef, hostPath string) {
	t.Helper()
	resp, err := cli.ContainerCreate(
		ctx,
		&container.Config{Image: imageRef, Cmd: []string{"true"}},
		&container.HostConfig{Mounts: []mount.Mount{{
			Type:   mount.TypeBind,
			Source: hostPath,
			Target: "/workspace",
		}}},
		nil,
		nil,
		"",
	)
	if err != nil {
		if strings.Contains(err.Error(), "bind source path does not exist") {
			skipOrFailCompose(t, "docker daemon cannot access bind source %s: %v", hostPath, err)
		}
		t.Fatalf("preflight bind mount for %s: %v", hostPath, err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		if err := cli.ContainerRemove(cleanupCtx, resp.ID, container.RemoveOptions{Force: true, RemoveVolumes: true}); err != nil && !dockerclient.IsErrNotFound(err) {
			t.Logf("remove preflight container %s: %v", short(resp.ID), err)
		}
	})
}

func removeComposeImage(t *testing.T, ctx context.Context, cli *dockerclient.Client, ref string) {
	t.Helper()
	if ref == "" {
		return
	}
	if _, err := cli.ImageRemove(ctx, ref, imagetypes.RemoveOptions{Force: true, PruneChildren: true}); err != nil && !dockerclient.IsErrNotFound(err) {
		t.Logf("remove image %s: %v", ref, err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatalf("git rev-parse --show-toplevel: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func skipOrFailCompose(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("CI") == "true" {
		t.Fatalf(format, args...)
	}
	t.Skipf(format, args...)
}

func hasString(list []string, want string) bool {
	for _, got := range list {
		if got == want {
			return true
		}
	}
	return false
}

type testLogWriter struct {
	t *testing.T
}

func (w testLogWriter) Write(p []byte) (int, error) {
	text := strings.TrimSpace(string(p))
	if text != "" {
		w.t.Log(text)
	}
	return len(p), nil
}

func writeJSONArtifact(t *testing.T, name string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Logf("marshal artifact %s: %v", name, err)
		return
	}
	writeArtifact(t, name, append(data, '\n'))
}

func writeArtifact(t *testing.T, name string, data []byte) {
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
