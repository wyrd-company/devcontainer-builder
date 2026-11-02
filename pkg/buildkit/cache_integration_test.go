//go:build docker_integration

// relationships:
//   references: [client, dockerfile, gateway]

package buildkit

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestLocalCacheSessionIntegration(t *testing.T) {
	ctx := context.Background()
	cli := requireEngine(t, ctx)
	for _, nested := range []bool{false, true} {
		t.Run(map[bool]string{false: "standalone", true: "nested"}[nested], func(t *testing.T) {
			d := t.TempDir()
			cache := filepath.Join(t.TempDir(), "sample-cache")
			writeTestFile(t, filepath.Join(d, "Dockerfile"), "FROM scratch\nCOPY sample /sample\n")
			writeTestFile(t, filepath.Join(d, "sample"), d)
			b := DockerfileBuild{ContextDir: d}
			var log bytes.Buffer
			opt := SolveOptions{Docker: cli, Output: "type=cacheonly", Progress: "plain", Stderr: &log, CacheExports: []CacheRef{{Type: "local", Attrs: map[string]string{"dest": cache, "mode": "max"}}}}
			solve := func() {
				t.Helper()
				var err error
				if nested {
					_, err = SolveDevcontainer(ctx, BuildSpec{Upstream: &b, Graph: GraphInputs{Spec: &spec.DevContainer{}}}, opt)
				} else {
					_, err = SolveDockerfile(ctx, b, opt)
				}
				if err != nil {
					t.Fatalf("solve: %v\n%s", err, log.String())
				}
			}
			solve()
			if _, err := os.Stat(filepath.Join(cache, "index.json")); err != nil {
				t.Fatal(err)
			}
			log.Reset()
			opt.CacheExports = nil
			opt.CacheImports = []CacheRef{{Type: "local", Attrs: map[string]string{"src": cache}}}
			solve()
			if !strings.Contains(log.String(), "importing cache manifest") {
				t.Fatalf("no native import: %s", log.String())
			}
		})
	}
}
