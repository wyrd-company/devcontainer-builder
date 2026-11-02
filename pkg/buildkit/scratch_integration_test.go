//go:build docker_integration

// relationships:
//   references: [gateway, dockerfile, engine_integration_test]

package buildkit

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// Both routes must export an empty filesystem without a synthetic base layer.
// COPY is a control that also checks the gateway preserves a nonempty ref.
func TestScratchFrontendExportIntegration(t *testing.T) {
	ctx := context.Background()
	t.Setenv("BUILDKIT_HOST", "")
	cli := requireEngine(t, ctx)
	for _, payload := range []bool{false, true} {
		name := "empty"
		if payload {
			name = "copy"
		}
		for _, nested := range []bool{false, true} {
			route := "standalone"
			if nested {
				route = "nested"
			}
			t.Run(name+"/"+route, func(t *testing.T) {
				dir := t.TempDir()
				out := filepath.Join(t.TempDir(), "output")
				df := "FROM scratch\nLABEL sample=retained\n"
				if payload {
					df += "COPY sample /sample\n"
					writeTestFile(t, filepath.Join(dir, "sample"), "payload")
				}
				writeTestFile(t, filepath.Join(dir, "Dockerfile"), df)
				b := DockerfileBuild{ContextDir: dir}
				opt := SolveOptions{Docker: cli, Output: "type=local,dest=" + out, Progress: "plain", Stderr: testLogWriter{t}}
				var err error
				if nested {
					_, err = SolveDevcontainer(ctx, BuildSpec{Upstream: &b, Graph: GraphInputs{Spec: &spec.DevContainer{}}}, opt)
				} else {
					_, err = SolveDockerfile(ctx, b, opt)
				}
				if err != nil {
					t.Fatal(err)
				}
				files, err := os.ReadDir(out)
				if err != nil {
					t.Fatal(err)
				}
				if !payload {
					if len(files) != 0 {
						t.Fatalf("expected empty export: %v", files)
					}
					return
				}
				data, err := os.ReadFile(filepath.Join(out, "sample"))
				if err != nil || string(data) != "payload" {
					t.Fatalf("payload=%q err=%v", data, err)
				}
			})
		}
	}
}
