//go:build docker_integration

package builder

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	imagetypes "github.com/docker/docker/api/types/image"
	dockerclient "github.com/docker/docker/client"
)

// TestEngineBuildIntegration builds the smoke fixtures through the
// Docker daemon's built-in BuildKit with BUILDKIT_HOST unset and checks
// the Engine API lists the loaded image. When BUILDKIT_HOST is set in
// the test environment, the same builds also run against that separate
// BuildKit, which loads through the Engine API image-load endpoint.
func TestEngineBuildIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Docker integration test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	cli := requireEngine(t, ctx)
	separate := os.Getenv("BUILDKIT_HOST")
	t.Setenv("BUILDKIT_HOST", "")

	endpoints := []struct{ name, address string }{{"daemon", ""}}
	if separate != "" {
		endpoints = append(endpoints, struct{ name, address string }{"separate", separate})
	}
	fixtures := []struct {
		name, fixture, path, want string
	}{
		{"dockerfile", "dockerfile", "/usr/local/share/dcb-smoke-dockerfile", "dockerfile-smoke"},
		{"image-feature", "features", "/usr/local/share/dcb-smoke-feature", "feature-smoke"},
	}

	root := repoRoot(t)
	for _, ep := range endpoints {
		for _, fx := range fixtures {
			t.Run(ep.name+"/"+fx.name, func(t *testing.T) {
				tag := fmt.Sprintf("devcontainer-builder/engine-it-%s-%s:%d", ep.name, fx.name, time.Now().UnixNano())
				t.Cleanup(func() {
					if _, err := cli.ImageRemove(context.Background(), tag, imagetypes.RemoveOptions{Force: true}); err != nil && !dockerclient.IsErrNotFound(err) {
						t.Logf("remove %s: %v", tag, err)
					}
				})

				res, err := New().Build(ctx, Options{
					Workspace:       filepath.Join(root, "testdata", "smoke", fx.fixture),
					Tags:            []string{tag},
					Load:            true,
					Labels:          map[string]string{"org.example.dcb-it": fx.name},
					BuildKitAddress: ep.address,
					Docker:          cli,
					Progress:        "plain",
					WorkDir:         t.TempDir(),
					Logger:          t.Logf,
					Stdout:          io.Discard,
					Stderr:          testLogWriter{t},
				})
				if err != nil {
					t.Fatalf("Build: %v", err)
				}
				if fx.fixture == "features" && len(res.Features) != 1 {
					t.Fatalf("resolved %d features, want 1", len(res.Features))
				}

				images, err := cli.ImageList(ctx, imagetypes.ListOptions{Filters: filters.NewArgs(filters.Arg("reference", tag))})
				if err != nil {
					t.Fatalf("list images: %v", err)
				}
				if len(images) != 1 {
					t.Fatalf("Engine API lists %d images for %s, want 1", len(images), tag)
				}
				inspect, err := cli.ImageInspect(ctx, tag)
				if err != nil {
					t.Fatalf("inspect %s: %v", tag, err)
				}
				if got := inspect.Config.Labels["org.example.dcb-it"]; got != fx.name {
					t.Fatalf("label org.example.dcb-it = %q, want %q", got, fx.name)
				}
				if got := readImageFile(t, ctx, cli, tag, fx.path); strings.TrimSpace(got) != fx.want {
					t.Fatalf("%s = %q, want %q", fx.path, got, fx.want)
				}
			})
		}
	}
}

func requireEngine(t *testing.T, ctx context.Context) *dockerclient.Client {
	t.Helper()
	cli, err := dockerclient.NewClientWithOpts(dockerclient.FromEnv, dockerclient.WithAPIVersionNegotiation())
	if err != nil {
		skipOrFail(t, "docker client: %v", err)
	}
	if _, err := cli.Ping(ctx); err != nil {
		_ = cli.Close()
		skipOrFail(t, "docker daemon is required for Docker integration tests: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

func skipOrFail(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("CI") == "true" {
		t.Fatalf(format, args...)
	}
	t.Skipf(format, args...)
}

// readImageFile reads one file from image through a created, never
// started, container.
func readImageFile(t *testing.T, ctx context.Context, cli *dockerclient.Client, image, path string) string {
	t.Helper()
	c, err := cli.ContainerCreate(ctx, &container.Config{Image: image, Cmd: []string{"true"}}, nil, nil, nil, "")
	if err != nil {
		t.Fatalf("create container from %s: %v", image, err)
	}
	defer func() {
		_ = cli.ContainerRemove(context.Background(), c.ID, container.RemoveOptions{Force: true})
	}()
	rc, _, err := cli.CopyFromContainer(ctx, c.ID, path)
	if err != nil {
		t.Fatalf("copy %s from %s: %v", path, image, err)
	}
	defer rc.Close()
	tr := tar.NewReader(rc)
	if _, err := tr.Next(); err != nil {
		t.Fatalf("read %s archive: %v", path, err)
	}
	body, err := io.ReadAll(tr)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// testLogWriter sends BuildKit progress to the test log.
type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
