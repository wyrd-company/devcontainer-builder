//go:build docker_integration

package buildkit

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	imagetypes "github.com/docker/docker/api/types/image"

	"github.com/wyrd-company/devcontainer-builder/internal/dockertest"
	"github.com/wyrd-company/devcontainer-builder/pkg/registryauth"
)

// TestDaemonPrivateRegistryIntegration pushes from the daemon's BuildKit
// to a test-owned registry that requires credentials, then builds from
// the pushed image, so that the push sends the Engine API credentials
// and the build session answers BuildKit's credential requests. Without
// credentials both are refused.
func TestDaemonPrivateRegistryIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Docker integration test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	t.Setenv("BUILDKIT_HOST", "")
	cli := requireEngine(t, ctx)
	reg := dockertest.StartAuthRegistry(t, ctx, cli)

	stamp := time.Now().UnixNano()
	base := fmt.Sprintf("%s/dcb-test/private-base:%d", reg.Addr, stamp)
	derived := fmt.Sprintf("devcontainer-builder/buildkit-it-private:%d", stamp)
	for _, ref := range []string{base, derived} {
		ref := ref
		t.Cleanup(func() { dockertest.RemoveImage(t, context.Background(), cli, ref) })
	}
	anonymous := registryauth.New(dockertest.DockerConfig(t, `{}`))

	baseDir := t.TempDir()
	writeTestFile(t, filepath.Join(baseDir, "Dockerfile"), fmt.Sprintf("FROM alpine:3.20\nRUN printf %d > /private\n", stamp))
	push := func(auth RegistryAuth) error {
		_, err := SolveDockerfile(ctx, DockerfileBuild{ContextDir: baseDir}, SolveOptions{
			Docker: cli, Tags: []string{base}, Push: true, RegistryAuth: auth, Progress: "plain", Stderr: testLogWriter{t},
		})
		return err
	}
	if err := push(anonymous); err == nil || !strings.Contains(err.Error(), "no basic auth credentials") {
		t.Fatalf("push without credentials: err = %v, want no basic auth credentials", err)
	}
	staticDir := reg.StaticAuthConfig(t)
	if err := push(registryauth.New(staticDir)); err != nil {
		t.Fatalf("push with static auths: %v", err)
	}
	if _, err := cli.ImageRemove(ctx, base, imagetypes.RemoveOptions{}); err != nil {
		t.Fatalf("remove local %s: %v", base, err)
	}

	derivedDir := t.TempDir()
	writeTestFile(t, filepath.Join(derivedDir, "Dockerfile"), "FROM "+base+"\nRUN cp /private /derived\n")
	build := func(auth RegistryAuth) error {
		_, err := SolveDockerfile(ctx, DockerfileBuild{ContextDir: derivedDir}, SolveOptions{
			Docker: cli, Tags: []string{derived}, Load: true, Pull: true, RegistryAuth: auth, Progress: "plain", Stderr: testLogWriter{t},
		})
		return err
	}
	if err := build(anonymous); err == nil || !strings.Contains(err.Error(), "401 Unauthorized") {
		t.Fatalf("build from the private image without credentials: err = %v, want 401 Unauthorized", err)
	}
	helperDir, log := reg.HelperConfig(t, "dcbtest")
	if err := build(registryauth.New(helperDir)); err != nil {
		t.Fatalf("build from the private image with a credential helper: %v", err)
	}
	if got := readImageFile(t, ctx, cli, derived, "/derived"); got != fmt.Sprint(stamp) {
		t.Fatalf("/derived = %q, want %d", got, stamp)
	}
	if b, err := os.ReadFile(log); err != nil || !strings.Contains(string(b), reg.Addr) {
		t.Fatalf("credential helper was not asked for %s: %q, %v", reg.Addr, b, err)
	}
	// The push and the build leave the Docker configuration directories
	// unchanged.
	for _, dir := range []string{staticDir, helperDir} {
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 1 || entries[0].Name() != "config.json" {
			t.Fatalf("Docker config directory %s holds %v (%v), want only config.json", dir, entries, err)
		}
	}
}
