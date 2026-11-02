// relationships:
//   references: [dockerfile, client]

package buildkit

import (
	"context"
	gateway "github.com/moby/buildkit/frontend/gateway/client"
	"reflect"
	"testing"
)

func TestInlineCacheBuildArgument(t *testing.T) {
	for _, value := range []string{"1", "true", "0", "false", ""} {
		original := []CacheRef{{Type: "local", Attrs: map[string]string{"dest": "sample-cache"}}}
		got := dockerfileSolveOptions(&DockerfileBuild{BuildArgs: map[string]string{"BUILDKIT_INLINE_CACHE": value}}, SolveOptions{CacheExports: original})
		want := 1
		if value == "1" || value == "true" {
			want = 2
		}
		if len(got.CacheExports) != want || got.CacheExports[0].Type != "local" || (want == 2 && got.CacheExports[1].Type != "inline") {
			t.Fatalf("value=%q exports=%+v", value, got.CacheExports)
		}
		if len(original) != 1 {
			t.Fatal("mutated caller exports")
		}
	}
}

func TestGatewayCacheImports(t *testing.T) {
	for _, stage := range []DevcontainerStage{FinalStage, AuthoredStage} {
		gc := &fakeGateway{baseConfig: []byte(`{"config":{}}`)}
		spec := imageBaseSpec()
		spec.Upstream = &DockerfileBuild{Stage: stage}
		entries := []gateway.CacheOptionsEntry{{Type: "local", Attrs: map[string]string{"src": "sample-cache", "digest": "sha256:sample"}}}
		if _, _, err := buildOnePlatform(context.Background(), gc, spec, amd64, buildSettings{cacheImports: entries}); err != nil {
			t.Fatal(err)
		}
		last := gc.requests[len(gc.requests)-1]
		if last.Definition == nil || !reflect.DeepEqual(last.CacheImports, entries) {
			t.Fatalf("stage=%v request=%+v", stage, last)
		}
	}
}
