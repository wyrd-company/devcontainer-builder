//go:build docker_integration

// relationships:
//   references: [build_cache, engine_integration_test]

package builder

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestEngineBuildCacheOptions(t *testing.T) {
	ctx := context.Background()
	cli := requireEngine(t, ctx)
	d := t.TempDir()
	cache := filepath.Join(d, "cache,store")
	if err := os.WriteFile(filepath.Join(d, "Dockerfile"), []byte("FROM alpine\nCOPY sample /sample\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "sample"), []byte(d), 0600); err != nil {
		t.Fatal(err)
	}
	dc := &spec.DevContainer{Path: filepath.Join(d, "devcontainer.json"), Build: &spec.Build{Dockerfile: "Dockerfile", Options: []string{"--cache-to", `type=local,"dest=` + cache + `",mode=max`}}}
	build := func() {
		t.Helper()
		var log bytes.Buffer
		_, err := New().Build(ctx, Options{Spec: dc, Workspace: d, WorkDir: t.TempDir(), Docker: cli, SkipFeatureAutoMapping: true, SkipImageMetaResolve: true, Output: "type=cacheonly", Stdout: io.Discard, Stderr: &log})
		if err != nil {
			t.Fatalf("build: %v\n%s", err, log.String())
		}
		if len(dc.Build.CacheFrom) > 0 && !strings.Contains(log.String(), "importing cache manifest") {
			t.Fatalf("missing native cache import: %s", log.String())
		}
	}
	build()
	raw, err := os.ReadFile(filepath.Join(cache, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	var index struct{ Manifests []struct{ Digest string } }
	if err = json.Unmarshal(raw, &index); err != nil || len(index.Manifests) == 0 {
		t.Fatalf("index=%s err=%v", raw, err)
	}
	for _, m := range index.Manifests {
		if _, err := os.Stat(filepath.Join(cache, "blobs", strings.Replace(m.Digest, ":", "/", 1))); err != nil {
			t.Fatal(err)
		}
	}
	dc.Build.Options = []string{"--build-arg=BUILDKIT_INLINE_CACHE=0"}
	dc.Build.CacheFrom = spec.StringOrSlice{`type=local,"src=` + cache + `"`}
	build()
}
