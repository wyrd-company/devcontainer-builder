package buildkit

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/client/llb/sourceresolver"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	gateway "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	digest "github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// fakeGateway records what a build function submits. Only the methods
// buildOnePlatform calls are implemented.
type fakeGateway struct {
	gateway.Client
	baseConfig  []byte
	resolveOpts []sourceresolver.Opt
	solved      []*pb.Definition
	frontends   []gateway.SolveRequest
}

func (f *fakeGateway) ResolveImageConfig(_ context.Context, _ string, opt sourceresolver.Opt) (string, digest.Digest, []byte, error) {
	f.resolveOpts = append(f.resolveOpts, opt)
	return "", "", f.baseConfig, nil
}

func (f *fakeGateway) Solve(_ context.Context, req gateway.SolveRequest) (*gateway.Result, error) {
	res := gateway.NewResult()
	res.SetRef(fakeRef{})
	if req.Frontend != "" {
		f.frontends = append(f.frontends, req)
		res.AddMeta(exptypes.ExporterImageConfigKey, f.baseConfig)
		return res, nil
	}
	f.solved = append(f.solved, req.Definition)
	return res, nil
}

type fakeRef struct{ gateway.Reference }

func (fakeRef) ToState() (llb.State, error) { return llb.Scratch(), nil }

func decodeOps(t *testing.T, def *pb.Definition) []*pb.Op {
	t.Helper()
	ops := make([]*pb.Op, 0, len(def.Def))
	for _, dt := range def.Def {
		var op pb.Op
		if err := op.Unmarshal(dt); err != nil {
			t.Fatalf("unmarshal op: %v", err)
		}
		ops = append(ops, &op)
	}
	return ops
}

func imageBaseSpec() BuildSpec {
	return BuildSpec{Graph: GraphInputs{
		Spec:               &spec.DevContainer{Image: "alpine:3.20", WorkspaceFolder: "/workspaces/demo"},
		BaseImage:          "alpine:3.20",
		ResolveImageConfig: true,
	}}
}

func baseConfigJSON(t *testing.T) []byte {
	t.Helper()
	var img dockerspec.DockerOCIImage
	img.Config.Env = []string{"PATH=/opt/base/bin:/usr/bin", "BASE_ONLY=1"}
	dt, err := json.Marshal(img)
	if err != nil {
		t.Fatal(err)
	}
	return dt
}

var amd64 = ocispecs.Platform{OS: "linux", Architecture: "amd64"}

func TestBuildOnePlatform_ResolvesBaseImageForTargetPlatform(t *testing.T) {
	arm64 := ocispecs.Platform{OS: "linux", Architecture: "arm64", Variant: "v8"}
	gc := &fakeGateway{baseConfig: baseConfigJSON(t)}
	if _, _, err := buildOnePlatform(context.Background(), gc, imageBaseSpec(), arm64, buildSettings{}); err != nil {
		t.Fatalf("buildOnePlatform: %v", err)
	}
	if len(gc.resolveOpts) != 1 || gc.resolveOpts[0].ImageOpt == nil || gc.resolveOpts[0].ImageOpt.Platform == nil {
		t.Fatalf("ResolveImageConfig opts = %+v, want an image platform", gc.resolveOpts)
	}
	if got := *gc.resolveOpts[0].ImageOpt.Platform; !reflect.DeepEqual(got, arm64) {
		t.Fatalf("ResolveImageConfig platform = %+v, want %+v", got, arm64)
	}
}

func TestBuildOnePlatform_ResolvesBaseImageWithResolveMode(t *testing.T) {
	for _, mode := range []string{
		pb.AttrImageResolveModePreferLocal,
		pb.AttrImageResolveModeForcePull,
		pb.AttrImageResolveModeDefault,
	} {
		t.Run(mode, func(t *testing.T) {
			gc := &fakeGateway{baseConfig: baseConfigJSON(t)}
			if _, _, err := buildOnePlatform(context.Background(), gc, imageBaseSpec(), amd64, buildSettings{resolveMode: mode}); err != nil {
				t.Fatalf("buildOnePlatform: %v", err)
			}
			if len(gc.resolveOpts) != 1 || gc.resolveOpts[0].ImageOpt == nil || gc.resolveOpts[0].ImageOpt.ResolveMode != mode {
				t.Fatalf("ResolveImageConfig opts = %+v, want resolve mode %q", gc.resolveOpts, mode)
			}
			var sources []*pb.SourceOp
			for _, op := range decodeOps(t, gc.solved[0]) {
				if src := op.GetSource(); src != nil {
					sources = append(sources, src)
				}
			}
			if len(sources) != 1 || sources[0].Identifier != "docker-image://docker.io/library/alpine:3.20" {
				t.Fatalf("sources = %+v, want the alpine image", sources)
			}
			got := sources[0].Attrs[pb.AttrImageResolveMode]
			want := mode
			if mode == pb.AttrImageResolveModeDefault {
				want = "" // the default mode is not written into the op
			}
			if got != want {
				t.Fatalf("image source resolve mode = %q, want %q", got, want)
			}
		})
	}
}

