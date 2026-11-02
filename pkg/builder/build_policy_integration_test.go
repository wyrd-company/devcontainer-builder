//go:build docker_integration

// relationships:
//   references: [builder, build_policy_test]

package builder

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestEngineBuildPolicyOptions(t *testing.T) {
	ctx := context.Background()
	cli := requireEngine(t, ctx)
	d := t.TempDir()
	for name, value := range map[string]string{"Dockerfile": "FROM alpine\nCOPY salt /salt\nRUN cat /proc/sys/kernel/random/uuid > /sample\n", "salt": d} {
		if err := os.WriteFile(filepath.Join(d, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	dc := &spec.DevContainer{Path: filepath.Join(d, "devcontainer.json"), Build: &spec.Build{Dockerfile: "Dockerfile"}}
	build := func(args []string) string {
		t.Helper()
		dc.Build.Options = args
		out := filepath.Join(t.TempDir(), "out")
		var log bytes.Buffer
		_, err := New().Build(ctx, Options{Spec: dc, Workspace: d, WorkDir: t.TempDir(), Docker: cli, SkipFeatureAutoMapping: true, SkipImageMetaResolve: true, Output: "type=local,dest=" + out, Stdout: io.Discard, Stderr: &log})
		if err != nil {
			t.Fatalf("build: %v\n%s", err, log.String())
		}
		value, err := os.ReadFile(filepath.Join(out, "sample"))
		if err != nil {
			t.Fatal(err)
		}
		return string(value)
	}
	first := build(nil)
	if cached := build([]string{"--no-cache=false", "--pull=false"}); cached != first {
		t.Fatalf("cache reused value=%q want=%q", cached, first)
	}
	if fresh := build([]string{"--no-cache", "--pull=false"}); fresh == first {
		t.Fatalf("cache bypass reused value=%q", fresh)
	}
}
