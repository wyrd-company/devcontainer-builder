//go:build docker_integration

package dockercompose

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wyrd-company/devcontainer-builder/internal/dockertest"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// With overrideCommand false, the primary service keeps its own command
// and still runs the features' entrypoints first.
func TestDockerComposeFeatureEntrypointIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Docker integration test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cli := requireComposeDocker(t, ctx)
	t.Cleanup(func() { _ = cli.Close() })
	dockertest.EnsureImage(t, ctx, cli, "alpine:3.20")

	workspace, err := os.MkdirTemp(repoRoot(t), ".tmp-compose-entrypoint-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	if err := os.MkdirAll(filepath.Join(workspace, ".devcontainer"), 0o755); err != nil {
		t.Fatal(err)
	}
	requireBindableComposePath(t, ctx, cli, "alpine:3.20", workspace)
	writeFile(t, filepath.Join(workspace, ".devcontainer", "docker-compose.yml"), `services:
  app:
    image: alpine:3.20
    command: ["sh", "-c", "echo app-command > /tmp/app-command; while true; do sleep 1; done"]
`)
	writeFile(t, filepath.Join(workspace, ".devcontainer", "devcontainer.json"), `{
  "dockerComposeFile": "docker-compose.yml",
  "service": "app",
  "workspaceFolder": "/workspace",
  "overrideCommand": false
}
`)
	specPath := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
	dc, err := spec.Load(specPath)
	if err != nil {
		t.Fatalf("spec.Load: %v", err)
	}

	h := New(cli, Options{Logger: t.Logf, Stdout: testLogWriter{t: t}, Stderr: testLogWriter{t: t}, StopTimeout: time.Second})
	t.Cleanup(func() {
		if err := h.Remove(context.Background(), workspace); err != nil && !errors.Is(err, ErrProjectNotFound) {
			t.Logf("remove compose project: %v", err)
		}
	})
	res, err := h.Up(ctx, UpRequest{
		Spec:           dc,
		SpecPath:       specPath,
		LocalWorkspace: workspace,
		LocalEnv:       map[string]string{},
		Features: []features.Resolved{{Source: "./marker", Metadata: features.Metadata{
			ID: "marker", Entrypoint: "echo feature > /tmp/feature-entrypoint",
		}}},
	})
	if err != nil {
		t.Fatalf("compose up: %v", err)
	}

	var got string
	for i := 0; i < 50; i++ {
		out := execOK(t, ctx, h, ExecRequest{
			ContainerID: res.PrimaryContainerID,
			Cmd:         []string{"sh", "-c", "cat /tmp/feature-entrypoint /tmp/app-command 2>/dev/null || true"},
		})
		if got = strings.TrimSpace(string(out.Stdout)); got == "feature\napp-command" {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("container state = %q, want feature entrypoint then the service command", got)
}
