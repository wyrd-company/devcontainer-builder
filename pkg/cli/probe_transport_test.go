// relationships:
//
//	references: [../host/docker/exec, ../host/dockercompose/exec]
package cli

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dockerclient "github.com/docker/docker/client"
	hostdocker "github.com/wyrd-company/devcontainer-builder/pkg/host/docker"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/dockercompose"
)

func TestProbeAttachClosesOnCancellation(t *testing.T) {
	for _, mode := range []string{"docker", "compose"} {
		t.Run(mode, func(t *testing.T) {
			attached, closed := make(chan struct{}), make(chan error, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/containers/sample/exec"):
					_, _ = w.Write([]byte(`{"Id":"sample-exec"}`))
				case strings.HasSuffix(r.URL.Path, "/exec/sample-exec/start"):
					_, _ = io.Copy(io.Discard, r.Body)
					conn, buf, err := w.(http.Hijacker).Hijack()
					if err != nil {
						closed <- err
						return
					}
					defer conn.Close()
					_, _ = buf.WriteString("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
					_ = buf.Flush()
					close(attached)
					_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
					b := make([]byte, 1)
					_, err = conn.Read(b)
					closed <- err
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.Error(w, "unexpected", 500)
				}
			}))
			defer server.Close()
			cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost("tcp://"+server.Listener.Addr().String()), dockerclient.WithVersion("1.47"))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var err error
				if mode == "docker" {
					_, err = hostdocker.New(cli, hostdocker.Options{}).Exec(ctx, hostdocker.ExecRequest{ContainerID: "sample", Cmd: []string{"/bin/sh", "-lc", "sleep 30"}})
				} else {
					_, err = dockercompose.New(cli, dockercompose.Options{}).Exec(ctx, dockercompose.ExecRequest{ContainerID: "sample", Cmd: []string{"/bin/sh", "-lc", "sleep 30"}})
				}
				done <- err
			}()
			select {
			case <-attached:
			case err := <-done:
				t.Fatalf("Exec failed before attach: %v", err)
			case <-time.After(5 * time.Second):
				t.Fatal("did not attach")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("Exec: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("Exec stream did not finish")
			}
			select {
			case err := <-closed:
				if err != io.EOF {
					t.Fatalf("attach did not close: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("attach left open")
			}
		})
	}
}
