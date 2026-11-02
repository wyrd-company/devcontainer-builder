// relationships:
//
//	references: [dockerfile, gateway, client]
package buildkit

import (
	"context"
	dockerclient "github.com/docker/docker/client"
	"github.com/moby/buildkit/client"
	"io"
	"reflect"
	"testing"

	"github.com/moby/buildkit/util/entitlements"
)

func TestDockerfileNetworkFrontend(t *testing.T) {
	for _, mode := range []string{"", "sandbox", "none", "host"} {
		t.Run(mode, func(t *testing.T) {
			spec := buildSpecWithUpstream()
			spec.Upstream.NetworkMode = mode
			gc := &fakeGateway{baseConfig: baseConfigJSON(t)}
			if _, _, err := buildOnePlatform(context.Background(), gc, spec, amd64, buildSettings{}); err != nil {
				t.Fatal(err)
			}
			if len(gc.frontends) != 1 {
				t.Fatalf("frontends=%v", gc.frontends)
			}
			got, present := gc.frontends[0].FrontendOpt["force-network-mode"]
			if got != mode || present != (mode != "") {
				t.Fatalf("network=%q present=%v want=%q", got, present, mode)
			}
			opt := dockerfileSolveOptions(spec.Upstream, SolveOptions{})
			var want []string
			if mode == "host" {
				want = []string{string(entitlements.EntitlementNetworkHost)}
			}
			if !reflect.DeepEqual(opt.allowedEntitlements, want) {
				t.Fatalf("entitlements=%v want=%v", opt.allowedEntitlements, want)
			}
		})
	}
	if got := dockerfileSolveOptions(nil, SolveOptions{}); len(got.allowedEntitlements) != 0 {
		t.Fatalf("image entitlements=%v", got.allowedEntitlements)
	}
}

func TestDockerfileNetworkSolveEntitlement(t *testing.T) {
	t.Setenv("BUILDKIT_HOST", "")
	for _, mode := range []string{"host", "none", "sandbox", ""} {
		t.Run(mode, func(t *testing.T) {
			opt := dockerfileSolveOptions(&DockerfileBuild{NetworkMode: mode}, SolveOptions{Docker: unusedNetworkEngine(t), Stderr: io.Discard})
			called := false
			_, err := runSolve(context.Background(), opt, false, nil, func(_ context.Context, _ *Connection, so client.SolveOpt, ch chan *client.SolveStatus) (*client.SolveResponse, error) {
				called = true
				close(ch)
				var want []string
				if mode == "host" {
					want = []string{"network.host"}
				}
				if !reflect.DeepEqual(so.AllowedEntitlements, want) {
					t.Errorf("solve entitlements=%v want=%v", so.AllowedEntitlements, want)
				}
				return &client.SolveResponse{}, nil
			})
			if err != nil || !called {
				t.Fatalf("called=%v err=%v", called, err)
			}
		})
	}
}

func unusedNetworkEngine(t *testing.T) *dockerclient.Client {
	t.Helper()
	c, err := dockerclient.NewClientWithOpts(dockerclient.WithHost("unix:///unused.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}
