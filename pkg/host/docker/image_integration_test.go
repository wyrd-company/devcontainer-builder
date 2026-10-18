//go:build docker_integration

package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	imagetypes "github.com/docker/docker/api/types/image"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"

	"github.com/wyrd-company/devcontainer-builder/internal/dockertest"
	"github.com/wyrd-company/devcontainer-builder/pkg/registryauth"
)

// TestEnsureImagePrivateRegistryIntegration pulls a missing image from a
// test-owned registry that requires credentials: refused without them,
// pulled with them.
func TestEnsureImagePrivateRegistryIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Docker integration test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		t.Fatalf("docker client: %v", err)
	}
	defer cli.Close()
	if _, err := cli.Ping(ctx); err != nil {
		if os.Getenv("CI") == "true" {
			t.Fatalf("docker daemon: %v", err)
		}
		t.Skipf("docker daemon is required for Docker integration tests: %v", err)
	}
	reg := dockertest.StartAuthRegistry(t, ctx, cli)
	dockertest.EnsureImage(t, ctx, cli, "alpine:3.20")
	ref := fmt.Sprintf("%s/dcb-test/private-pull:%d", reg.Addr, time.Now().UnixNano())
	t.Cleanup(func() { dockertest.RemoveImage(t, context.Background(), cli, ref) })
	auth := registryauth.New(reg.StaticAuthConfig(t))

	if err := cli.ImageTag(ctx, "alpine:3.20", ref); err != nil {
		t.Fatalf("tag %s: %v", ref, err)
	}
	encoded, err := auth.EngineAuth(ref)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := cli.ImagePush(ctx, ref, imagetypes.PushOptions{RegistryAuth: encoded})
	if err != nil {
		t.Fatalf("push %s: %v", ref, err)
	}
	err = jsonmessage.DisplayJSONMessagesStream(rc, io.Discard, 0, false, nil)
	rc.Close()
	if err != nil {
		t.Fatalf("push %s: %v", ref, err)
	}
	if _, err := cli.ImageRemove(ctx, ref, imagetypes.RemoveOptions{}); err != nil {
		t.Fatalf("remove local %s: %v", ref, err)
	}

	anonymous := New(cli, Options{RegistryAuth: registryauth.New(dockertest.DockerConfig(t, `{}`))})
	if err := anonymous.ensureImage(ctx, ref); err == nil || !strings.Contains(err.Error(), "no basic auth credentials") {
		t.Fatalf("pull without credentials: err = %v, want no basic auth credentials", err)
	}
	if err := New(cli, Options{RegistryAuth: auth}).ensureImage(ctx, ref); err != nil {
		t.Fatalf("pull with credentials: %v", err)
	}
	if _, err := cli.ImageInspect(ctx, ref); err != nil {
		t.Fatalf("pulled image %s not in the daemon: %v", ref, err)
	}
}
