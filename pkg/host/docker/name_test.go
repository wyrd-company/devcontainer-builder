// relationships:
//
//	references: [up, runargs]
package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/errdefs"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestCreateContainerName(t *testing.T) {
	for _, tc := range []struct {
		name     string
		conflict bool
	}{{"", false}, {"sample-dev", false}, {"sample-dev", true}} {
		t.Run(tc.name+map[bool]string{true: "-conflict", false: "-create"}[tc.conflict], func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/containers/create") {
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected", 404)
					return
				}
				calls++
				if got := r.URL.Query().Get("name"); got != tc.name {
					t.Errorf("create name=%q, want %q", got, tc.name)
				}
				var body struct{ Labels map[string]string }
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				if body.Labels[LabelLocalFolder] != "/sample" {
					t.Errorf("identity labels=%v", body.Labels)
				}
				if tc.conflict {
					w.WriteHeader(http.StatusConflict)
					_, _ = w.Write([]byte(`{"message":"name already in use"}`))
					return
				}
				_, _ = w.Write([]byte(`{"Id":"sample-id"}`))
			}))
			defer srv.Close()
			cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost("tcp://"+srv.Listener.Addr().String()), dockerclient.WithVersion("1.47"))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			dc := &spec.DevContainer{}
			if tc.name != "" {
				dc.RunArgs = []string{"--name=" + tc.name}
			}
			id, err := New(cli, Options{}).create(context.Background(), dc, UpRequest{Image: "alpine", LocalWorkspace: "/sample"}, "/workspace")
			if tc.conflict {
				if err == nil || !strings.Contains(err.Error(), `container name "sample-dev" is already in use`) || !errdefs.IsConflict(err) {
					t.Fatalf("conflict=%v", err)
				}
			} else if err != nil || id != "sample-id" {
				t.Fatalf("create=%q, %v", id, err)
			}
			if calls != 1 {
				t.Fatalf("create calls=%d", calls)
			}
		})
	}
}
