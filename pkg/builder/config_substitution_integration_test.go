//go:build docker_integration

// relationships:
//
//	references: [config, builder]
package builder

import (
	"context"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"io"
	"os"
	"path/filepath"
	"testing"
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
