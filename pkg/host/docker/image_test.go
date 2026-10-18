package docker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	dockerclient "github.com/docker/docker/client"
)

// fakeEngine serves the image inspect and pull endpoints of the Engine
// API and records the pulls it receives.
type fakeEngine struct {
	inspectStatus int
	pullBody      string

	mu    sync.Mutex
	pulls []string
}

func (f *fakeEngine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/json") && strings.Contains(r.URL.Path, "/images/"):
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.inspectStatus)
		if f.inspectStatus == http.StatusOK {
			_, _ = w.Write([]byte(`{"Id":"sha256:0123"}`))
		} else {
			_, _ = w.Write([]byte(`{"message":"no such image"}`))
		}
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/images/create"):
		f.mu.Lock()
		f.pulls = append(f.pulls, r.URL.Query().Get("fromImage")+":"+r.URL.Query().Get("tag"))
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(f.pullBody))
	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotImplemented)
	}
}

func newFakeEngineHost(t *testing.T, f *fakeEngine) *Host {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.WithHost("tcp://"+strings.TrimPrefix(srv.URL, "http://")),
		dockerclient.WithVersion("1.45"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return New(cli, Options{})
}

func TestEnsureImage(t *testing.T) {
	const okPull = `{"status":"Pulling from library/alpine"}` + "\n" + `{"status":"Status: Downloaded newer image"}` + "\n"

	t.Run("present image is not pulled", func(t *testing.T) {
		f := &fakeEngine{inspectStatus: http.StatusOK}
		if err := newFakeEngineHost(t, f).ensureImage(context.Background(), "alpine:3.20"); err != nil {
			t.Fatal(err)
		}
		if len(f.pulls) != 0 {
			t.Fatalf("pulls = %v, want none", f.pulls)
		}
	})

	t.Run("missing image is pulled", func(t *testing.T) {
		f := &fakeEngine{inspectStatus: http.StatusNotFound, pullBody: okPull}
		if err := newFakeEngineHost(t, f).ensureImage(context.Background(), "alpine:3.20"); err != nil {
			t.Fatal(err)
		}
		if len(f.pulls) != 1 || f.pulls[0] != "docker.io/library/alpine:3.20" {
			t.Fatalf("pulls = %v, want [docker.io/library/alpine:3.20]", f.pulls)
		}
	})

	t.Run("pull stream error fails", func(t *testing.T) {
		f := &fakeEngine{inspectStatus: http.StatusNotFound, pullBody: `{"errorDetail":{"message":"manifest unknown"},"error":"manifest unknown"}` + "\n"}
		err := newFakeEngineHost(t, f).ensureImage(context.Background(), "alpine:0.0")
		if err == nil || !strings.Contains(err.Error(), "manifest unknown") {
			t.Fatalf("err = %v, want the pull error", err)
		}
	})

	t.Run("inspect failure other than not-found fails without pulling", func(t *testing.T) {
		f := &fakeEngine{inspectStatus: http.StatusInternalServerError}
		if err := newFakeEngineHost(t, f).ensureImage(context.Background(), "alpine:3.20"); err == nil {
			t.Fatal("expected inspect error")
		}
		if len(f.pulls) != 0 {
			t.Fatalf("pulls = %v, want none", f.pulls)
		}
	})
}
