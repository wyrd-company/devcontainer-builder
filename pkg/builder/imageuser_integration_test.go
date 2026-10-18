//go:build docker_integration

// relationships:
//   references: [imageuser, imageuser_test, metadata]

package builder

import (
	"context"
	"fmt"
	"testing"

	"github.com/docker/docker/api/types/container"

	"github.com/wyrd-company/devcontainer-builder/internal/dockertest"
)

func TestGeneratedDockerfilesLocalUserIntegration(t *testing.T) {
	ctx := context.Background()
	cli := requireEngine(t, ctx)
	dockertest.EnsureImage(t, ctx, cli, "alpine:3.20")
	created, err := cli.ContainerCreate(ctx, &container.Config{Image: "alpine:3.20", Cmd: []string{"true"}}, nil, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.ContainerRemove(ctx, created.ID, container.RemoveOptions{Force: true}) })
	// The full container ID gives the test-owned tag a unique name.
	image := "devcontainer-builder/local-user:" + created.ID
	if _, err := cli.ContainerCommit(ctx, created.ID, container.CommitOptions{
		Reference: image,
		Config:    &container.Config{Image: "alpine:3.20", User: "nobody"},
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dockertest.RemoveImage(t, ctx, cli, image) })
	for _, prepare := range []bool{false, true} {
		for _, skipFeatures := range []bool{false, true} {
			t.Run(fmt.Sprintf("prepare=%v/skip-features=%v", prepare, skipFeatures), func(t *testing.T) {
				ws := writeWorkspace(t, `"image":"`+image+`"`, nil)
				df, _ := generatedUserDockerfile(t, New(), Options{
					Workspace: ws, WorkDir: t.TempDir(), SkipFeatures: skipFeatures,
				}, prepare)
				wantFinalUser(t, df, "nobody")
				if !skipFeatures {
					wantBuiltinUser(t, df, "nobody")
				}
			})
		}
	}
}
