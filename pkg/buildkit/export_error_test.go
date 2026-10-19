// relationships:
//
//	references: [client, output]
package buildkit

import (
	"context"
	"errors"
	"strings"
	"testing"

	dockerclient "github.com/docker/docker/client"
	"github.com/moby/buildkit/client"
	"github.com/tonistiigi/fsutil"
)

func TestOCIExporterErrorExplainsClassicStore(t *testing.T) {
	for _, tc := range []struct {
		name, address, message string
		explain                bool
	}{
		{"classic", "", `exporter "oci" could not be found`, true},
		{"remote", "tcp://buildkit.test:1234", `exporter "oci" could not be found`, false},
		{"unrelated", "", "build failed", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BUILDKIT_HOST", "")
			dc, err := dockerclient.NewClientWithOpts(dockerclient.WithHost("unix:///unused.sock"))
			if err != nil {
				t.Fatal(err)
			}
			defer dc.Close()
			original := errors.New(tc.message)
			_, err = runSolve(context.Background(), SolveOptions{Docker: dc, Address: tc.address, Output: "type=oci"}, false, map[string]fsutil.FS{}, func(_ context.Context, _ *Connection, _ client.SolveOpt, ch chan *client.SolveStatus) (*client.SolveResponse, error) {
				close(ch)
				return nil, original
			})
			if !errors.Is(err, original) {
				t.Fatalf("lost original error: %v", err)
			}
			for _, hint := range []string{"classic Docker image store", "containerd image store", "separate BuildKit"} {
				if strings.Contains(err.Error(), hint) != tc.explain {
					t.Fatalf("explanation %q: %v", hint, err)
				}
			}
		})
	}
}
