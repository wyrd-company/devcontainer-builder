// relationships:
//
//	references: [up, ../../../internal/dockerimage/image]
package dockercompose

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
	dockerclient "github.com/docker/docker/client"
)

func TestComposeServiceImagePull(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		localStatus                         int
		build, primary, existing, pullError bool
		want                                []string
		wantError                           bool
	}{
		{name: "missing-sidecar", localStatus: 404, want: []string{"inspect", "pull", "create"}},
		{name: "present-sidecar", localStatus: 200, want: []string{"inspect", "create"}},
		{name: "built-sidecar", localStatus: 404, build: true, want: []string{"create"}},
		{name: "primary-build-path", build: true, primary: true, localStatus: 200, want: []string{"inspect", "create"}},
		{name: "pull-error", localStatus: 404, pullError: true, want: []string{"inspect", "pull"}, wantError: true},
		{name: "inspect-error", localStatus: 500, want: []string{"inspect"}, wantError: true},
		{name: "existing-no-recreate", existing: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events []string
			var mu sync.Mutex
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/containers/json"):
					if tc.existing {
						fmt.Fprint(w, `[{"Id":"sample-container"}]`)
					} else {
						fmt.Fprint(w, `[]`)
					}
				case strings.Contains(r.URL.Path, "/images/") && strings.HasSuffix(r.URL.Path, "/json"):
					events = append(events, "inspect")
					w.WriteHeader(tc.localStatus)
					if tc.localStatus == 200 {
						fmt.Fprint(w, `{"Id":"sample-image"}`)
					} else {
						fmt.Fprint(w, `{"message":"image unavailable"}`)
					}
				case strings.HasSuffix(r.URL.Path, "/images/create"):
					events = append(events, "pull")
					if r.Header.Get("X-Registry-Auth") != "sample-auth" {
						t.Error("missing registry auth")
					}
					if tc.pullError {
						fmt.Fprintln(w, `{"errorDetail":{"message":"pull failed"},"error":"pull failed"}`)
					} else {
						fmt.Fprintln(w, `{"status":"done"}`)
					}
				case strings.HasSuffix(r.URL.Path, "/containers/create"):
					events = append(events, "create")
					w.WriteHeader(201)
					fmt.Fprint(w, `{"Id":"sample-container"}`)
				case strings.HasSuffix(r.URL.Path, "/containers/sample-container/json"):
					fmt.Fprint(w, `{"Id":"sample-container","State":{"Running":true}}`)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected", 500)
				}
			}))
			defer srv.Close()
			cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost("tcp://"+strings.TrimPrefix(srv.URL, "http://")), dockerclient.WithVersion("1.45"))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			svc := types.ServiceConfig{Name: "sidecar", ContainerSpec: types.ContainerSpec{Image: "registry.example.test/sample:latest"}}
			if tc.build {
				svc.Build = &types.BuildConfig{Context: "."}
			}
			labels := map[string]string{}
			if tc.primary {
				labels[LabelPrimary] = "true"
			}
			h := New(cli, Options{RegistryAuth: serviceImageAuth{}, NoRecreate: tc.existing})
			_, _, _, _, err = h.bringUp(context.Background(), &types.Project{Name: "sample"}, svc, labels, "", nil, nil, nil)
			if (err != nil) != tc.wantError {
				t.Fatalf("err=%v wantError=%v", err, tc.wantError)
			}
			mu.Lock()
			defer mu.Unlock()
			if !reflect.DeepEqual(events, tc.want) {
				t.Fatalf("events=%v want %v", events, tc.want)
			}
		})
	}
}

type serviceImageAuth struct{}

func (serviceImageAuth) EngineAuth(string) (string, error) { return "sample-auth", nil }
