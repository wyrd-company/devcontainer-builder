// Package dockertest holds helpers for Docker integration tests. Every
// helper works through the Engine API of the daemon under test, so the
// tests run the same against any daemon DOCKER_HOST names.
package dockertest

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	imagetypes "github.com/docker/docker/api/types/image"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
	"github.com/docker/go-connections/nat"
)

// RegistryImage is the registry the tests run.
const RegistryImage = "registry:2"

// EnsureImage pulls ref when the daemon does not have it. It never
// removes an image.
func EnsureImage(t testing.TB, ctx context.Context, cli *dockerclient.Client, ref string) {
	t.Helper()
	if _, err := cli.ImageInspect(ctx, ref); err == nil {
		return
	} else if !dockerclient.IsErrNotFound(err) {
		t.Fatalf("inspect image %s: %v", ref, err)
	}
	rc, err := cli.ImagePull(ctx, ref, imagetypes.PullOptions{})
	if err != nil {
		t.Fatalf("pull %s: %v", ref, err)
	}
	defer rc.Close()
	if err := jsonmessage.DisplayJSONMessagesStream(rc, io.Discard, 0, false, nil); err != nil {
		t.Fatalf("pull %s: %v", ref, err)
	}
}

// StartRegistry runs a test-owned registry container on the daemon and
// returns its address as the daemon reaches it ("127.0.0.1:<port>",
// which the daemon treats as an insecure registry). The container is
// removed when the test ends.
func StartRegistry(t testing.TB, ctx context.Context, cli *dockerclient.Client) string {
	t.Helper()
	EnsureImage(t, ctx, cli, RegistryImage)
	name := fmt.Sprintf("dcb-test-registry-%d", time.Now().UnixNano())
	resp, err := cli.ContainerCreate(ctx,
		&container.Config{Image: RegistryImage, ExposedPorts: nat.PortSet{"5000/tcp": {}}},
		&container.HostConfig{PortBindings: nat.PortMap{"5000/tcp": {{HostIP: "127.0.0.1"}}}},
		nil, nil, name)
	if err != nil {
		t.Fatalf("create registry: %v", err)
	}
	t.Cleanup(func() {
		if err := cli.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true, RemoveVolumes: true}); err != nil {
			t.Logf("remove registry %s: %v", name, err)
		}
	})
	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		t.Fatalf("start registry: %v", err)
	}
	inspect, err := cli.ContainerInspect(ctx, resp.ID)
	if err != nil {
		t.Fatalf("inspect registry: %v", err)
	}
	bindings := inspect.NetworkSettings.Ports["5000/tcp"]
	if len(bindings) == 0 || bindings[0].HostPort == "" {
		t.Fatalf("registry port not published: %+v", inspect.NetworkSettings.Ports)
	}
	waitForLog(t, ctx, cli, resp.ID, "listening on")
	return "127.0.0.1:" + bindings[0].HostPort
}

// waitForLog follows the container log until a line contains want or
// ctx ends.
func waitForLog(t testing.TB, ctx context.Context, cli *dockerclient.Client, id, want string) {
	t.Helper()
	rc, err := cli.ContainerLogs(ctx, id, container.LogsOptions{ShowStdout: true, ShowStderr: true, Follow: true})
	if err != nil {
		t.Fatalf("registry logs: %v", err)
	}
	defer rc.Close()
	sc := bufio.NewScanner(rc)
	for sc.Scan() {
		if strings.Contains(sc.Text(), want) {
			return
		}
	}
	t.Fatalf("registry never logged %q: %v", want, errOrEOF(sc.Err(), ctx.Err()))
}

func errOrEOF(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return io.EOF
}
