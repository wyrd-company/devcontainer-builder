// relationships:
//   references: [gateway, gateway_solve_test]

package buildkit

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/moby/buildkit/client/llb"
	"github.com/moby/buildkit/exporter/containerimage/exptypes"
	gateway "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

type upstreamReferenceGateway struct {
	*fakeGateway
	ref gateway.Reference
}

func (g *upstreamReferenceGateway) Solve(ctx context.Context, req gateway.SolveRequest) (*gateway.Result, error) {
	if req.Frontend == "" {
		return g.fakeGateway.Solve(ctx, req)
	}
	res := gateway.NewResult()
	res.SetRef(g.ref)
	res.AddMeta(exptypes.ExporterImageConfigKey, g.baseConfig)
	return res, nil
}

type stateReference struct {
	gateway.Reference
	state llb.State
	err   error
}

func (r stateReference) ToState() (llb.State, error) { return r.state, r.err }

func TestScratchFrontendEmptyReference(t *testing.T) {
	gc := &fakeGateway{emptyFrontend: true, baseConfig: []byte(`{"config":{"User":"1234","WorkingDir":"/sample","Labels":{"sample":"retained"}}}`)}
	bs := BuildSpec{Upstream: &DockerfileBuild{}, Graph: GraphInputs{Spec: &spec.DevContainer{}}}
	_, cfg, err := buildOnePlatform(context.Background(), gc, bs, amd64, buildSettings{})
	if err != nil {
		t.Fatal(err)
	}
	if len(gc.solved) != 1 || len(gc.solved[0].Def) != 0 {
		t.Fatalf("expected empty filesystem: %+v", gc.solved)
	}
	for _, want := range []string{`"User":"1234"`, `"WorkingDir":"/sample"`, `"sample":"retained"`} {
		if !strings.Contains(string(cfg), want) {
			t.Fatalf("missing %s: %s", want, cfg)
		}
	}
}

func TestScratchFrontendNonemptyReference(t *testing.T) {
	gc := &upstreamReferenceGateway{fakeGateway: &fakeGateway{}, ref: stateReference{state: llb.Scratch().File(llb.Mkfile("/sample", 0600, []byte("payload")))}}
	bs := BuildSpec{Upstream: &DockerfileBuild{}, Graph: GraphInputs{Spec: &spec.DevContainer{}}}
	if _, _, err := buildOnePlatform(context.Background(), gc, bs, amd64, buildSettings{}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, op := range decodeOps(t, gc.solved[0]) {
		if op.GetFile() != nil {
			found = true
		}
	}
	if !found {
		t.Fatal("lost upstream filesystem")
	}
}

func TestScratchFrontendReferenceError(t *testing.T) {
	sentinel := errors.New("sample reference error")
	gc := &upstreamReferenceGateway{fakeGateway: &fakeGateway{}, ref: stateReference{err: sentinel}}
	bs := BuildSpec{Upstream: &DockerfileBuild{}, Graph: GraphInputs{Spec: &spec.DevContainer{}}}
	_, _, err := buildOnePlatform(context.Background(), gc, bs, amd64, buildSettings{})
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "upstream ref to state") {
		t.Fatalf("error=%v", err)
	}
	if len(gc.solved) != 0 {
		t.Fatal("solved after reference conversion error")
	}
}
