package buildkit

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/docker/docker/api/types/registry"
	dockerclient "github.com/docker/docker/client"

	"github.com/wyrd-company/devcontainer-builder/pkg/registryauth"
)

// dockerConfig writes a Docker client config.json into a new directory.
func dockerConfig(t *testing.T, cfg string) string {
	t.Helper()
	t.Setenv("DOCKER_AUTH_CONFIG", "")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// anonymous is a credential source with no credentials, independent of
// the operator's Docker configuration.
func anonymous(t *testing.T) RegistryAuth {
	return registryauth.New(dockerConfig(t, `{}`))
}

// failingAuth fails every credential lookup.
type failingAuth struct{}

func (failingAuth) Credentials(string) (registryauth.Credentials, error) {
	return registryauth.Credentials{}, errors.New("credential helper failed")
}

func (failingAuth) EngineAuth(string) (string, error) {
	return "", errors.New("credential helper failed")
}

// fakeEngine serves the Engine API image push and save endpoints and
// records the requests it receives.
type fakeEngine struct {
	pushBody string
	saveBody string

	mu     sync.Mutex
	pushes []string
	auths  []string
	saves  [][]string
}

func (f *fakeEngine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/push"):
		i := strings.Index(r.URL.Path, "/images/")
		name := strings.TrimSuffix(r.URL.Path[i+len("/images/"):], "/push")
		f.mu.Lock()
		f.pushes = append(f.pushes, name+":"+r.URL.Query().Get("tag"))
		f.auths = append(f.auths, r.Header.Get("X-Registry-Auth"))
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(f.pushBody))
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/images/get"):
		f.mu.Lock()
		f.saves = append(f.saves, r.URL.Query()["names"])
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/x-tar")
		_, _ = w.Write([]byte(f.saveBody))
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
		p, err := exportsFor(context.Background(), SolveOptions{Push: true, Tags: tags, Stderr: &progress, RegistryAuth: anonymous(t)}, conn, false)
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

	t.Run("each push sends its registry credentials", func(t *testing.T) {
		f := &fakeEngine{pushBody: okPush}
		conn := &Connection{Daemon: true, Docker: newFakeEngineClient(t, f)}
		auth := registryauth.New(dockerConfig(t, `{"auths":{"example.test":{"auth":"`+
			base64.StdEncoding.EncodeToString([]byte("testuser:testpassword"))+`"}}}`))
		p, err := exportsFor(context.Background(), SolveOptions{Push: true, Tags: tags, Stderr: &bytes.Buffer{}, RegistryAuth: auth}, conn, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.finish(context.Background()); err != nil {
			t.Fatalf("finish: %v", err)
		}
		if len(f.auths) != len(tags) {
			t.Fatalf("pushes sent %d X-Registry-Auth values, want %d", len(f.auths), len(tags))
		}
		for i, v := range f.auths {
			b, err := base64.URLEncoding.DecodeString(v)
			if err != nil {
				t.Fatalf("push %d X-Registry-Auth %q: %v", i, v, err)
			}
			var ac registry.AuthConfig
			if err := json.Unmarshal(b, &ac); err != nil {
				t.Fatal(err)
			}
			if ac.Username != "testuser" || ac.Password != "testpassword" || ac.ServerAddress != "example.test" {
				t.Fatalf("push %d credentials = %+v, want the example.test entry", i, ac)
			}
		}
	})

	t.Run("a credential error fails before pushing", func(t *testing.T) {
		f := &fakeEngine{pushBody: okPush}
		conn := &Connection{Daemon: true, Docker: newFakeEngineClient(t, f)}
		p, err := exportsFor(context.Background(), SolveOptions{Push: true, Tags: tags, Stderr: &bytes.Buffer{}, RegistryAuth: failingAuth{}}, conn, false)
		if err != nil {
			t.Fatal(err)
		}
		if err := p.finish(context.Background()); err == nil || !strings.Contains(err.Error(), "credential helper failed") {
			t.Fatalf("finish err = %v, want the credential error", err)
		}
		if len(f.pushes) != 0 {
			t.Fatalf("pushes = %v, want none", f.pushes)
		}
	})

	t.Run("a push error fails the solve", func(t *testing.T) {
		f := &fakeEngine{pushBody: `{"errorDetail":{"message":"denied: requested access to the resource is denied"},"error":"denied: requested access to the resource is denied"}` + "\n"}
		conn := &Connection{Daemon: true, Docker: newFakeEngineClient(t, f)}
		p, err := exportsFor(context.Background(), SolveOptions{Push: true, Tags: tags, Stderr: &bytes.Buffer{}, RegistryAuth: anonymous(t)}, conn, false)
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

// closeRecorder is a writer that records whether it was closed.
type closeRecorder struct {
	bytes.Buffer
	closed bool
}

func (c *closeRecorder) Close() error { c.closed = true; return nil }

func TestDaemonSaveFinish(t *testing.T) {
	f := &fakeEngine{saveBody: "tarball-bytes"}
	conn := &Connection{Daemon: true, Docker: newFakeEngineClient(t, f)}
	var out closeRecorder
	p, err := exportsFor(context.Background(), SolveOptions{Load: true, Tags: []string{"example.test/app:1", "example.test/app:2"}, LoadWriter: &out}, conn, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.finish(context.Background()); err != nil {
		t.Fatalf("finish: %v", err)
	}
	if out.String() != "tarball-bytes" || !out.closed {
		t.Fatalf("writer got %q (closed=%v), want the saved tarball, closed", out.String(), out.closed)
	}
	if len(f.saves) != 1 || strings.Join(f.saves[0], ",") != "example.test/app:1,example.test/app:2" {
		t.Fatalf("saves = %v, want one save of both tags", f.saves)
	}
}
