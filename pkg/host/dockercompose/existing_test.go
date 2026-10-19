// relationships:
//
//	references: [existing, up]
package dockercompose

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	dockerclient "github.com/docker/docker/client"
)

func TestFindExistingCompose(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"first-in-engine-order", `[{"Id":"stopped-first","State":"exited"},{"Id":"running-second","State":"running"}]`, "stopped-first", 200},
		{"empty", `[]`, "", 200},
		{"engine-error", `{"message":"lookup unavailable"}`, "", 500},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if !strings.HasSuffix(r.URL.Path, "/containers/json") {
					t.Errorf("path=%s", r.URL.Path)
				}
				if r.URL.Query().Get("all") != "1" {
					t.Errorf("must include stopped containers: %s", r.URL.RawQuery)
				}
				var got map[string]map[string]bool
				if err := json.Unmarshal([]byte(r.URL.Query().Get("filters")), &got); err != nil {
					t.Error(err)
				}
				want := map[string]map[string]bool{"label": {LabelProject + "=sample-project": true, LabelService + "=app": true}}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("filters=%v want %v", got, want)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost(server.URL), dockerclient.WithVersion("1.47"))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			got, err := FindExisting(context.Background(), cli, "sample-project", "app")
			if tc.status == 500 {
				if err == nil || !strings.Contains(err.Error(), "lookup unavailable") {
					t.Fatalf("err=%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if got != tc.want || requests != 1 {
				t.Fatalf("id=%q requests=%d", got, requests)
			}
		})
	}
}
