//go:build docker_integration

// relationships:
//
//	references: [builder, ../../internal/dockertest/authregistry]
package builder

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	imagetypes "github.com/docker/docker/api/types/image"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"

	"github.com/wyrd-company/devcontainer-builder/internal/dockertest"
	"github.com/wyrd-company/devcontainer-builder/pkg/buildkit"
	"github.com/wyrd-company/devcontainer-builder/pkg/registryauth"
)

// TestBuildRegistryAuthIntegration builds from a private base image with
// a private OCI feature while DOCKER_CONFIG names a working credential
// helper that logs each request. The default source runs the helper; an
// injected source reaches the base image pull, the feature download and
// the base image configuration read without running it.
func TestBuildRegistryAuthIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Docker integration test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	t.Setenv("BUILDKIT_HOST", "")
	cli := requireEngine(t, ctx)
	reg := dockertest.StartAuthRegistry(t, ctx, cli)
	stamp := time.Now().UnixNano()

	base := fmt.Sprintf("%s/dcb-test/auth-base:%d", reg.Addr, stamp)
	pushPrivate(t, ctx, cli, reg, "busybox:1.36", base)
	reg.PushArtifact(t, "dcb-test/features/marker", "1", "application/vnd.devcontainers", []byte("{}"),
		"application/vnd.devcontainers.layer.v1+tar",
		featureLayer(t, `{"id":"marker","version":"1.0.0"}`))
	ws := t.TempDir()
	writeFile(t, filepath.Join(ws, ".devcontainer", "devcontainer.json"),
		`{"image":"`+base+`","features":{"`+reg.Addr+`/dcb-test/features/marker:1":{}}}`)
	staticDir := reg.StaticAuthConfig(t)
	var buildIn func(t *testing.T, ws string, auth buildkit.RegistryAuth) (string, error)
	var buildWith func(t *testing.T, ws string, auth buildkit.RegistryAuth, mutate func(*Options)) (string, error)

	baseOnly := t.TempDir()
	writeFile(t, filepath.Join(baseOnly, ".devcontainer", "devcontainer.json"), `{"image":"`+base+`"}`)

	build := func(t *testing.T, auth buildkit.RegistryAuth) (log string, err error) {
		t.Helper()
		return buildIn(t, ws, auth)
	}
	buildIn = func(t *testing.T, ws string, auth buildkit.RegistryAuth) (log string, err error) {
		t.Helper()
		return buildWith(t, ws, auth, func(*Options) {})
	}
	buildWith = func(t *testing.T, ws string, auth buildkit.RegistryAuth, mutate func(*Options)) (log string, err error) {
		t.Helper()
		helperDir, helperLog := reg.HelperConfig(t, "dcbmarker")
		t.Setenv("DOCKER_CONFIG", helperDir)
		tag := fmt.Sprintf("devcontainer-builder/registryauth-it:%d", time.Now().UnixNano())
		t.Cleanup(func() { dockertest.RemoveImage(t, context.Background(), cli, tag) })
		b := New()
		b.Resolver.OCI.HTTP = reg.Client
		opts := Options{
			Workspace: ws, Tags: []string{tag}, Load: true, Pull: true, Docker: cli,
			RegistryAuth: auth, Progress: "plain", WorkDir: t.TempDir(),
			Logger: t.Logf, Stdout: io.Discard, Stderr: testLogWriter{t},
		}
		mutate(&opts)
		_, err = b.Build(ctx, opts)
		if err == nil {
			if _, ierr := cli.ImageInspect(ctx, tag); ierr != nil {
				t.Fatalf("built image %s not loaded: %v", tag, ierr)
			}
		}
		return helperLog, err
	}
	helperRan := func(t *testing.T, log string) bool {
		t.Helper()
		b, err := os.ReadFile(log)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return strings.Contains(string(b), reg.Addr)
	}

	t.Run("default source runs the configured helper", func(t *testing.T) {
		log, err := build(t, nil)
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if !helperRan(t, log) {
			t.Fatal("the credential helper was not run for the default source")
		}
	})
	user, pass := dockertest.RegistryUsername, dockertest.RegistryPassword
	for name, auth := range map[string]buildkit.RegistryAuth{
		"Static":                         registryauth.Static{reg.Addr: {Username: user, Password: pass}},
		"NewWithoutHelpers static auths": registryauth.NewWithoutHelpers(staticDir),
	} {
		t.Run(name+" runs no helper", func(t *testing.T) {
			log, err := build(t, auth)
			if err != nil {
				t.Fatalf("Build: %v", err)
			}
			if helperRan(t, log) {
				t.Fatal("the credential helper was run for an injected source")
			}
		})
	}
	t.Run("empty Static is anonymous", func(t *testing.T) {
		log, err := build(t, registryauth.Static{})
		if err == nil {
			t.Fatal("Build without credentials succeeded")
		}
		if helperRan(t, log) {
			t.Fatal("the credential helper was run for an empty Static")
		}
	})
	t.Run("NewWithoutHelpers refuses a helper host", func(t *testing.T) {
		helperDir, _ := reg.HelperConfig(t, "dcbmarker")
		for name, dir := range map[string]string{"with a feature": ws, "base image only": baseOnly} {
			log, err := buildIn(t, dir, registryauth.NewWithoutHelpers(helperDir))
			if !errors.Is(err, registryauth.ErrHelpersDisabled) {
				t.Fatalf("%s: Build err = %v, want ErrHelpersDisabled", name, err)
			}
			t.Logf("%s: %v", name, err)
			if helperRan(t, log) {
				t.Fatalf("%s: the credential helper was run with helpers disabled", name)
			}
		}
	})
	t.Run("NewWithoutHelpers error stays typed through BuildKit", func(t *testing.T) {
		helperDir, _ := reg.HelperConfig(t, "dcbmarker")
		skipRead := func(o *Options) { o.SkipImageMetaResolve = true }
		log, err := buildWith(t, baseOnly, registryauth.NewWithoutHelpers(helperDir), skipRead)
		if !errors.Is(err, registryauth.ErrHelpersDisabled) {
			t.Fatalf("Build err = %v, want ErrHelpersDisabled", err)
		}
		if !strings.Contains(err.Error(), "failed to solve") {
			t.Fatalf("Build err = %v, want the BuildKit solve failure", err)
		}
		if helperRan(t, log) {
			t.Fatal("the credential helper was run with helpers disabled")
		}
	})
	t.Run("Static reaches the BuildKit pull of a base image only", func(t *testing.T) {
		log, err := buildIn(t, baseOnly, registryauth.Static{reg.Addr: {Username: user, Password: pass}})
		if err != nil {
			t.Fatalf("Build: %v", err)
		}
		if helperRan(t, log) {
			t.Fatal("the credential helper was run for an injected source")
		}
	})
}

// pushPrivate tags the local image src as dst, pushes it with the
// registry's credentials, and removes the local dst tag, so that a build
// must pull it.
func pushPrivate(t *testing.T, ctx context.Context, cli *dockerclient.Client, reg *dockertest.AuthRegistry, src, dst string) {
	t.Helper()
	dockertest.EnsureImage(t, ctx, cli, src)
	if err := cli.ImageTag(ctx, src, dst); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { dockertest.RemoveImage(t, context.Background(), cli, dst) })
	encoded, err := registryauth.New(reg.StaticAuthConfig(t)).EngineAuth(dst)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := cli.ImagePush(ctx, dst, imagetypes.PushOptions{RegistryAuth: encoded})
	if err != nil {
		t.Fatal(err)
	}
	err = jsonmessage.DisplayJSONMessagesStream(rc, io.Discard, 0, false, nil)
	rc.Close()
	if err != nil {
		t.Fatal(err)
	}
	// Remove only the unique tag owned by this test, never the source image.
	if _, err := cli.ImageRemove(ctx, dst, imagetypes.RemoveOptions{}); err != nil && !dockerclient.IsErrNotFound(err) {
		t.Fatal(err)
	}
}
