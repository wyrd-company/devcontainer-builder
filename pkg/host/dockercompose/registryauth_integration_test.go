//go:build docker_integration

// relationships:
//
//	references: [registryauth, build, up, ../../../internal/dockertest/authregistry]
package dockercompose

import (
	"context"
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
	"github.com/wyrd-company/devcontainer-builder/pkg/builder"
	"github.com/wyrd-company/devcontainer-builder/pkg/registryauth"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// engineView exposes only the EngineAuth method of a source, as a caller's
// Engine-only credential source does.
type engineView struct{ src registryauth.Static }

func (v engineView) EngineAuth(ref string) (string, error) { return v.src.EngineAuth(ref) }

// TestComposeRegistryAuthIntegration brings up a Compose project whose
// primary and sidecar services build FROM a private image and whose third
// service runs that image, while DOCKER_CONFIG names a working credential
// helper that logs each request. Injected sources reach the primary
// build, Up's service build (through an Engine-only source) and the pull
// without running it; the default source then runs the helper for a
// service build, so that the helper log can tell the two apart.
func TestComposeRegistryAuthIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	t.Setenv("BUILDKIT_HOST", "")
	cli := requireComposeDocker(t, ctx)
	t.Cleanup(func() { _ = cli.Close() })
	reg := dockertest.StartAuthRegistry(t, ctx, cli)
	stamp := time.Now().UnixNano()
	base := fmt.Sprintf("%s/dcb-test/compose-auth-base:%d", reg.Addr, stamp)
	pushComposePrivate(t, ctx, cli, reg, "busybox:1.36", base)
	static := registryauth.Static{reg.Addr: {Username: dockertest.RegistryUsername, Password: dockertest.RegistryPassword}}

	workspace := t.TempDir()
	project := fmt.Sprintf("sample-registry-auth-%d", stamp)
	for name, body := range map[string]string{
		"app.Dockerfile":     "FROM " + base + "\nRUN echo app > /app-built\n",
		"sidecar.Dockerfile": "FROM " + base + "\nRUN echo sidecar > /sidecar-built\n",
		"compose.yaml": `name: ` + project + `
services:
  app:
    build: {context: ., dockerfile: app.Dockerfile}
    image: devcontainer-builder/registryauth-app:` + fmt.Sprint(stamp) + `
    command: [sleep, infinity]
  sidecar:
    build: {context: ., dockerfile: sidecar.Dockerfile}
    image: devcontainer-builder/registryauth-sidecar:` + fmt.Sprint(stamp) + `
    command: [sleep, infinity]
  puller:
    image: ` + base + `
    command: [sleep, infinity]
`,
	} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, ref := range []string{"devcontainer-builder/registryauth-app:" + fmt.Sprint(stamp), "devcontainer-builder/registryauth-sidecar:" + fmt.Sprint(stamp)} {
		ref := ref
		t.Cleanup(func() { dockertest.RemoveImage(t, context.Background(), cli, ref) })
	}
	path := filepath.Join(workspace, "devcontainer.json")
	dc := &spec.DevContainer{Path: path, DockerComposeFile: spec.StringOrSlice{"compose.yaml"}, Service: "app", WorkspaceFolder: "/"}
	helperRan := func(t *testing.T, log string) bool {
		t.Helper()
		b, err := os.ReadFile(log)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return strings.Contains(string(b), reg.Addr)
	}

	t.Run("injected sources run no helper", func(t *testing.T) {
		helperDir, log := reg.HelperConfig(t, "dcbmarker")
		t.Setenv("DOCKER_CONFIG", helperDir)
		built, err := BuildPrimaryImage(ctx, BuildPrimaryInputs{Spec: dc, Workspace: workspace, BuilderOptions: builder.Options{
			Docker: cli, RegistryAuth: static, Progress: "plain", Stdout: io.Discard, Stderr: io.Discard, Logger: t.Logf,
		}})
		if err != nil {
			t.Fatalf("BuildPrimaryImage: %v", err)
		}
		h := New(cli, Options{RegistryAuth: engineView{static}, Logger: t.Logf, Stdout: io.Discard, Stderr: io.Discard})
		t.Cleanup(func() {
			if err := h.RemoveProject(context.Background(), project, workspace); err != nil {
				t.Log(err)
			}
		})
		dockertest.RemoveImage(t, ctx, cli, "devcontainer-builder/registryauth-sidecar:"+fmt.Sprint(stamp))
		result, err := h.Up(ctx, UpRequest{Spec: dc, SpecPath: path, LocalWorkspace: workspace, PrimaryImage: built.Image, SkipLifecycle: true})
		if err != nil {
			t.Fatalf("Up: %v", err)
		}
		for _, name := range []string{"app", "sidecar", "puller"} {
			state, err := cli.ContainerInspect(ctx, result.ContainerIDs[name])
			if err != nil || state.State == nil || !state.State.Running {
				t.Fatalf("%s not running: %+v, %v", name, state.State, err)
			}
		}
		if helperRan(t, log) {
			t.Fatal("the credential helper was run for an injected source")
		}
	})

	t.Run("default source runs the configured helper", func(t *testing.T) {
		helperDir, log := reg.HelperConfig(t, "dcbmarker")
		t.Setenv("DOCKER_CONFIG", helperDir)
		proj, err := LoadProject(ctx, LoadInputs{Spec: dc, Workspace: workspace})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := BuildServiceImages(ctx, BuildServiceImagesInputs{Project: proj, Services: []string{"sidecar"}, Docker: cli,
			Pull: true, Progress: "plain", Stdout: io.Discard, Stderr: io.Discard}); err != nil {
			t.Fatalf("BuildServiceImages: %v", err)
		}
		if !helperRan(t, log) {
			t.Fatal("the credential helper was not run for the default source")
		}
	})
}

// pushComposePrivate tags the local image src as dst, pushes it with the
// registry's credentials, and removes the local dst tag, so that Compose
// must pull or build from the registry.
func pushComposePrivate(t *testing.T, ctx context.Context, cli *dockerclient.Client, reg *dockertest.AuthRegistry, src, dst string) {
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
