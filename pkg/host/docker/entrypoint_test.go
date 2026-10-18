package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	dockerclient "github.com/docker/docker/client"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// fakeDaemon serves image inspect and container create, recording the
// created container config.
func fakeDaemon(t *testing.T, imageConfig string) (*Host, *container.Config) {
	t.Helper()
	created := &container.Config{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/images/"):
			_, _ = w.Write([]byte(`{"Id":"sha256:0","Config":` + imageConfig + `}`))
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/containers/create"):
			var body struct{ container.Config }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			*created = body.Config
			_, _ = w.Write([]byte(`{"Id":"c0"}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	cli, err := dockerclient.NewClientWithOpts(
		dockerclient.WithHost("tcp://"+srv.Listener.Addr().String()),
		dockerclient.WithVersion("1.47"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return New(cli, Options{}), created
}

// With overrideCommand false, upstream still runs feature entrypoints in
// its wrapper and then execs the image's own entrypoint and command.
func TestCreate_OverrideCommandFalseWrapsImageCommand(t *testing.T) {
	h, created := fakeDaemon(t, `{"Entrypoint":["/docker-entrypoint.sh"],"Cmd":["serve","--port","80"]}`)
	override := false
	req := UpRequest{
		Image:          "img:latest",
		LocalWorkspace: "/host/proj",
		Features: []features.Resolved{{Metadata: features.Metadata{
			ID: "alpha", Entrypoint: "/usr/local/share/alpha-init.sh",
		}}},
	}
	if _, err := h.create(context.Background(), &spec.DevContainer{OverrideCommand: &override}, req, "/workspaces/proj"); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got := strings.Join(created.Entrypoint, " "); got != "/bin/sh" {
		t.Fatalf("entrypoint = %q, want /bin/sh", got)
	}
	if len(created.Cmd) != 7 || created.Cmd[0] != "-c" || !strings.Contains(created.Cmd[1], "/usr/local/share/alpha-init.sh\nexec \"$@\"") ||
		strings.Join(created.Cmd[2:], " ") != "- /docker-entrypoint.sh serve --port 80" {
		t.Fatalf("cmd = %q", created.Cmd)
	}
}
