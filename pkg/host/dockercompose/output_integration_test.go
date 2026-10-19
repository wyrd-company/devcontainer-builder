//go:build docker_integration

// relationships:
//
//	references: [build, ../../buildkit/output]
package dockercompose

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	imagetypes "github.com/docker/docker/api/types/image"
	dockerclient "github.com/docker/docker/client"
	"github.com/wyrd-company/devcontainer-builder/pkg/builder"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestComposeExporterIntegration(t *testing.T) {
	ctx := context.Background()
	t.Setenv("BUILDKIT_HOST", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  app:\n    image: alpine:3.20\n"), 0600); err != nil {
		t.Fatal(err)
	}
	dc := &spec.DevContainer{Path: filepath.Join(dir, "devcontainer.json"), DockerComposeFile: spec.StringOrSlice{"compose.yaml"}, Service: "app", WorkspaceFolder: "/workspace"}
	cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	tag := fmt.Sprintf("sample/compose-export:%d", time.Now().UnixNano())
	t.Cleanup(func() { _, _ = cli.ImageRemove(context.Background(), tag, imagetypes.RemoveOptions{Force: true}) })
	dest := filepath.Join(dir, "result")
	_, err = BuildPrimaryImage(ctx, BuildPrimaryInputs{Spec: dc, Workspace: dir, Tag: tag, BuilderOptions: builder.Options{Docker: cli, Output: "type=local,dest=" + dest, SkipFeatures: true, Stdout: io.Discard, Stderr: io.Discard}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dest, "etc", "alpine-release")); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.ImageInspect(ctx, tag); !dockerclient.IsErrNotFound(err) {
		t.Fatalf("export unexpectedly loaded into Docker: %v", err)
	}
}
