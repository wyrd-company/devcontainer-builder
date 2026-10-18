//go:build docker_integration

package buildkit

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
	imagetypes "github.com/docker/docker/api/types/image"
	dockerclient "github.com/docker/docker/client"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// TestDaemonBuildKitIntegration builds on the Docker daemon's BuildKit
// with BUILDKIT_HOST unset: a Dockerfile through SolveDockerfile, then a
// second Dockerfile and a devcontainer graph on top of that image, which
// exists only in the daemon's image store.
func TestDaemonBuildKitIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Docker integration test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	t.Setenv("BUILDKIT_HOST", "")
	cli := requireEngine(t, ctx)

	stamp := time.Now().UnixNano()
	baseTag := fmt.Sprintf("devcontainer-builder/buildkit-it-base:%d", stamp)
	childTag := fmt.Sprintf("devcontainer-builder/buildkit-it-child:%d", stamp)
	graphTag := fmt.Sprintf("devcontainer-builder/buildkit-it-graph:%d", stamp)
	for _, tag := range []string{graphTag, childTag, baseTag} {
		tag := tag
		t.Cleanup(func() {
			if _, err := cli.ImageRemove(context.Background(), tag, imagetypes.RemoveOptions{Force: true}); err != nil && !dockerclient.IsErrNotFound(err) {
				t.Logf("remove %s: %v", tag, err)
			}
		})
	}
	opt := func(tag string) SolveOptions {
		return SolveOptions{Docker: cli, Tags: []string{tag}, Load: true, Progress: "plain", Stderr: testLogWriter{t}}
	}

	baseDir := t.TempDir()
	writeTestFile(t, filepath.Join(baseDir, "docker", "base.Dockerfile"), `FROM alpine:3.20 AS unused
RUN false
FROM alpine:3.20 AS base
ARG GREETING=unset
RUN printf '%s' "$GREETING" > /greeting
`)
	if _, err := SolveDockerfile(ctx, DockerfileBuild{
		ContextDir:    baseDir,
		DockerfileDir: filepath.Join(baseDir, "docker"),
		Filename:      "base.Dockerfile",
		Target:        "base",
		BuildArgs:     map[string]string{"GREETING": "hello"},
		Labels:        map[string]string{"org.example.role": "base"},
	}, opt(baseTag)); err != nil {
		t.Fatalf("SolveDockerfile base: %v", err)
	}
	inspect, err := cli.ImageInspect(ctx, baseTag)
	if err != nil {
		t.Fatalf("inspect %s: %v", baseTag, err)
	}
	if got := inspect.Config.Labels["org.example.role"]; got != "base" {
		t.Fatalf("base label = %q, want base", got)
	}
	if got := readImageFile(t, ctx, cli, baseTag, "/greeting"); got != "hello" {
		t.Fatalf("/greeting = %q, want hello (build arg)", got)
	}

	childDir := t.TempDir()
	writeTestFile(t, filepath.Join(childDir, "Dockerfile"), "FROM "+baseTag+"\nRUN cp /greeting /child\n")
	if _, err := SolveDockerfile(ctx, DockerfileBuild{ContextDir: childDir}, opt(childTag)); err != nil {
		t.Fatalf("SolveDockerfile FROM a local-only image: %v", err)
	}
	if got := readImageFile(t, ctx, cli, childTag, "/child"); got != "hello" {
		t.Fatalf("/child = %q, want hello", got)
	}

	if _, err := SolveDevcontainer(ctx, BuildSpec{Graph: GraphInputs{
		Spec:               &spec.DevContainer{Image: baseTag, WorkspaceFolder: "/workspaces/it"},
		BaseImage:          baseTag,
		ResolveImageConfig: true,
	}}, opt(graphTag)); err != nil {
		t.Fatalf("SolveDevcontainer on a local-only image: %v", err)
	}
	if got := readImageFile(t, ctx, cli, graphTag, "/greeting"); got != "hello" {
		t.Fatalf("graph /greeting = %q, want hello", got)
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

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
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

// testLogWriter sends BuildKit progress to the test log.
type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
