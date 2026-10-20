//go:build docker_integration

// relationships:
//
//	references: [up, ../../../internal/dockertest/authregistry]
package dockercompose

import (
	"context"
	"fmt"
	"io"
	"net/http"
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

func TestComposeMissingPrimaryImageIntegration(t *testing.T) {
	t.Run("registry-metadata", func(t *testing.T) { testComposeMissingPrimary(t, true) })
	t.Run("pull-fallback", func(t *testing.T) { testComposeMissingPrimary(t, false) })
}

func testComposeMissingPrimary(t *testing.T, trustRegistry bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cli := requireComposeDocker(t, ctx)
	t.Cleanup(func() { _ = cli.Close() })
	reg := dockertest.StartAuthRegistry(t, ctx, cli)
	// Trust the fixture so metadata is read from the registry without pulling.
	// Container creation must still load the missing image through the Engine.
	previousTransport := http.DefaultTransport
	if trustRegistry {
		http.DefaultTransport = reg.Client.Transport
	}
	t.Cleanup(func() { http.DefaultTransport = previousTransport })
	dockertest.EnsureImage(t, ctx, cli, "alpine:3.20")
	ref := fmt.Sprintf("%s/dcb-test/compose-pull:%d", reg.Addr, time.Now().UnixNano())
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
	// Remove only this test's unique registry tag; shared alpine stays local.
	if _, err := cli.ImageRemove(ctx, ref, imagetypes.RemoveOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cli.ImageInspect(ctx, ref); !dockerclient.IsErrNotFound(err) {
		t.Fatalf("expected missing primary: %v", err)
	}
	workspace := t.TempDir()
	path := filepath.Join(workspace, "devcontainer.json")
	write := func(image string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(workspace, "compose.yaml"), []byte(fmt.Sprintf("name: sample-pull-%d\nservices:\n  app:\n    image: %s\n    command: [sleep, infinity]\n", os.Getpid(), image)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(ref)
	dc := &spec.DevContainer{Path: path, DockerComposeFile: spec.StringOrSlice{"compose.yaml"}, Service: "app", WorkspaceFolder: "/"}
	h := New(cli, Options{RegistryAuth: auth, Logger: t.Logf})
	t.Cleanup(func() {
		if err := h.Remove(context.Background(), workspace); err != nil {
			t.Log(err)
		}
	})
	req := UpRequest{Spec: dc, SpecPath: path, LocalWorkspace: workspace, SkipLifecycle: true}
	result, err := h.Up(ctx, req)
	if err != nil {
		t.Fatalf("up with missing primary: %v", err)
	}
	if _, err := cli.ImageInspect(ctx, ref); err != nil {
		t.Fatalf("primary was not pulled: %v", err)
	}
	state, err := cli.ContainerInspect(ctx, result.PrimaryContainerID)
	if err != nil || !state.State.Running {
		t.Fatalf("primary not running: %+v, %v", state.State, err)
	}
	write("registry.invalid/sample/unavailable:latest")
	h.opts.NoRecreate = true
	again, err := h.Up(ctx, req)
	if err != nil {
		t.Fatalf("no-recreate should not inspect replacement image: %v", err)
	}
	if again.PrimaryContainerID != result.PrimaryContainerID {
		t.Fatal("no-recreate replaced primary")
	}
}
