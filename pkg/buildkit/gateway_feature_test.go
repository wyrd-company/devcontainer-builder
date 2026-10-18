package buildkit

import (
	"context"
	"strings"
	"testing"

	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
)

// solvedGraph decodes the devcontainer definition fakeGateway captured.
func solvedGraph(t *testing.T, gc *fakeGateway) graphOps {
	t.Helper()
	if len(gc.solved) != 1 {
		t.Fatalf("solved %d definitions, want 1", len(gc.solved))
	}
	g := graphOps{}
	for _, dt := range gc.solved[0].Def {
		var op pb.Op
		if err := op.UnmarshalVT(dt); err != nil {
			t.Fatal(err)
		}
		g[digest.FromBytes(dt)] = &op
	}
	return g
}

func TestBuildOnePlatform_FeaturesSeeBaseImageUser(t *testing.T) {
	for _, upstream := range []bool{false, true} {
		gc := &fakeGateway{baseConfig: []byte(`{"config":{"User":"image-user"}}`)}
		in := featureGraphInputs(testFeature)
		in.Spec.RemoteUser = ""
		in.ResolveImageConfig = true
		in.ImageUser = "" // the base config must supply it
		bs := BuildSpec{Graph: in}
		if upstream {
			bs.Upstream = &DockerfileBuild{ContextDir: "."}
		}
		if _, _, err := buildOnePlatform(context.Background(), gc, bs, ocispecs.Platform{OS: "linux", Architecture: "amd64"}, buildSettings{}); err != nil {
			t.Fatalf("buildOnePlatform (upstream=%v): %v", upstream, err)
		}
		want := features.BuiltinEnvContent("image-user", "image-user")
		if got := solvedGraph(t, gc).allMkfiles()["/"+features.BuiltinEnvFile]; got != want {
			t.Fatalf("upstream=%v: builtin env = %q, want %q", upstream, got, want)
		}
	}
}

// Upstream merges features first, then devcontainer.json, so the spec's
// containerEnv wins a key collision.
func TestMergeImageConfig_SpecContainerEnvWinsOverFeature(t *testing.T) {
	g := featureGraphInputs(features.Resolved{Metadata: features.Metadata{
		ContainerEnv: map[string]string{"SHARED": "feature", "FEATURE_ONLY": "yes"},
	}})
	g.Spec.ContainerEnv = map[string]any{"SHARED": "spec"}
	out, err := mergeImageConfig(nil, g, nil, ocispecs.Platform{OS: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"SHARED=spec"`) || !strings.Contains(string(out), `"FEATURE_ONLY=yes"`) {
		t.Fatalf("config env = %s", out)
	}
}
