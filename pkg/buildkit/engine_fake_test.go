package buildkit

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	dockerclient "github.com/docker/docker/client"
)

// fakeEngine serves the Engine API image push endpoint and records the
// requests it receives.
type fakeEngine struct {
	pushBody string

	mu     sync.Mutex
	pushes []string
}

func (f *fakeEngine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/push"):
		i := strings.Index(r.URL.Path, "/images/")
		name := strings.TrimSuffix(r.URL.Path[i+len("/images/"):], "/push")
		f.mu.Lock()
		f.pushes = append(f.pushes, name+":"+r.URL.Query().Get("tag"))
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(f.pushBody))
	default:
		http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotImplemented)
	}
}

func newFakeEngineClient(t *testing.T, f *fakeEngine) *dockerclient.Client {
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
	return cli
}

func TestDaemonPushFinish(t *testing.T) {
	const okPush = `{"status":"The push refers to repository [example.test/app]"}` + "\n" +
		`{"status":"1: digest: sha256:0123 size: 528"}` + "\n"
	tags := []string{"example.test/app:1", "example.test/other:2"}

	t.Run("each tag is pushed", func(t *testing.T) {
		f := &fakeEngine{pushBody: okPush}
		var progress bytes.Buffer
		conn := &Connection{Daemon: true, Docker: newFakeEngineClient(t, f)}
		p, err := exportsFor(context.Background(), SolveOptions{Push: true, Tags: tags, Stderr: &progress}, conn, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.finish(context.Background()); err != nil {
			t.Fatalf("finish: %v", err)
		}
		want := []string{"example.test/app:1", "example.test/other:2"}
		if strings.Join(f.pushes, " ") != strings.Join(want, " ") {
			t.Fatalf("pushes = %v, want %v", f.pushes, want)
		}
		if !strings.Contains(progress.String(), "digest: sha256:0123") {
			t.Fatalf("push progress not written: %q", progress.String())
		}
	})

	t.Run("a push error fails the solve", func(t *testing.T) {
		f := &fakeEngine{pushBody: `{"errorDetail":{"message":"denied: requested access to the resource is denied"},"error":"denied: requested access to the resource is denied"}` + "\n"}
		conn := &Connection{Daemon: true, Docker: newFakeEngineClient(t, f)}
		p, err := exportsFor(context.Background(), SolveOptions{Push: true, Tags: tags, Stderr: &bytes.Buffer{}}, conn, false)
		if err != nil {
			t.Fatal(err)
		}
		err = p.finish(context.Background())
		if err == nil || !strings.Contains(err.Error(), "push example.test/app:1") || !strings.Contains(err.Error(), "denied") {
			t.Fatalf("finish err = %v, want the push denial", err)
		}
		if len(f.pushes) != 1 {
			t.Fatalf("pushes = %v, want to stop after the first failure", f.pushes)
		}
	})
}
