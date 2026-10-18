//go:build docker_integration

package features

import (
	"archive/tar"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dockerclient "github.com/docker/docker/client"

	"github.com/wyrd-company/devcontainer-builder/internal/dockertest"
)

// TestPrivateFeatureIntegration resolves a feature from a test-owned
// registry that requires credentials, with the credentials of a static
// auths entry and of a credential helper, and checks that anonymous
// access is refused.
func TestPrivateFeatureIntegration(t *testing.T) {
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
	layer := tarGz(t, []tar.Header{
		{Name: "./", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "./devcontainer-feature.json", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "./install.sh", Typeflag: tar.TypeReg, Mode: 0o755},
	}, map[string]string{
		"./devcontainer-feature.json": `{"id":"marker","version":"1.0.0"}`,
		"./install.sh":                "#!/bin/sh\n",
	})
	reg.PushArtifact(t, "team/features/marker", "1", "application/vnd.devcontainers", []byte("{}"),
		"application/vnd.devcontainers.layer.v1+tar", layer)
	source := reg.Addr + "/team/features/marker:1"

	resolve := func(t *testing.T, dockerConfig string) ([]Resolved, error) {
		t.Setenv("DOCKER_CONFIG", dockerConfig)
		r := New()
		r.OCI.HTTP = reg.Client
		return r.Resolve(userFeatures(source, `{}`), nil, filepath.Join(t.TempDir(), "stage"), t.TempDir())
	}
	staged := func(t *testing.T, got []Resolved) {
		t.Helper()
		if len(got) != 1 || got[0].Metadata.ID != "marker" {
			t.Fatalf("resolved %v, want the marker feature", ids(got))
		}
		if _, err := os.Stat(filepath.Join(got[0].Dir, "install.sh")); err != nil {
			t.Fatalf("install.sh not staged: %v", err)
		}
	}

	t.Run("static auths", func(t *testing.T) {
		got, err := resolve(t, reg.StaticAuthConfig(t))
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		staged(t, got)
	})
	t.Run("credential helper", func(t *testing.T) {
		dir, log := reg.HelperConfig(t, "dcbtest")
		got, err := resolve(t, dir)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}
		staged(t, got)
		if b, err := os.ReadFile(log); err != nil || !strings.Contains(string(b), reg.Addr) {
			t.Fatalf("credential helper was not asked for %s: %q, %v", reg.Addr, b, err)
		}
	})
	t.Run("anonymous is refused", func(t *testing.T) {
		_, err := resolve(t, dockertest.DockerConfig(t, `{}`))
		if err == nil || !strings.Contains(err.Error(), "requires credentials") {
			t.Fatalf("Resolve without credentials: err = %v, want a missing-credentials error", err)
		}
	})
}
