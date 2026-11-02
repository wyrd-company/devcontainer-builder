// relationships:
//   references: [runargs, cidfile, attach]

package docker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	dockerclient "github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestRunArgsAttachContract(t *testing.T) {
	for _, args := range [][]string{nil, {"-aSTDIN"}, {"-itaStDiN"}, {"--attach=stdout", "--attach", "STDERR"}, {"-i"}, {"-i", "-i=false"}, {"-d", "--detach=false"}} {
		cfg, _ := applyForTest(t, args)
		stdin := strings.Contains(strings.Join(args, " "), "STDIN") || strings.Contains(strings.Join(args, " "), "StDiN") || len(args) == 1 && args[0] == "-i"
		if !cfg.AttachStdout || !cfg.AttachStderr || cfg.AttachStdin != stdin || cfg.StdinOnce != (cfg.OpenStdin && stdin) {
			t.Fatalf("%v: %+v", args, cfg)
		}
	}
}
func TestRunArgsDetachConflict(t *testing.T) {
	for _, args := range [][]string{{"-d"}, {"-dit"}, {"--detach=false", "--detach"}, {"--attach=stdin", "--detach=true"}} {
		if _, _, err := tryApply(args); err == nil || !strings.Contains(err.Error(), "cannot specify both") {
			t.Fatalf("%v: %v", args, err)
		}
	}
}
func TestRunArgsAttachValidation(t *testing.T) {
	for _, args := range [][]string{{"-a"}, {"--attach="}, {"--attach=invalid"}, {"-a stdin"}} {
		if _, _, err := tryApply(args); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
}
func TestRunArgsCIDFileScalar(t *testing.T) {
	for _, args := range [][]string{{"--cidfile=first", "--cidfile=relative-id"}, {"--cidfile", "relative-id"}} {
		_, host := applyForTest(t, args)
		if host.ContainerIDFile != "relative-id" {
			t.Fatal(host.ContainerIDFile)
		}
	}
	_, host := applyForTest(t, []string{"--cidfile=first", "--cidfile="})
	if host.ContainerIDFile != "" {
		t.Fatal(host.ContainerIDFile)
	}
	if _, _, err := tryApply([]string{"--cidfile"}); err == nil {
		t.Fatal("accepted missing value")
	}
}
func TestCIDFileWriteFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "id")
	cid, err := newCIDFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := cid.file.Close(); err != nil {
		t.Fatal(err)
	}
	if err := cid.Write("sample-id"); err == nil {
		t.Fatal("write succeeded on closed file")
	}
	cid.Close()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unwritten reservation remains: %v", err)
	}
}
func TestCreateCIDFile(t *testing.T) {
	for _, merged := range []bool{false, true} {
		for _, mode := range []string{"success", "existing", "parent", "create-error", "empty", "relative"} {
			t.Run(fmt.Sprintf("%t/%s", merged, mode), func(t *testing.T) {
				dir := t.TempDir()
				path := filepath.Join(dir, "id")
				args := []string{"--cidfile=ignored", "--cidfile", path}
				if mode == "existing" {
					if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if mode == "parent" {
					path = filepath.Join(dir, "missing", "id")
					args = []string{"--cidfile", path}
				}
				if mode == "empty" {
					args = append(args, "--cidfile=")
				}
				if mode == "relative" {
					t.Chdir(dir)
					path = "relative-id"
					args = []string{"--cidfile", path}
				}
				calls := 0
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					if mode != "empty" {
						if _, err := os.Stat(path); err != nil {
							t.Error("not reserved before Engine create", err)
						}
					}
					if mode == "create-error" {
						http.Error(w, `{"message":"sample create failure"}`, 500)
						return
					}
					var body struct{ HostConfig container.HostConfig }
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if mode != "empty" && body.HostConfig.ContainerIDFile != path {
						t.Error("wire path lost")
					}
					_, _ = w.Write([]byte(`{"Id":"sample-id"}`))
				}))
				defer srv.Close()
				cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost("tcp://"+srv.Listener.Addr().String()), dockerclient.WithVersion("1.47"))
				if err != nil {
					t.Fatal(err)
				}
				defer cli.Close()
				req := UpRequest{Image: "alpine"}
				if merged {
					req.metadataEntries = []metadata.Entry{}
				}
				id, err := New(cli, Options{}).create(context.Background(), &spec.DevContainer{RunArgs: args}, req, "")
				switch mode {
				case "existing":
					if err == nil || calls != 0 {
						t.Fatalf("%s %v calls %d", id, err, calls)
					}
					b, _ := os.ReadFile(path)
					if string(b) != "original" {
						t.Fatal("existing file changed")
					}
				case "parent":
					if err == nil || calls != 0 {
						t.Fatalf("%s %v calls %d", id, err, calls)
					}
				case "create-error":
					if err == nil || calls != 1 {
						t.Fatalf("%s %v calls %d", id, err, calls)
					}
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatal("reservation leaked")
					}
				default:
					if err != nil || id != "sample-id" || calls != 1 {
						t.Fatalf("%s %v calls %d", id, err, calls)
					}
					b, e := os.ReadFile(path)
					if mode == "empty" {
						if !os.IsNotExist(e) {
							t.Fatal("empty final path reserved file")
						}
					} else if e != nil || string(b) != "sample-id" {
						t.Fatalf("ID bytes %q %v", b, e)
					}
				}
			})
		}
	}
}

