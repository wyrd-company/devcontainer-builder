//go:build docker_integration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	imagetypes "github.com/docker/docker/api/types/image"
	"github.com/docker/docker/pkg/jsonmessage"

	"github.com/wyrd-company/devcontainer-builder/internal/dockertest"
	"github.com/wyrd-company/devcontainer-builder/pkg/registryauth"
)

// TestDockerHostUpPullsPrivateImage covers dcb up on an image that only a
// registry requiring credentials has: dcb reads DOCKER_CONFIG, gets the
// credentials from a credential helper, and pulls the image. The image is
// a test-owned tag in a test-owned registry.
func TestDockerHostUpPullsPrivateImage(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Docker integration test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cli := hostRequireDocker(t, ctx)
	t.Cleanup(func() { _ = cli.Close() })

	reg := dockertest.StartAuthRegistry(t, ctx, cli)
	dockertest.EnsureImage(t, ctx, cli, "alpine:3.20")
	imageRef := fmt.Sprintf("%s/dcb-test/private-up:%d", reg.Addr, time.Now().UnixNano())
	t.Cleanup(func() { dockertest.RemoveImage(t, context.Background(), cli, imageRef) })
	if err := cli.ImageTag(ctx, "alpine:3.20", imageRef); err != nil {
		t.Fatalf("tag %s: %v", imageRef, err)
	}
	encoded, err := registryauth.New(reg.StaticAuthConfig(t)).EngineAuth(imageRef)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := cli.ImagePush(ctx, imageRef, imagetypes.PushOptions{RegistryAuth: encoded})
	if err != nil {
		t.Fatalf("push %s: %v", imageRef, err)
	}
	err = jsonmessage.DisplayJSONMessagesStream(rc, io.Discard, 0, false, nil)
	rc.Close()
	if err != nil {
		t.Fatalf("push %s: %v", imageRef, err)
	}
	if _, err := cli.ImageRemove(ctx, imageRef, imagetypes.RemoveOptions{}); err != nil {
		t.Fatalf("remove local tag %s: %v", imageRef, err)
	}

	bin := hostBuildBinary(t)
	workspace := hostPrepareWorkspace(t, imageRef)
	t.Cleanup(func() {
		_ = hostRunCommandAllowError(bin, "down", "--workspace-folder", workspace)
	})
	config, log := reg.HelperConfig(t, "dcbtest")
	cmd := exec.Command(bin, "up", "--workspace-folder", workspace, "--update-remote-user-uid-default", "off")
	cmd.Env = append(os.Environ(), "DOCKER_CONFIG="+config, "DCB_HOST_VALUE=local-value")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("dcb up: %v\nstdout: %s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	var result map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result["outcome"] != "success" {
		t.Fatalf("dcb up result %s: %v", stdout.String(), err)
	}
	// The helper's stderr reaches neither the JSON result nor dcb's logs.
	if strings.Contains(stdout.String()+stderr.String(), dockertest.HelperStderrMarker) {
		t.Fatalf("credential helper output surfaced:\nstdout: %s\nstderr: %s", stdout.String(), stderr.String())
	}

	if _, err := cli.ImageInspect(ctx, imageRef); err != nil {
		t.Fatalf("image %s not in the daemon after up: %v", imageRef, err)
	}
	if b, err := os.ReadFile(log); err != nil || !strings.Contains(string(b), reg.Addr) {
		t.Fatalf("credential helper was not asked for %s: %q, %v", reg.Addr, b, err)
	}
	if entries, err := os.ReadDir(config); err != nil || len(entries) != 1 || entries[0].Name() != "config.json" {
		t.Fatalf("Docker config directory holds %v (%v), want only config.json", entries, err)
	}
}
