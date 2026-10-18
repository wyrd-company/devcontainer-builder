//go:build docker_integration

// relationships:
//
//	references: [CONTRIBUTING]
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	imagetypes "github.com/docker/docker/api/types/image"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
)

func isolateDockerConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_CONFIG", dir)
}

func TestDockerComposeCLI(t *testing.T) {
	cli := hostRequireDocker(t, context.Background())
	t.Cleanup(func() { _ = cli.Close() })
	hostEnsureImage(t, context.Background(), cli, "alpine:3.20")
	workspace := hostPrepareWorkspace(t, "alpine:3.20")
	config := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	body := `{"dockerComposeFile":"compose.yml","service":"app","workspaceFolder":"/workspace","postCreateCommand":"echo ready > /tmp/cli-created"}`
	if err := os.WriteFile(config, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(config), "compose.yml"), []byte("services:\n  app:\n    image: alpine:3.20\n    command: [sleep, infinity]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := hostBuildBinary(t)
	tag := fmt.Sprintf("dcb-test/cli-compose:%d", time.Now().UnixNano())
	alias := tag + "-alias"
	t.Cleanup(func() {
		_, _ = cli.ImageRemove(context.Background(), tag, imagetypes.RemoveOptions{})
		_, _ = cli.ImageRemove(context.Background(), alias, imagetypes.RemoveOptions{})
	})
	output := hostRunCommand(t, os.Environ(), bin, "build", "--workspace-folder", workspace, "--image-name", tag, "--image-name", alias, "--label", "example.test=value")
	var buildResult map[string]any
	if err := json.Unmarshal(output, &buildResult); err != nil {
		t.Fatal(err)
	}
	names := buildResult["imageName"].([]any)
	if len(names) != 2 || names[0] != tag || names[1] != alias {
		t.Fatalf("Compose image names=%v", names)
	}
	for _, name := range []string{tag, alias} {
		image, err := cli.ImageInspect(context.Background(), name)
		if err != nil || image.Config.Labels["example.test"] != "value" {
			t.Fatalf("Compose build image %s: %+v %v", name, image, err)
		}
	}

	cacheTag := tag + "-cache"
	t.Cleanup(func() { _, _ = cli.ImageRemove(context.Background(), cacheTag, imagetypes.RemoveOptions{}) })
	cacheOutput := hostRunCommand(t, os.Environ(), bin, "build", "--workspace-folder", workspace, "--image-name", cacheTag, "--load=false")
	var cacheResult map[string]any
	if err := json.Unmarshal(cacheOutput, &cacheResult); err != nil || cacheResult["cacheOnly"] != true || cacheResult["imageName"] != nil {
		t.Fatalf("Compose cache-only result=%s (%v)", cacheOutput, err)
	}
	assertImageAbsent(t, cacheTag)
	// Register exact workspace teardown before up so partial failures are cleaned.
	t.Cleanup(func() { _ = hostRunCommandAllowError(bin, "down", "--workspace-folder", workspace) })
	up := hostRunCommand(t, os.Environ(), bin, "up", "--workspace-folder", workspace)
	var result map[string]any
	if err := json.Unmarshal(up, &result); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"containerId", "remoteUser", "remoteWorkspaceFolder", "composeProjectName"} {
		if value, ok := result[field].(string); !ok || value == "" {
			t.Fatalf("missing %s: %v", field, result)
		}
	}

	for _, field := range []string{"imageName", "cacheOnly", "outputDir"} {
		if _, exists := result[field]; exists {
			t.Fatalf("up leaked build result field %s: %v", field, result)
		}
	}
	id := result["containerId"].(string)
	t.Cleanup(func() {
		_ = cli.ContainerRemove(context.Background(), id, container.RemoveOptions{Force: true, RemoveVolumes: true})
	})
	out := hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", workspace, "--", "sh", "-c", "cat /tmp/cli-created; pwd")
	if !bytes.Equal(out, []byte("ready\n/workspace\n")) {
		t.Fatalf("compose exec=%q", out)
	}
	ttyOut := hostRunCommand(t, os.Environ(), bin, "exec", "--tty", "--workspace-folder", workspace, "--", "cat", "/tmp/cli-created")
	if !bytes.Contains(ttyOut, []byte("ready")) {
		t.Fatalf("Compose TTY output=%q", ttyOut)
	}

	failed := exec.Command(bin, "exec", "--workspace-folder", workspace, "--", "sh", "-c", "echo diagnostic; exit 7")
	var logs bytes.Buffer
	failed.Stderr = &logs
	output, err := failed.Output()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 7 {
		t.Fatalf("exec failure=%v output=%s logs=%s", err, output, logs.String())
	}
	if err := json.Unmarshal(output, &result); err != nil || result["outcome"] != "error" || result["exitCode"] != float64(7) || bytes.Count(output, []byte("\n")) != 1 {
		t.Fatalf("exec error result=%s (%v)", output, err)
	}
	if !bytes.Contains(logs.Bytes(), []byte("diagnostic")) {
		t.Fatalf("command output missing from stderr: %s", logs.String())
	}
	if err := os.Remove(config); err != nil {
		t.Fatal(err)
	}
	hostRunCommand(t, os.Environ(), bin, "down", "--workspace-folder", workspace)
	if _, err := cli.ContainerInspect(context.Background(), id); err == nil {
		t.Fatal("Compose primary still exists after down without config")
	}

}

func TestDockerLifecycleCLI(t *testing.T) {
	cli := hostRequireDocker(t, context.Background())
	t.Cleanup(func() { _ = cli.Close() })
	hostEnsureImage(t, context.Background(), cli, "alpine:3.20")
	workspace := hostPrepareWorkspace(t, "alpine:3.20")
	config := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	if err := os.WriteFile(config, []byte(`{"image":"alpine:3.20","workspaceFolder":"/workspace","onCreateCommand":"echo create >> /tmp/hooks","postAttachCommand":"echo attach >> /tmp/hooks"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := hostBuildBinary(t)
	t.Cleanup(func() { _ = hostRunCommandAllowError(bin, "down", "--workspace-folder", workspace) })
	output := hostRunCommand(t, os.Environ(), bin, "up", "--workspace-folder", workspace, "--skip-post-create")
	var first map[string]any
	if err := json.Unmarshal(output, &first); err != nil {
		t.Fatal(err)
	}
	hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", workspace, "--", "test", "!", "-e", "/tmp/hooks")
	hostRunCommand(t, os.Environ(), bin, "run-user-commands", "--workspace-folder", workspace, "--skip-post-attach")
	hostRunCommand(t, os.Environ(), bin, "set-up", "--workspace-folder", workspace, "--container-id", first["containerId"].(string), "--skip-post-attach")
	logs := hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", workspace, "--", "cat", "/tmp/hooks")
	if string(logs) != "create\n" {
		t.Fatalf("lifecycle skip/markers: %q", logs)
	}
	output = hostRunCommand(t, os.Environ(), bin, "up", "--workspace-folder", workspace, "--remove-existing-container", "--skip-post-attach")
	var second map[string]any
	if err := json.Unmarshal(output, &second); err != nil {
		t.Fatal(err)
	}
	if first["containerId"] == second["containerId"] {
		t.Fatal("remove-existing-container reused the container")
	}
	logs = hostRunCommand(t, os.Environ(), bin, "exec", "--tty", "--workspace-folder", workspace, "--", "cat", "/tmp/hooks")
	if !bytes.Contains(logs, []byte("create")) {
		t.Fatalf("TTY output=%q", logs)
	}
}