func TestStartAttachedStreams(t *testing.T) {
	for _, tty := range []bool{false, true} {
		t.Run(fmt.Sprint(tty), func(t *testing.T) {
			started := make(chan struct{})
			closed := make(chan struct{})
			gotInput := make(chan string, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/attach") {
					if r.URL.Query().Get("stdin") != "1" || r.URL.Query().Get("stdout") != "1" || r.URL.Query().Get("stderr") != "1" {
						t.Error(r.URL)
					}
					conn, rw, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					defer conn.Close()
					defer close(closed)
					_, _ = rw.WriteString("HTTP/1.1 101 UPGRADED\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
					_ = rw.Flush()
					<-started
					if tty {
						_, _ = conn.Write([]byte("sample out"))
					} else {
						_, _ = stdcopy.NewStdWriter(conn, stdcopy.Stdout).Write([]byte("sample out"))
						_, _ = stdcopy.NewStdWriter(conn, stdcopy.Stderr).Write([]byte("sample err"))
					}
					b := make([]byte, 5)
					n, _ := rw.Read(b)
					gotInput <- string(b[:n])
				} else if strings.HasSuffix(r.URL.Path, "/start") {
					close(started)
					w.WriteHeader(204)
				} else {
					t.Error(r.URL)
				}
			}))
			defer srv.Close()
			cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost("tcp://"+srv.Listener.Addr().String()), dockerclient.WithVersion("1.47"))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			// Reads occur only after server EOF and stream completion, via the wrapper.
			out, errs := &lockedBuffer{}, &lockedBuffer{}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			h := New(cli, Options{Stdout: out, Stderr: errs, Stdin: strings.NewReader("input")})
			if err := h.startAttached(ctx, "sample-id", &container.Config{AttachStdin: true, AttachStdout: true, AttachStderr: true, Tty: tty}); err != nil {
				t.Fatal(err)
			}
			select {
			case s := <-gotInput:
				if s != "input" {
					t.Fatal(s)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("stdin not forwarded")
			}
			<-closed
			deadline := time.Now().Add(3 * time.Second)
			for out.String() != "sample out" || !tty && errs.String() != "sample err" {
				if time.Now().After(deadline) {
					t.Fatalf("output %q %q", out.String(), errs.String())
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}
func (b *lockedBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.b.String() }

func TestStartAttachedLifetime(t *testing.T) {
	for _, failStart := range []bool{false, true} {
		t.Run(fmt.Sprint(failStart), func(t *testing.T) {
			closed := make(chan struct{})
			var attached atomic.Bool
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/attach") {
					conn, rw, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					defer conn.Close()
					defer close(closed)
					attached.Store(true)
					_, _ = rw.WriteString("HTTP/1.1 101 UPGRADED\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
					_ = rw.Flush()
					b := make([]byte, 1)
					_, _ = rw.Read(b)
				} else if strings.HasSuffix(r.URL.Path, "/start") {
					if !attached.Load() {
						t.Error("started before attaching")
					}
					if failStart {
						http.Error(w, `{"message":"sample failure"}`, 500)
					} else {
						w.WriteHeader(204)
					}
				} else {
					t.Error(r.URL)
				}
			}))
			defer srv.Close()
			cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost("tcp://"+srv.Listener.Addr().String()), dockerclient.WithVersion("1.47"))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			err = New(cli, Options{}).startAttached(ctx, "sample-id", &container.Config{AttachStdout: true, AttachStderr: true})
			if (err != nil) != failStart {
				t.Fatalf("start result %v", err)
			}
			if !failStart {
				select {
				case <-closed:
					t.Fatal("stream ended at start return")
				default:
				}
				cancel()
			}
			select {
			case <-closed:
			case <-time.After(3 * time.Second):
				t.Fatal("stream not released")
			}
		})
	}
}
