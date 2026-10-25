//go:build docker_integration

// relationships:
//
// references: [CONTRIBUTING]
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestFeatureContainerEnvIntegration builds a base image with the published
// Go feature, whose containerEnv extends ${PATH}, and a local feature that
// reads the Go feature's values, its own earlier keys, an unset name and a
// default. Each value is expanded as the reference CLI's Dockerfile ENV
// expands it, for the install steps and in the final image; the spec's
// containerEnv stays literal.
func TestFeatureContainerEnvIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Docker integration test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	cli := hostRequireDocker(t, ctx)
	defer cli.Close()

	const base = "mcr.microsoft.com/devcontainers/base:trixie"
	if out, err := exec.CommandContext(ctx, "docker", "pull", "-q", base).CombinedOutput(); err != nil {
		t.Fatalf("pull %s: %v\n%s", base, err, out)
	}
	baseImage, err := cli.ImageInspect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	basePath := envValue(baseImage.Config.Env, "PATH")
	if !strings.Contains(basePath, "/bin") {
		t.Fatalf("base PATH = %q", basePath)
	}
	goPath := "/usr/local/go/bin:/go/bin:" + basePath

	bin := hostBuildBinary(t)
	workspace := copyFixture(t, filepath.Join("..", "..", "testdata", "smoke", "feature-containerenv"))
	tag := fmt.Sprintf("devcontainer-builder/feature-containerenv-%d:latest", time.Now().UnixNano())
	t.Cleanup(func() {
		_ = hostRunCommandAllowError(bin, "down", "--workspace-folder", workspace)
		_ = exec.Command("docker", "image", "rm", "-f", tag).Run()
	})

	up := hostRunCommand(t, os.Environ(), bin,
		"up",
		"--workspace-folder", workspace,
		"--image-name", tag,
	)
	var result struct {
		ContainerID string `json:"containerId"`
	}
	if err := json.Unmarshal(up, &result); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"PATH=" + goPath,
		"GOPATH=/go",
		"ZZ_HOME=/go/zz",
		"AA_BIN=/go/zz/bin",
		"MM_UNSET=[]",
		"QQ_DEFAULT=fallback",
		"SPEC_REF=${GOPATH}/spec",
	}
	img, err := cli.ImageInspect(ctx, tag)
	if err != nil {
		t.Fatalf("inspect image %s: %v", tag, err)
	}
	for _, kv := range want {
		if !slices.Contains(img.Config.Env, kv) {
			t.Errorf("image config env missing %q: %q", kv, img.Config.Env)
		}
	}
	// The container keeps the image's expanded values: creation adds no
	// literal feature value on top of them.
	ctr, err := cli.ContainerInspect(ctx, result.ContainerID)
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range want {
		if !slices.Contains(ctr.Config.Env, kv) {
			t.Errorf("container env missing %q: %q", kv, ctr.Config.Env)
		}
	}

	out := hostRunCommand(t, os.Environ(), bin,
		"exec",
		"--workspace-folder", workspace,
		"--tty=false",
		"--",
		"sh", "-c",
		`cat /usr/local/share/dcb-env-chain.txt; go version; command -v chmod`,
	)
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	wantInstall := []string{
		"AA_BIN=/go/zz/bin",
		"MM_UNSET=[]",
		"QQ_DEFAULT=fallback",
		"PATH=" + goPath,
		"go=/usr/local/go/bin/go",
	}
	if len(lines) != len(wantInstall)+2 || !slices.Equal(lines[:len(wantInstall)], wantInstall) {
		t.Fatalf("install env and tools:\n%s\nwant install env:\n%s", out, strings.Join(wantInstall, "\n"))
	}
	if !strings.HasPrefix(lines[len(wantInstall)], "go version go1.27.3 ") {
		t.Errorf("go version = %q", lines[len(wantInstall)])
	}
	if !strings.HasSuffix(lines[len(wantInstall)+1], "/chmod") {
		t.Errorf("chmod = %q", lines[len(wantInstall)+1])
	}
}

func envValue(env []string, key string) string {
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}
