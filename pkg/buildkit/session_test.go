// relationships:
//   references: [client, dockerfile, network_test]

package buildkit

import (
	"context"
	"io"
	"testing"

	"github.com/moby/buildkit/client"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/session/secrets/secretsprovider"
)

func TestSolveSessionProviders(t *testing.T) {
	t.Setenv("BUILDKIT_HOST", "")
	provider := secretsprovider.FromMap(map[string][]byte{"sample": []byte("fixture")})
	_, err := runSolve(context.Background(), SolveOptions{Docker: unusedNetworkEngine(t), Stderr: io.Discard, Output: "type=cacheonly", Session: []session.Attachable{provider}}, false, nil, func(_ context.Context, _ *Connection, so client.SolveOpt, ch chan *client.SolveStatus) (*client.SolveResponse, error) {
		close(ch)
		if len(so.Session) != 2 || so.Session[1] != provider {
			t.Fatalf("session providers=%v", so.Session)
		}
		return &client.SolveResponse{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
