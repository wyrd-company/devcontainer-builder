//go:build docker_integration

// relationships:
//
//	references: [metadata, up, cli, build]
package dockercompose_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	imagetypes "github.com/docker/docker/api/types/image"
	dockerclient "github.com/docker/docker/client"

	"github.com/wyrd-company/devcontainer-builder/internal/dockertest"
	command "github.com/wyrd-company/devcontainer-builder/pkg/cli"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/dockercompose"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// TestMetadataPrebuiltComposeIntegration exercises Up through the CLI, then
// consumes the same persisted list through merged reads and standalone hooks.
// Run live evidence only against a disposable daemon with an empty Docker config.
func TestMetadataPrebuiltComposeIntegration(t *testing.T) {
	ctx := context.Background()
	cli := metadataDocker(t, ctx)
	h := dockercompose.New(cli, dockercompose.Options{Logger: t.Logf})
	workspace := metadataWorkspace(t)
	t.Setenv("SAMPLE_VALUE", "local-value")
	truth, falsity := true, false
	entries := []metadata.Entry{
		{ID: "./sample", Init: &truth, Privileged: &truth, CapAdd: []string{"SYS_PTRACE"}, SecurityOpt: []string{"no-new-privileges"}, Mounts: []any{map[string]any{"type": "bind", "source": "${localWorkspaceFolder}", "target": "/sample-source", "readOnly": true}, "type=volume,source=sample-cache,target=/sample-cache"}, ContainerUser: "nobody", RemoteUser: "root", OverrideCommand: &falsity, ContainerEnv: map[string]any{"IMAGE": "image", "VALUE": "${localEnv:SAMPLE_VALUE}"}, RemoteEnv: map[string]any{"HOOK_VALUE": "${containerEnv:VALUE}"}, Entrypoint: "echo feature > /tmp/sample-entrypoint", PostCreate: spec.Command{Shell: "printf 'feature:%s:%s:%s\\n' \"$HOOK_VALUE\" \"$(id -u)\" \"$UPDATED\" >> /tmp/sample-hooks"}, PostAttach: spec.Command{Shell: "echo feature-attach >> /tmp/sample-attach"}},
		{PostCreate: spec.Command{Shell: "echo image-config >> /tmp/sample-hooks"}},
	}
	tag := metadataImage(t, ctx, cli, entries)
	compose := fmt.Sprintf(`services:
  app:
    image: %s
    user: root
    volumes: [".:/workspace"]
    environment:
      NATIVE: native
    cap_add: [NET_ADMIN]
    command: ["sh", "-c", "echo native >> /tmp/sample-entrypoint; while sleep 1; do :; done"]
`, tag)
	metadataWrite(t, filepath.Join(workspace, "compose.yml"), compose)
	path := filepath.Join(workspace, "devcontainer.json")
	metadataWrite(t, path, `{"dockerComposeFile":"compose.yml","service":"app","workspaceFolder":"/workspace","containerEnv":{"CONFIG":"config"},"postCreateCommand":"echo current >> /tmp/sample-hooks","postAttachCommand":"echo current-attach >> /tmp/sample-attach"}`)
	t.Cleanup(func() {
		if err := h.Remove(context.Background(), workspace); err != nil {
			t.Log(err)
		}
	})
	up := metadataCLI(t, ctx, "up", "--workspace-folder", workspace, "--config", path, "--skip-post-create")
	id := up["containerId"].(string)
	if up["remoteUser"] != "root" {
		t.Fatalf("result=%v", up)
	}
	inspect, err := cli.ContainerInspect(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	host, cfg := inspect.HostConfig, inspect.Config
	if host.Init == nil || !*host.Init || !host.Privileged || !slices.Contains([]string(host.CapAdd), "CAP_SYS_PTRACE") || !slices.Contains([]string(host.CapAdd), "CAP_NET_ADMIN") || !slices.Contains(host.SecurityOpt, "no-new-privileges") {
		t.Fatalf("runtime host config=%+v", host)
	}
	if cfg.User != "nobody" || !slices.Contains(cfg.Env, "IMAGE=image") || !slices.Contains(cfg.Env, "VALUE=local-value") || !slices.Contains(cfg.Env, "CONFIG=config") || !slices.Contains(cfg.Env, "NATIVE=native") {
		t.Fatalf("runtime config=%+v", cfg)
	}
	mounted := false
	for _, m := range inspect.Mounts {
		if m.Source == workspace && m.Destination == "/sample-source" && !m.RW {
			mounted = true
		}
	}
	if !mounted {
		t.Fatalf("metadata mount missing: %+v", inspect.Mounts)
	}
	// Metadata volumes are project volumes, as in upstream's override file.
	project := cfg.Labels[dockercompose.LabelProject]
	volumeName := ""
	for _, m := range inspect.Mounts {
		if m.Destination == "/sample-cache" {
			volumeName = m.Name
		}
	}
	if volumeName != project+"_sample-cache" {
		t.Fatalf("metadata volume=%q, want %s_sample-cache", volumeName, project)
	}
	if vol, err := cli.VolumeInspect(ctx, volumeName); err != nil || vol.Labels[dockercompose.LabelProject] != project {
		t.Fatalf("metadata volume labels=%v (%v)", vol.Labels, err)
	}
	raw := cfg.Labels[metadata.Label]
	stored, err := metadata.Parse(raw)
	if err != nil || len(stored) != 3 {
		t.Fatalf("complete label=%s (%v)", raw, err)
	}
	if stored[0].ContainerEnv["VALUE"] != "${localEnv:SAMPLE_VALUE}" || len(metadata.Plan(stored)) != 5 {
		t.Fatalf("raw label lost hooks/substitutions: %s", raw)
	}
	exec := func(script string) string {
		t.Helper()
		out, err := h.Exec(ctx, dockercompose.ExecRequest{ContainerID: id, User: "root", Cmd: []string{"sh", "-c", script}})
		if err != nil || out.ExitCode != 0 {
			t.Fatalf("exec %q: %+v %v", script, out, err)
		}
		return string(out.Stdout)
	}
	exec("test ! -e /tmp/sample-hooks; test ! -e /tmp/sample-attach")
	if got := exec("cat /tmp/sample-entrypoint; cat /proc/1/comm; id -u nobody; grep '^NoNewPrivs:' /proc/1/status"); got != "feature\nnative\ndocker-init\n65534\nNoNewPrivs:\t1\n" {
		t.Fatalf("entrypoint/users/init/security=%q", got)
	}
	merged := metadataCLI(t, ctx, "read-configuration", "--workspace-folder", workspace, "--config", path, "--include-merged-configuration")["mergedConfiguration"].(map[string]any)
	if got := merged["postCreateCommands"]; !reflect.DeepEqual(got, []any{entries[0].PostCreate.Shell, "echo image-config >> /tmp/sample-hooks", "echo current >> /tmp/sample-hooks"}) {
		t.Fatalf("merged hooks=%v", got)
	}
	metadataCLI(t, ctx, "run-user-commands", "--workspace-folder", workspace, "--config", path, "--skip-post-attach")
	if got := exec("cat /tmp/sample-hooks; test ! -e /tmp/sample-attach"); got != "feature:local-value:0:\nimage-config\ncurrent\n" {
		t.Fatalf("standalone hooks=%q", got)
	}
	metadataCLI(t, ctx, "run-user-commands", "--workspace-folder", workspace, "--config", path)
	if got := exec("cat /tmp/sample-attach"); got != "feature-attach\ncurrent-attach\n" {
		t.Fatalf("standalone attach=%q", got)
	}
	// A lifecycle-only config change changes the captured label and must recreate
	// the primary under the default config-hash policy.
	originalConfig, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	metadataWrite(t, path, strings.Replace(string(originalConfig), "echo current >> /tmp/sample-hooks", "echo changed >> /tmp/sample-hooks", 1))
	next := metadataCLI(t, ctx, "up", "--workspace-folder", workspace, "--config", path)
	nextID := next["containerId"].(string)
	if nextID == id {
		t.Fatal("hook-only config change reused stale captured metadata")
	}
	id = nextID
	recreated, err := cli.ContainerInspect(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	raw = recreated.Config.Labels[metadata.Label]
	if got := exec("cat /tmp/sample-hooks"); got != "feature:local-value:0:\nimage-config\nchanged\n" {
		t.Fatalf("changed hooks=%q", got)
	}
	// Force a captured container reuse even though current hooks, container env
	// and container user differ. Only upstream updateable remote fields apply.
	current, err := spec.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	current.PostCreate = spec.Command{Shell: "echo forbidden-current >> /tmp/sample-hooks"}
	current.PostAttach = spec.Command{Shell: "echo forbidden-attach >> /tmp/sample-attach"}
	current.ContainerUser = "root"
	current.ContainerEnv = map[string]any{"VALUE": "forbidden"}
	current.UpdateRemoteEnv = map[string]any{"UPDATED": "update-value"}
	noRecreate := dockercompose.New(cli, dockercompose.Options{NoRecreate: true, Logger: t.Logf})
	exec("rm -rf /root/.devcontainer; rm /tmp/sample-hooks /tmp/sample-attach")
	reused, err := noRecreate.Up(ctx, dockercompose.UpRequest{Spec: current, SpecPath: path, LocalWorkspace: workspace, PrimaryImage: "unavailable-sample:unused", LocalEnv: map[string]string{"SAMPLE_VALUE": "later-local"}})
	if err != nil {
		t.Fatal(err)
	}
	if reused.PrimaryContainerID != id || len(reused.CreatedThisCall) != 0 {
		t.Fatalf("reused=%+v", reused)
	}
	if got := exec("cat /tmp/sample-hooks /tmp/sample-attach"); got != "feature:local-value:0:update-value\nimage-config\nchanged\nfeature-attach\ncurrent-attach\n" {
		t.Fatalf("captured hooks=%q", got)
	}
	final, err := cli.ContainerInspect(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if final.Config.Labels[metadata.Label] != raw || final.Config.User != "nobody" {
		t.Fatalf("stored container changed: %+v", final.Config)
	}
}

func TestMetadataDerivedComposeIntegration(t *testing.T) {
	ctx := context.Background()
	cli := metadataDocker(t, ctx)
	h := dockercompose.New(cli, dockercompose.Options{Logger: t.Logf})
	workspace := metadataWorkspace(t)
	metadataWrite(t, filepath.Join(workspace, "compose.yml"), "services:\n  app:\n    image: alpine:3.20\n    volumes: [\".:/workspace\"]\n")
	feature := filepath.Join(workspace, "sample-feature")
	if err := os.Mkdir(feature, 0755); err != nil {
		t.Fatal(err)
	}
	metadataWrite(t, filepath.Join(feature, "devcontainer-feature.json"), `{"id":"sample","version":"1.0.0","containerEnv":{"FEATURE":"from-image"},"postCreateCommand":"echo feature >> /tmp/sample-hooks","entrypoint":"echo entrypoint > /tmp/sample-entrypoint"}`)
	metadataWrite(t, filepath.Join(feature, "install.sh"), "#!/bin/sh\nset -eu\n")
	path := filepath.Join(workspace, "devcontainer.json")
	metadataWrite(t, path, `{"dockerComposeFile":"compose.yml","service":"app","features":{"./sample-feature":{}},"workspaceFolder":"/workspace","postCreateCommand":"echo config >> /tmp/sample-hooks","postAttachCommand":"echo attach >> /tmp/sample-attach"}`)
	tag := fmt.Sprintf("sample-compose-derived:%s", strings.TrimPrefix(filepath.Base(workspace), "."))
	t.Cleanup(func() { _, _ = cli.ImageRemove(context.Background(), tag, imagetypes.RemoveOptions{Force: true}) })
	t.Cleanup(func() {
		if err := h.Remove(context.Background(), workspace); err != nil {
			t.Log(err)
		}
	})
	result := metadataCLI(t, ctx, "up", "--workspace-folder", workspace, "--config", path, "--image-name", tag, "--skip-post-attach")
	id := result["containerId"].(string)
	out, err := h.Exec(ctx, dockercompose.ExecRequest{ContainerID: id, Cmd: []string{"sh", "-c", "cat /tmp/sample-hooks; cat /tmp/sample-entrypoint; echo \"$FEATURE\"; test ! -e /tmp/sample-attach"}})
	if err != nil || out.ExitCode != 0 || string(out.Stdout) != "feature\nconfig\nentrypoint\nfrom-image\n" {
		t.Fatalf("derived runtime=%+v (%v)", out, err)
	}
	inspect, err := cli.ContainerInspect(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := metadata.Parse(inspect.Config.Labels[metadata.Label])
	if err != nil || len(metadata.Plan(entries)) != 3 {
		t.Fatalf("derived label=%s (%v)", inspect.Config.Labels[metadata.Label], err)
	}
}

func metadataDocker(t *testing.T, ctx context.Context) *dockerclient.Client {
	t.Helper()
	if testing.Short() {
		t.Skip("Docker integration")
	}
	configDir := t.TempDir()
	metadataWrite(t, filepath.Join(configDir, "config.json"), "{}\n")
	t.Setenv("DOCKER_CONFIG", configDir)
	cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	if _, err := cli.Ping(ctx); err != nil {
		if os.Getenv("CI") == "true" {
			t.Fatal(err)
		}
		t.Skipf("Docker unavailable: %v", err)
	}
	dockertest.EnsureImage(t, ctx, cli, "alpine:3.20")
	return cli
}
func metadataWorkspace(t *testing.T) string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "../../.."))
	workspace, err := os.MkdirTemp(root, ".tmp-compose-metadata-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	return workspace
}
func metadataImage(t *testing.T, ctx context.Context, cli *dockerclient.Client, entries []metadata.Entry) string {
	t.Helper()
	value, err := metadata.Encode(entries)
	if err != nil {
		t.Fatal(err)
	}
	created, err := cli.ContainerCreate(ctx, &container.Config{Image: "alpine:3.20", Cmd: []string{"true"}}, nil, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cli.ContainerRemove(context.Background(), created.ID, container.RemoveOptions{Force: true, RemoveVolumes: true})
	})
	tag := "sample-compose-metadata:" + created.ID[:12]
	if _, err := cli.ContainerCommit(ctx, created.ID, container.CommitOptions{Reference: tag, Config: &container.Config{Labels: map[string]string{metadata.Label: value}}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = cli.ImageRemove(context.Background(), tag, imagetypes.RemoveOptions{Force: true}) })
	return tag
}
func metadataWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}
func metadataCLI(t *testing.T, ctx context.Context, args ...string) map[string]any {
	t.Helper()
	var out, logs bytes.Buffer
	if code := command.Run(ctx, args, "test", strings.NewReader(""), &out, &logs); code != 0 {
		t.Fatalf("dcb %v exit %d: %s %s", args, code, out.String(), logs.String())
	}
	var result map[string]any
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("output=%s: %v", out.String(), err)
	}
	return result
}
