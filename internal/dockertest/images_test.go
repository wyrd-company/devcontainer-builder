// relationships:
//
//	references: [dockertest]
package dockertest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	dockerclient "github.com/docker/docker/client"
)

// Embed the real TB so private testing methods remain satisfied; intercept
// Fatalf to verify helper failures without failing the enclosing test.
type helperTB struct{ testing.TB }

func (t helperTB) Fatalf(format string, args ...any) { panic(fmt.Sprintf(format, args...)) }

func TestEnsureImage(t *testing.T) {
	for _, tc := range []struct {
		name                string
		inspect, pull       int
		stream, wantFailure string
		wantPull            bool
	}{
		{name: "present", inspect: 200},
		{name: "missing", inspect: 404, pull: 200, stream: `{"status":"downloaded"}`, wantPull: true},
		{name: "inspect-error", inspect: 500, wantFailure: "inspect image"},
		{name: "pull-error", inspect: 404, pull: 500, wantFailure: "pull fixture:owned", wantPull: true},
		{name: "stream-error", inspect: 404, pull: 200, stream: `{"errorDetail":{"message":"fixture stream failure"},"error":"fixture stream failure"}`, wantFailure: "fixture stream failure", wantPull: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pulled := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/images/fixture:owned/json"):
					w.WriteHeader(tc.inspect)
					if tc.inspect == 200 {
						fmt.Fprint(w, `{}`)
					} else {
						fmt.Fprint(w, `{"message":"fixture inspect error"}`)
					}
				case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/images/create"):
					pulled = true
					w.WriteHeader(tc.pull)
					if tc.pull == 200 {
						fmt.Fprint(w, tc.stream)
					} else {
						fmt.Fprint(w, `{"message":"fixture pull error"}`)
					}
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost(server.URL), dockerclient.WithVersion("1.47"))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			var failure any
			func() {
				defer func() { failure = recover() }()
				EnsureImage(helperTB{t}, context.Background(), cli, "fixture:owned")
			}()
			if tc.wantFailure == "" && failure != nil {
				t.Fatalf("unexpected failure: %v", failure)
			}
			if tc.wantFailure != "" && (failure == nil || !strings.Contains(fmt.Sprint(failure), tc.wantFailure)) {
				t.Fatalf("failure = %v, want %q", failure, tc.wantFailure)
			}
			if pulled != tc.wantPull {
				t.Fatalf("pulled = %v, want %v", pulled, tc.wantPull)
			}
		})
	}
}

func TestRemoveImage(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != http.MethodDelete || !strings.HasSuffix(r.URL.Path, "/images/fixture:owned") {
			t.Errorf("unexpected removal: %s %s", r.Method, r.URL)
		}
		if r.URL.Query().Get("noprune") != "1" || r.URL.Query().Get("force") != "1" {
			t.Errorf("removal options = %s, want force without parent pruning", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[]`)
	}))
	defer server.Close()
	cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost(server.URL), dockerclient.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	RemoveImage(t, context.Background(), cli, "")
	if calls != 0 {
		t.Fatal("empty reference removed an image")
	}
	RemoveImage(t, context.Background(), cli, "fixture:owned")
	if calls != 1 {
		t.Fatalf("removal requests = %d, want one exact reference", calls)
	}
}
