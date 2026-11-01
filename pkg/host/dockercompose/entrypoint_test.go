package dockercompose

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
	dockerclient "github.com/docker/docker/client"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestWrapServiceCommand(t *testing.T) {
	imageEntrypoint := []string{"/image-entrypoint.sh"}
	imageCmd := []string{"image-cmd"}
	cases := []struct {
		name                  string
		entrypoint, command   types.ShellCommand
		wantArgs, wantCommand string
	}{
		{"image entrypoint and command", nil, nil, "/image-entrypoint.sh", "image-cmd"},
		{"service entrypoint drops image command", types.ShellCommand{"/svc-entrypoint.sh"}, nil, "/svc-entrypoint.sh", ""},
		{"service command keeps image entrypoint", nil, types.ShellCommand{"svc-cmd"}, "/image-entrypoint.sh", "svc-cmd"},
		{"service entrypoint and command", types.ShellCommand{"/svc-entrypoint.sh"}, types.ShellCommand{"svc-cmd"}, "/svc-entrypoint.sh", "svc-cmd"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc := types.ServiceConfig{ContainerSpec: types.ContainerSpec{Entrypoint: c.entrypoint, Command: c.command}}
			wrapServiceCommand(&svc, []string{"/usr/local/share/alpha-init.sh"}, imageEntrypoint, imageCmd)
			if len(svc.Entrypoint) < 4 || svc.Entrypoint[0] != "/bin/sh" || svc.Entrypoint[1] != "-c" || svc.Entrypoint[3] != "-" {
				t.Fatalf("entrypoint = %q", svc.Entrypoint)
			}
			if !strings.Contains(svc.Entrypoint[2], "/usr/local/share/alpha-init.sh\nexec \"$@\"") {
				t.Fatalf("script = %q", svc.Entrypoint[2])
			}
			if got := strings.Join(svc.Entrypoint[4:], " "); got != c.wantArgs {
				t.Errorf("args after script = %q, want %q", got, c.wantArgs)
			}
			if got := strings.Join(svc.Command, " "); got != c.wantCommand {
				t.Errorf("command = %q, want %q", got, c.wantCommand)
			}
		})
	}
}

// Runs the wrapped entrypoint and command the way the container would:
// the feature entrypoint runs, then the image entrypoint execs with the
// command as its arguments.
func TestWrapServiceCommand_ExecutesFeatureEntrypointThenImageCommand(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "feature-ran")
	out := filepath.Join(dir, "image-args")
	svc := types.ServiceConfig{}
	wrapServiceCommand(&svc, []string{"touch '" + marker + "'"},
		[]string{"/bin/sh", "-c", `echo "$@" > '` + out + `'`, "image"}, []string{"serve", "80"})

	argv := append([]string(svc.Entrypoint), svc.Command...)
	if b, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
		t.Fatalf("run %q: %v\n%s", argv, err, b)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("feature entrypoint did not run: %v", err)
	}
	got, err := os.ReadFile(out)
	if err != nil || strings.TrimSpace(string(got)) != "serve 80" {
		t.Fatalf("image entrypoint args = %q, %v; want serve 80", got, err)
	}
}

func fakeImageDaemon(t *testing.T, imageConfig string) (*Host, func() int) {
	t.Helper()
	var mu sync.Mutex
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/images/") {
			_, _ = w.Write([]byte(`{"Id":"sha256:0","Config":` + imageConfig + `}`))
			return
		}
		http.Error(w, "unexpected", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost("tcp://"+srv.Listener.Addr().String()), dockerclient.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return New(cli, Options{}), func() int { mu.Lock(); defer mu.Unlock(); return requests }
}

func TestWrapPrimaryCommand_UsesImageCommandWhenOverrideCommandFalse(t *testing.T) {
	h, requests := fakeImageDaemon(t, `{"Entrypoint":["/image-entrypoint.sh"],"Cmd":["image-cmd"]}`)
	proj := &types.Project{Services: types.Services{"app": {Name: "app", ContainerSpec: types.ContainerSpec{Image: "img:latest"}}}}
	in := OverrideInputs{Spec: &spec.DevContainer{Service: "app"}, Entrypoints: []string{"/usr/local/share/alpha-init.sh"}}
	if err := h.wrapPrimaryCommand(context.Background(), proj, in); err != nil {
		t.Fatalf("wrapPrimaryCommand: %v", err)
	}
	svc := proj.Services["app"]
	if got := strings.Join(append(svc.Entrypoint[4:], svc.Command...), " "); got != "/image-entrypoint.sh image-cmd" {
		t.Fatalf("exec args = %q", got)
	}
	if requests() != 1 {
		t.Fatalf("daemon requests = %d, want one image inspect", requests())
	}
}

func TestWrapPrimaryCommand_NoOpWithoutEntrypointsOrWhenOverriding(t *testing.T) {
	h, requests := fakeImageDaemon(t, `{}`)
	for _, in := range []OverrideInputs{
		{Spec: &spec.DevContainer{Service: "app"}},
		{Spec: &spec.DevContainer{Service: "app"}, OverrideCommand: true, Entrypoints: []string{"/usr/local/share/alpha-init.sh"}},
	} {
		proj := &types.Project{Services: types.Services{"app": {Name: "app", ContainerSpec: types.ContainerSpec{Image: "img:latest"}}}}
		if err := h.wrapPrimaryCommand(context.Background(), proj, in); err != nil {
			t.Fatal(err)
		}
		if svc := proj.Services["app"]; svc.Entrypoint != nil || svc.Command != nil {
			t.Fatalf("service changed: %+v", svc)
		}
	}
	if requests() != 0 {
		t.Fatalf("daemon requests = %d, want none", requests())
	}
}

func TestWrapPrimaryCommand_ServiceEntrypointAndCommandNeedNoInspect(t *testing.T) {
	h, requests := fakeImageDaemon(t, `{}`)
	proj := &types.Project{Services: types.Services{"app": {
		Name: "app",
		ContainerSpec: types.ContainerSpec{
			Image:      "img:latest",
			Entrypoint: types.ShellCommand{"/svc-entrypoint.sh"},
			Command:    types.ShellCommand{"svc-cmd"},
		},
	}}}
	in := OverrideInputs{Spec: &spec.DevContainer{Service: "app"}, Entrypoints: []string{"/usr/local/share/alpha-init.sh"}}
	if err := h.wrapPrimaryCommand(context.Background(), proj, in); err != nil {
		t.Fatal(err)
	}
	if requests() != 0 {
		t.Fatalf("daemon requests = %d, want none", requests())
	}
	if svc := proj.Services["app"]; strings.Join(append(svc.Entrypoint[4:], svc.Command...), " ") != "/svc-entrypoint.sh svc-cmd" {
		t.Fatalf("service = %+v", svc)
	}
}
