//go:build docker_integration

// relationships:
//
//	references: [config, builder]
package builder

import (
	"context"
	"fmt"
	imagetypes "github.com/docker/docker/api/types/image"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEngineBuildConfigurationSubstitution(t *testing.T) {
	ctx := context.Background()
	cli := requireEngine(t, ctx)
	dir := t.TempDir()
	content := "FROM alpine\nARG VALUE\nARG FALLBACK\nARG WORKSPACE\nRUN printf '%s\\n%s\\n%s\\n' \"$VALUE\" \"$FALLBACK\" \"$WORKSPACE\" > /sample\n"
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	dc := &spec.DevContainer{Path: filepath.Join(dir, "devcontainer.json"), Build: &spec.Build{Dockerfile: "${localEnv:FILE:Dockerfile}", Context: "${localWorkspaceFolder}", Args: map[string]string{"VALUE": "${localEnv:SAMPLE_BUILD_VALUE}", "FALLBACK": "${localEnv:SAMPLE_ABSENT:fallback}", "WORKSPACE": "${localWorkspaceFolder}"}}}
	dest := filepath.Join(t.TempDir(), "out")
	_, err := New().Build(ctx, Options{Spec: dc, Workspace: dir, LocalEnv: map[string]string{"SAMPLE_BUILD_VALUE": "selected"}, Docker: cli, WorkDir: t.TempDir(), SkipFeatures: true, Output: "type=local,dest=" + dest, Stdout: io.Discard, Stderr: os.Stderr})
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "sample"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "selected\nfallback\n"+dir+"\n" {
		t.Fatalf("Dockerfile ARG output = %q", got)
	}
}

func TestEngineBuildRuntimeConfigurationSubstitution(t *testing.T) {
	ctx := context.Background()
	cli := requireEngine(t, ctx)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "feature", "devcontainer-feature.json"), `{"id":"sample","version":"1.0.0"}`)
	writeFile(t, filepath.Join(dir, "feature", "install.sh"), "#!/bin/sh\nset -eu\ncp /tmp/dev-container-features/devcontainer-features.builtin.env /sample-users\n")
	dc := &spec.DevContainer{Path: filepath.Join(dir, "devcontainer.json"), Image: "alpine", ContainerUser: "${localEnv:BUILD_CONTAINER_USER}", RemoteUser: "${localEnv:BUILD_REMOTE_USER}", ContainerEnv: map[string]any{"SAMPLE": "${localEnv:VALUE}", "LITERAL": "${localEnv:LITERAL}"}}
	if err := dc.Features.UnmarshalJSON([]byte(`{"./feature":{}}`)); err != nil {
		t.Fatal(err)
	}
	tag := fmt.Sprintf("dcb-substitution-test:%d", time.Now().UnixNano())
	t.Cleanup(func() { _, _ = cli.ImageRemove(context.Background(), tag, imagetypes.RemoveOptions{Force: true}) })
	opts := Options{Spec: dc, Workspace: dir, LocalEnv: map[string]string{"BUILD_CONTAINER_USER": "sample", "BUILD_REMOTE_USER": "other", "VALUE": "selected", "LITERAL": "${localEnv:VALUE}"}, Docker: cli, WorkDir: t.TempDir(), Tags: []string{tag}, Load: true, SkipFeatureAutoMapping: true, Stdout: io.Discard, Stderr: os.Stderr}
	_, err := New().Build(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	img, err := cli.ImageInspect(ctx, tag)
	if err != nil {
		t.Fatal(err)
	}
	env := strings.Join(img.Config.Env, "\n")
	if !strings.Contains(env, "SAMPLE=selected") || !strings.Contains(env, "LITERAL=${localEnv:VALUE}") {
		t.Fatalf("image env=%s", env)
	}
	users := readImageFile(t, ctx, cli, tag, "/sample-users")
	if !strings.Contains(users, `_CONTAINER_USER="sample"`) || !strings.Contains(users, `_REMOTE_USER="other"`) {
		t.Fatalf("feature users=%s", users)
	}
	label := img.Config.Labels["devcontainer.metadata"]
	if !strings.Contains(label, "${localEnv:BUILD_CONTAINER_USER}") || !strings.Contains(label, "${localEnv:LITERAL}") {
		t.Fatalf("authored metadata=%s", label)
	}
}
