//go:build docker_integration

// relationships:
//
//	references: [up, ../../../internal/dockertest/authregistry]
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
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/wyrd-company/devcontainer-builder/internal/dockertest"
	"github.com/wyrd-company/devcontainer-builder/pkg/registryauth"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestComposeMissingServiceImageIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cli := requireComposeDocker(t, ctx)
	t.Cleanup(func() { _ = cli.Close() })
	reg := dockertest.StartAuthRegistry(t, ctx, cli)
	dockertest.EnsureImage(t, ctx, cli, "alpine:3.20")
	ref := fmt.Sprintf("%s/dcb-test/compose-service:%d", reg.Addr, time.Now().UnixNano())
	t.Cleanup(func() { dockertest.RemoveImage(t, context.Background(), cli, ref) })
	auth := registryauth.New(reg.StaticAuthConfig(t))
	if err := cli.ImageTag(ctx, "alpine:3.20", ref); err != nil {
		t.Fatal(err)
	}
	encoded, err := auth.EngineAuth(ref)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := cli.ImagePush(ctx, ref, imagetypes.PushOptions{RegistryAuth: encoded})
	if err != nil {
		t.Fatal(err)
	}
	err = jsonmessage.DisplayJSONMessagesStream(rc, io.Discard, 0, false, nil)
	rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Remove only the unique tag owned by this test, never the shared source image.
	if _, err := cli.ImageRemove(ctx, ref, imagetypes.RemoveOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.ImageInspect(ctx, ref); !dockerclient.IsErrNotFound(err) {
		t.Fatalf("expected missing sidecar image: %v", err)
	}
	workspace := t.TempDir()
	path := filepath.Join(workspace, "devcontainer.json")
	compose := fmt.Sprintf(`name: sample-service-pull-%d
services:
  app:
    image: alpine:3.20
    depends_on: [sidecar]
    command: [sleep, infinity]
  sidecar:
    image: %s
    command: [sleep, infinity]
  optional:
    image: %s-disabled
    profiles: [optional]
    command: [sleep, infinity]
`, os.Getpid(), ref, ref)
	if err := os.WriteFile(filepath.Join(workspace, "compose.yaml"), []byte(compose), 0600); err != nil {
		t.Fatal(err)
	}
	dc := &spec.DevContainer{Path: path, DockerComposeFile: spec.StringOrSlice{"compose.yaml"}, Service: "app", WorkspaceFolder: "/"}
	h := New(cli, Options{RegistryAuth: auth, Logger: t.Logf})
	project := fmt.Sprintf("sample-service-pull-%d", os.Getpid())
	// Remove by project so cleanup also covers failure before primary creation.
	t.Cleanup(func() {
		if err := h.RemoveProject(context.Background(), project, workspace); err != nil {
			t.Log(err)
		}
	})
	result, err := h.Up(ctx, UpRequest{Spec: dc, SpecPath: path, LocalWorkspace: workspace, SkipLifecycle: true})
	if err != nil {
		t.Fatalf("up with missing sidecar: %v", err)
	}
	if _, err := cli.ImageInspect(ctx, ref); err != nil {
		t.Fatalf("sidecar image was not pulled: %v", err)
	}
	for _, name := range []string{"app", "sidecar"} {
		state, err := cli.ContainerInspect(ctx, result.ContainerIDs[name])
		if err != nil || state.State == nil || !state.State.Running {
			t.Fatalf("%s not running: %+v, %v", name, state.State, err)
		}
	}
	if _, ok := result.ContainerIDs["optional"]; ok {
		t.Fatal("profile-disabled service was started")
	}
	if _, err := cli.ImageInspect(ctx, ref+"-disabled"); !dockerclient.IsErrNotFound(err) {
		t.Fatalf("profile-disabled image was pulled: %v", err)
	}
}
