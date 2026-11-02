//go:build docker_integration

// relationships:
//
//	references: [dockerfile, gateway, build_resources_test]
package buildkit

import (
	"bytes"
	"context"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildResourcesIntegration(t *testing.T) {
	ctx := context.Background()
	cli := requireEngine(t, ctx)
	for _, nested := range []bool{false, true} {
		for _, explicit := range []bool{false, true} {
			name := "standalone"
			if nested {
				name = "nested"
			}
			if explicit {
				name += "/explicit"
			} else {
				name += "/default"
			}
			t.Run(name, func(t *testing.T) {
				d := t.TempDir()
				out := filepath.Join(t.TempDir(), "output")
				writeTestFile(t, filepath.Join(d, "Dockerfile"), "FROM alpine AS build\nRUN ulimit -n > /limit; df -k /dev/shm | tail -1 | awk '{print $2}' > /shm\nFROM scratch\nCOPY --from=build /limit /shm /\n")
				b := DockerfileBuild{ContextDir: d}
				if explicit {
					b.ShmSize = 134217728
					b.Ulimits = "nofile=1024:1024"
				}
				var log bytes.Buffer
				opt := SolveOptions{Docker: cli, Output: "type=local,dest=" + out, NoCache: true, Progress: "plain", Stderr: &log}
				var err error
				if nested {
					_, err = SolveDevcontainer(ctx, BuildSpec{Upstream: &b, Graph: GraphInputs{Spec: &spec.DevContainer{}}}, opt)
				} else {
					_, err = SolveDockerfile(ctx, b, opt)
				}
				if err != nil {
					t.Fatalf("solve: %v\n%s", err, log.String())
				}
				shm, err := os.ReadFile(filepath.Join(out, "shm"))
				if err != nil {
					t.Fatal(err)
				}
				want := "65536"
				if explicit {
					want = "131072"
				}
				if strings.TrimSpace(string(shm)) != want {
					t.Fatalf("shm=%q want=%q", shm, want)
				}
				limit, err := os.ReadFile(filepath.Join(out, "limit"))
				if err != nil {
					t.Fatal(err)
				}
				if explicit && strings.TrimSpace(string(limit)) != "1024" {
					t.Fatalf("limit=%q", limit)
				}
			})
		}
	}
}