func TestBuildOnePlatform_BaseImageEnvReachesBuildSteps(t *testing.T) {
	gc := &fakeGateway{baseConfig: baseConfigJSON(t)}
	if _, _, err := buildOnePlatform(context.Background(), gc, imageBaseSpec(), amd64, buildSettings{resolveMode: pb.AttrImageResolveModePreferLocal}); err != nil {
		t.Fatalf("buildOnePlatform: %v", err)
	}
	var execs int
	for _, op := range decodeOps(t, gc.solved[0]) {
		e := op.GetExec()
		if e == nil {
			continue
		}
		execs++
		if !slices.Contains(e.Meta.Env, "PATH=/opt/base/bin:/usr/bin") || !slices.Contains(e.Meta.Env, "BASE_ONLY=1") {
			t.Errorf("exec %v env = %v, want the base image env", e.Meta.Args, e.Meta.Env)
		}
	}
	if execs == 0 {
		t.Fatal("expected the workspace-folder exec step")
	}
}

func TestBuildOnePlatform_UpstreamDockerfileBase(t *testing.T) {
	gc := &fakeGateway{baseConfig: baseConfigJSON(t)}
	bs := buildSpecWithUpstream()
	if _, _, err := buildOnePlatform(context.Background(), gc, bs, amd64, buildSettings{resolveMode: pb.AttrImageResolveModePreferLocal}); err != nil {
		t.Fatalf("buildOnePlatform: %v", err)
	}
	if len(gc.frontends) != 1 || gc.frontends[0].Frontend != "dockerfile.v0" {
		t.Fatalf("frontend solves = %+v, want one dockerfile.v0", gc.frontends)
	}
	opts := gc.frontends[0].FrontendOpt
	if opts["image-resolve-mode"] != pb.AttrImageResolveModePreferLocal || opts["platform"] != "linux/amd64" ||
		opts["dockerfilekey"] != dockerfileDockerfileLocal {
		t.Fatalf("frontend opts = %v", opts)
	}
	if _, ok := opts["contextkey"]; ok {
		t.Fatalf("frontend opts = %v; contextkey does not select the context mount", opts)
	}
	if len(gc.resolveOpts) != 0 {
		t.Fatalf("upstream base resolved an image config: %+v", gc.resolveOpts)
	}
	var execs int
	for _, op := range decodeOps(t, gc.solved[0]) {
		if e := op.GetExec(); e != nil {
			execs++
			if !slices.Contains(e.Meta.Env, "BASE_ONLY=1") {
				t.Errorf("exec %v env = %v, want the upstream image env", e.Meta.Args, e.Meta.Env)
			}
		}
		if src := op.GetSource(); src != nil && src.Identifier != "" && !strings.HasPrefix(src.Identifier, "local://") {
			t.Errorf("unexpected source %s on an upstream base", src.Identifier)
		}
	}
	if execs == 0 {
		t.Fatal("expected the workspace-folder exec step")
	}
}

func buildSpecWithUpstream() BuildSpec {
	return BuildSpec{
		Graph:    GraphInputs{Spec: &spec.DevContainer{WorkspaceFolder: "/workspaces/demo"}},
		Upstream: &DockerfileBuild{ContextDir: "/ctx"},
	}
}

func TestBuildOnePlatform_NoCacheIgnoresCache(t *testing.T) {
	for _, noCache := range []bool{true, false} {
		gc := &fakeGateway{baseConfig: baseConfigJSON(t)}
		if _, _, err := buildOnePlatform(context.Background(), gc, imageBaseSpec(), amd64, buildSettings{noCache: noCache}); err != nil {
			t.Fatalf("buildOnePlatform: %v", err)
		}
		n := countIgnoreCache(t, gc.solved[0])
		want := 0
		if noCache {
			want = n.total
		}
		if n.total == 0 || n.ignored != want {
			t.Errorf("noCache=%v: %d of %d vertices ignore cache, want %d", noCache, n.ignored, n.total, want)
		}
	}
}

func TestDockerfileFrontendAttrs(t *testing.T) {
	b := &DockerfileBuild{
		Filename:  "app.Dockerfile",
		Target:    "dev",
		BuildArgs: map[string]string{"VARIANT": "3"},
		Labels:    map[string]string{"org.example.role": "app"},
	}
	got := dockerfileFrontendAttrs(b, buildSettings{resolveMode: pb.AttrImageResolveModePreferLocal, noCache: true})
	want := map[string]string{
		"filename":               "app.Dockerfile",
		"dockerfilekey":          "dockerfile",
		"image-resolve-mode":     pb.AttrImageResolveModePreferLocal,
		"target":                 "dev",
		"no-cache":               "",
		"build-arg:VARIANT":      "3",
		"label:org.example.role": "app",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("attrs = %v, want %v", got, want)
	}

	got = dockerfileFrontendAttrs(&DockerfileBuild{}, buildSettings{resolveMode: pb.AttrImageResolveModeDefault})
	if got["filename"] != "Dockerfile" {
		t.Errorf("default filename = %q, want Dockerfile", got["filename"])
	}
	if _, ok := got["no-cache"]; ok {
		t.Error("no-cache set without noCache")
	}
}
