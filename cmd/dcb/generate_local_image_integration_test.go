//go:build docker_integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"

	"github.com/wyrd-company/devcontainer-builder/internal/dockertest"
)

// TestGenerateRestoresLocalImageUser covers dcb generate on an image that
// only the Docker daemon has: the generated Dockerfile restores the
// image's USER, read through the Engine API. The image is a test-owned
// commit of alpine with USER nobody; no registry has it.
func TestGenerateRestoresLocalImageUser(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Docker integration test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cli := hostRequireDocker(t, ctx)
	t.Cleanup(func() { _ = cli.Close() })

	dockertest.EnsureImage(t, ctx, cli, "alpine:3.20")
	image := fmt.Sprintf("devcontainer-builder/generate-local-user:%d", time.Now().UnixNano())
	created, err := cli.ContainerCreate(ctx, &container.Config{Image: "alpine:3.20", Cmd: []string{"true"}}, nil, nil, nil, "")
	if err != nil {
		t.Fatalf("create container: %v", err)
	}
	t.Cleanup(func() {
		_ = cli.ContainerRemove(context.Background(), created.ID, container.RemoveOptions{Force: true})
	})
	if _, err := cli.ContainerCommit(ctx, created.ID, container.CommitOptions{
		Reference: image,
		Config:    &container.Config{Image: "alpine:3.20", Cmd: []string{"/bin/sh"}, User: "nobody"},
	}); err != nil {
		t.Fatalf("commit %s: %v", image, err)
	}
	t.Cleanup(func() { dockertest.RemoveImage(t, context.Background(), cli, image) })

	workspace := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, ".devcontainer"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".devcontainer", "devcontainer.json"),
		[]byte(fmt.Sprintf(`{"image":%q}`, image)), 0o644); err != nil {
		t.Fatal(err)
	}
	out := hostRunCommand(t, os.Environ(), hostBuildBinary(t), "generate", "--workspace-folder", workspace)
	var result struct {
		Dockerfile string `json:"dockerfile"`
		ContextDir string `json:"contextDir"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("generate result %s: %v", out, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(filepath.Dir(result.Dockerfile)) })
	df, err := os.ReadFile(result.Dockerfile)
	if err != nil {
		t.Fatalf("read generated Dockerfile: %v", err)
	}
	if !strings.Contains(string(df), "USER nobody\n") {
		t.Fatalf("generated Dockerfile does not restore USER nobody:\n%s", df)
	}
}
