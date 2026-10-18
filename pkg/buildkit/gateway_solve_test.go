package buildkit

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/moby/buildkit/client/llb/sourceresolver"
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
}

func (f *fakeGateway) ResolveImageConfig(_ context.Context, _ string, opt sourceresolver.Opt) (string, digest.Digest, []byte, error) {
	f.resolveOpts = append(f.resolveOpts, opt)
	return "", "", f.baseConfig, nil
}

func (f *fakeGateway) Solve(_ context.Context, req gateway.SolveRequest) (*gateway.Result, error) {
	f.solved = append(f.solved, req.Definition)
	res := gateway.NewResult()
	res.SetRef(fakeRef{})
	return res, nil
}

type fakeRef struct{ gateway.Reference }

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
	got := dockerfileFrontendAttrs(b, buildSettings{resolveMode: pb.AttrImageResolveModePreferLocal, noCache: true}, "ctx", "df")
	want := map[string]string{
		"filename":               "app.Dockerfile",
		"contextkey":             "ctx",
		"dockerfilekey":          "df",
		"image-resolve-mode":     pb.AttrImageResolveModePreferLocal,
		"target":                 "dev",
		"no-cache":               "",
		"build-arg:VARIANT":      "3",
		"label:org.example.role": "app",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("attrs = %v, want %v", got, want)
	}

	got = dockerfileFrontendAttrs(&DockerfileBuild{}, buildSettings{resolveMode: pb.AttrImageResolveModeDefault}, "ctx", "df")
	if got["filename"] != "Dockerfile" {
		t.Errorf("default filename = %q, want Dockerfile", got["filename"])
	}
	if _, ok := got["no-cache"]; ok {
		t.Error("no-cache set without noCache")
	}
}
