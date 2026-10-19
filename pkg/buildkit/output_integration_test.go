//go:build docker_integration

// relationships:
//
//	references: [output, engine_integration_test]
package buildkit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	imagetypes "github.com/docker/docker/api/types/image"
	"github.com/wyrd-company/devcontainer-builder/internal/dockertest"
)

func TestOutputRegistryIntegration(t *testing.T) {
	ctx := context.Background()
	t.Setenv("BUILDKIT_HOST", "")
	cli := requireEngine(t, ctx)
	registry := dockertest.StartRegistry(t, ctx, cli)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine:3.20\nRUN printf exported > /message\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []string{"image", "registry"} {
		t.Run(typ, func(t *testing.T) {
			tag := fmt.Sprintf("%s/sample/export:%s-%d", registry, typ, time.Now().UnixNano())
			t.Cleanup(func() { _, _ = cli.ImageRemove(context.Background(), tag, imagetypes.RemoveOptions{Force: true}) })
			raw := "type=" + typ + ",name=" + tag
			if typ == "image" {
				raw += ",push=true"
			}
			if _, err := SolveDockerfile(ctx, DockerfileBuild{ContextDir: dir}, SolveOptions{Docker: cli, Output: raw, Progress: "plain", Stderr: testLogWriter{t}}); err != nil {
				t.Fatal(err)
			}
			if _, err := cli.ImageRemove(ctx, tag, imagetypes.RemoveOptions{}); err != nil {
				t.Fatal(err)
			}
			dockertest.EnsureImage(t, ctx, cli, tag)
			if got := readImageFile(t, ctx, cli, tag, "/message"); got != "exported" {
				t.Fatalf("pulled content %q", got)
			}
		})
	}
}
