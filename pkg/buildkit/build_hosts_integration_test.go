//go:build docker_integration

// relationships:
//
//	references: [build_hosts, dockerfile, gateway, engine_integration_test]
package buildkit

import (
	"context"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtraHostsBuildIntegration(t *testing.T) {
	ctx := context.Background()
	t.Setenv("BUILDKIT_HOST", "")
	cli := requireEngine(t, ctx)
	for _, nested := range []bool{false, true} {
		for _, gateway := range []bool{false, true} {
			name := "standalone"
			if nested {
				name = "nested"
			}
			if gateway {
				name += "-gateway"
			}
			t.Run(name, func(t *testing.T) {
				dir := t.TempDir()
				out := filepath.Join(t.TempDir(), "out")
				entries := []string{"sample:192.0.2.7", "sample-v6=[2001:db8::7]"}
				if gateway {
					entries = append(entries, "sample-gateway:host-gateway")
				}
				writeTestFile(t, filepath.Join(dir, "Dockerfile"), "FROM alpine\nRUN cp /etc/hosts /hosts\nFROM scratch\nCOPY --from=0 /hosts /hosts\n")
				b := DockerfileBuild{ContextDir: dir, ExtraHosts: entries}
				opt := SolveOptions{Docker: cli, Output: "type=local,dest=" + out, Progress: "plain", Stderr: testLogWriter{t}, NoCache: true}
				var err error
				if nested {
					_, err = SolveDevcontainer(ctx, BuildSpec{Upstream: &b, Graph: GraphInputs{Spec: &spec.DevContainer{}}}, opt)
				} else {
					_, err = SolveDockerfile(ctx, b, opt)
				}
				if err != nil {
					t.Fatal(err)
				}
				data, err := os.ReadFile(filepath.Join(out, "hosts"))
				if err != nil {
					t.Fatal(err)
				}
				for _, want := range []string{"192.0.2.7\tsample", "2001:db8::7\tsample-v6"} {
					if !strings.Contains(string(data), want) {
						t.Fatalf("missing %q in %s", want, data)
					}
				}
				if gateway && !strings.Contains(string(data), "sample-gateway") {
					t.Fatalf("gateway missing: %s", data)
				}
			})
		}
	}
}
