//go:build docker_integration

// relationships:
//
//	references: [metadata, config, lifecycle, host]
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

	"github.com/docker/docker/api/types/container"
	imagetypes "github.com/docker/docker/api/types/image"

	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
)

func TestMetadataPrebuiltCLIIntegration(t *testing.T) {
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_CONFIG", configDir)
	t.Setenv("SAMPLE_REMOTE_USER", "sample-user")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cli := hostRequireDocker(t, ctx)
	t.Cleanup(func() { _ = cli.Close() })
	hostEnsureImage(t, ctx, cli, "alpine:3.20")
	bin := hostBuildBinary(t)
	workspace := hostPrepareWorkspace(t, "alpine:3.20")
	path := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	featureDir := filepath.Join(workspace, ".devcontainer", "sample-feature")
	if err := os.Mkdir(featureDir, 0755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"devcontainer-feature.json": `{"id":"sample","version":"1.0.0","capAdd":["SYS_PTRACE"],"customizations":{"editor":{"sample":true}},"postCreateCommand":"printf 'feature\\n' >> /tmp/sample-hooks; printf '%s\\n' \"$SAMPLE_ID\" > /tmp/sample-id","postAttachCommand":"echo attach >> /tmp/sample-attach"}`,
		"install.sh":                "#!/bin/sh\nset -eu\nadduser -D sample-user\n",
	}
	for name, value := range files {
		if err := os.WriteFile(filepath.Join(featureDir, name), []byte(value), 0755); err != nil {
			t.Fatal(err)
		}
	}
	initial := `{"image":"alpine:3.20","features":{"./sample-feature":{}},"remoteUser":"${localEnv:SAMPLE_REMOTE_USER}","containerUser":"root","containerEnv":{"FEATURE_VALUE":"image-env"},"workspaceFolder":"/workspace","workspaceMount":"type=volume,target=/workspace","remoteEnv":{"SAMPLE_ID":"${devcontainerId}","SAMPLE_ENV":"${containerEnv:FEATURE_VALUE}"},"postCreateCommand":"echo config >> /tmp/sample-hooks"}`
	if err := os.WriteFile(path, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}
	tag := fmt.Sprintf("dcb-cli-metadata-test:%d", time.Now().UnixNano())
	t.Cleanup(func() { _, _ = cli.ImageRemove(context.Background(), tag, imagetypes.RemoveOptions{Force: true}) })
	// The workspace is unique to this test; cleanup verifies the discovery label
	// before removing each exact daemon-provided container ID.
	t.Cleanup(func() {
		list, err := cli.ContainerList(context.Background(), hostFiltersForWorkspace(workspace))
		if err != nil {
			t.Log(err)
			return
		}
		for _, item := range list {
			if item.Labels["devcontainer.local_folder"] == workspace {
				_ = cli.ContainerRemove(context.Background(), item.ID, container.RemoveOptions{Force: true, RemoveVolumes: true})
			}
		}
	})
	hostRunCommand(t, os.Environ(), bin, "up", "--workspace-folder", workspace, "--image-name", tag, "--update-remote-user-uid-default", "off")
	if got := string(hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", workspace, "--", "cat", "/tmp/sample-hooks")); got != "feature\nconfig\n" {
		t.Fatalf("fresh build duplicated hooks=%q", got)
	}
	hostRunCommand(t, os.Environ(), bin, "down", "--workspace-folder", workspace)
	up := hostRunCommand(t, os.Environ(), bin, "up", "--workspace-folder", workspace, "--image-name", tag, "--skip-post-create", "--update-remote-user-uid-default", "off")
	var response map[string]any
	if err := json.Unmarshal(up, &response); err != nil {
		t.Fatal(err)
	}
	if response["remoteUser"] != "sample-user" {
		t.Fatalf("up response=%v", response)
	}
	hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", workspace, "--", "sh", "-c", "test ! -e /tmp/sample-hooks; test ! -e /tmp/sample-attach")
	hostRunCommand(t, os.Environ(), bin, "run-user-commands", "--workspace-folder", workspace)
	if got := string(hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", workspace, "--", "cat", "/tmp/sample-hooks")); got != "feature\nconfig\n" {
		t.Fatalf("current-spec hooks=%q", got)
	}
	img, err := cli.ImageInspect(ctx, tag)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := metadata.Parse(img.Config.Labels[metadata.Label])
	if err != nil || len(entries) != 2 {
		t.Fatalf("image metadata=%+v err=%v", entries, err)
	}
	hostRunCommand(t, os.Environ(), bin, "down", "--workspace-folder", workspace)
	// Consume only the prebuilt image: current config has no features, hooks or user.
	raw, _ := json.Marshal(map[string]any{"image": tag, "workspaceFolder": "/workspace", "workspaceMount": "type=volume,target=/workspace"})
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	up = hostRunCommand(t, os.Environ(), bin, "up", "--workspace-folder", workspace, "--skip-post-attach", "--update-remote-user-uid-default", "off")
	if err := json.Unmarshal(up, &response); err != nil {
		t.Fatal(err)
	}
	id := response["containerId"].(string)
	if response["remoteUser"] != "sample-user" {
		t.Fatalf("prebuilt up response=%v", response)
	}
	got := string(hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", workspace, "--", "sh", "-c", "id -un; cat /tmp/sample-hooks; test ! -e /tmp/sample-attach; printf '%s\\n' \"$SAMPLE_ENV\"; printf '%s\\n' \"$SAMPLE_ID\"; cat /tmp/sample-id"))
	lines := strings.Split(strings.TrimSpace(got), "\n")
	if len(lines) != 6 || lines[0] != "sample-user" || lines[1] != "feature" || lines[2] != "config" || lines[3] != "image-env" || lines[4] == "" || lines[4] != lines[5] {
		t.Fatalf("prebuilt exec/user/hooks/identity=%q", got)
	}
	for _, selection := range [][]string{nil, {"--container-id", id}} {
		args := []string{"read-configuration", "--workspace-folder", workspace, "--include-merged-configuration"}
		args = append(args, selection...)
		output := hostRunCommand(t, os.Environ(), bin, args...)
		var read map[string]any
		if err := json.Unmarshal(output, &read); err != nil {
			t.Fatal(err)
		}
		merged := read["mergedConfiguration"].(map[string]any)
		if merged["remoteUser"] != "sample-user" || len(merged["postCreateCommands"].([]any)) != 2 || len(merged["capAdd"].([]any)) != 1 || len(merged["customizations"].(map[string]any)["editor"].([]any)) != 1 {
			t.Fatalf("merged configuration=%v", merged)
		}
	}
	hostRunCommand(t, os.Environ(), bin, "run-user-commands", "--workspace-folder", workspace, "--skip-post-create")
	hostRunCommand(t, os.Environ(), bin, "set-up", "--workspace-folder", workspace, "--container-id", id, "--skip-post-attach")
	hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", workspace, "--", "sh", "-c", "test ! -e /tmp/sample-attach")
	hostRunCommand(t, os.Environ(), bin, "set-up", "--workspace-folder", workspace, "--container-id", id)
	if got := string(hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", workspace, "--", "sh", "-c", "cat /tmp/sample-hooks; cat /tmp/sample-attach")); got != "feature\nconfig\nattach\n" {
		t.Fatalf("standalone persisted hooks=%q", got)
	}
	// Compose keeps image metadata separate from the current config. Preserve
	// the current hooks when standalone lifecycle reads that image label.
	hostRunCommand(t, os.Environ(), bin, "down", "--workspace-folder", workspace)
	composePath := filepath.Join(workspace, ".devcontainer", "compose.yml")
	compose := fmt.Sprintf("services:\n  app:\n    image: %s\n    user: root\n    command: [sleep, infinity]\n", tag)
	if err := os.WriteFile(composePath, []byte(compose), 0644); err != nil {
		t.Fatal(err)
	}
	composeConfig := `{"dockerComposeFile":"compose.yml","service":"app","remoteUser":"sample-user","workspaceFolder":"/workspace","workspaceMount":"type=volume,target=/workspace","postAttachCommand":"echo current >> /tmp/compose-current-attach"}`
	if err := os.WriteFile(path, []byte(composeConfig), 0644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd := exec.Command(bin, "down", "--workspace-folder", workspace)
		cmd.Env = os.Environ()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Logf("cleanup Compose: %v %s", err, out)
		}
	})
	composeUp := hostRunCommand(t, os.Environ(), bin, "up", "--workspace-folder", workspace, "--update-remote-user-uid-default", "never")
	var composeResponse map[string]any
	if err := json.Unmarshal(composeUp, &composeResponse); err != nil {
		t.Fatal(err)
	}
	composeID := composeResponse["containerId"].(string)
	hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", workspace, "--container-id", composeID, "--", "rm", "/tmp/compose-current-attach")
	hostRunCommand(t, os.Environ(), bin, "run-user-commands", "--workspace-folder", workspace, "--container-id", composeID)
	if got := string(hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", workspace, "--container-id", composeID, "--", "cat", "/tmp/compose-current-attach")); got != "current\n" {
		t.Fatalf("current Compose hook=%q", got)
	}
	output := hostRunCommand(t, os.Environ(), bin, "read-configuration", "--workspace-folder", workspace, "--include-merged-configuration", "--container-id", composeID)
	var read map[string]any
	if err := json.Unmarshal(output, &read); err != nil {
		t.Fatal(err)
	}
	if hooks := read["mergedConfiguration"].(map[string]any)["postAttachCommands"].([]any); len(hooks) != 2 {
		t.Fatalf("Compose merged hooks=%v", hooks)
	}

}
