// relationships:
//
//	references: [entrypoint, override, spawn, metadata]
package dockercompose

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestPrimaryCommandComposeDefaultAndOverride(t *testing.T) {
	for _, mode := range []string{"default", "false", "true"} {
		t.Run(mode, func(t *testing.T) {
			h, requests := fakeImageDaemon(t, `{"Entrypoint":["/image","$LITERAL"],"Cmd":["serve","$$"]}`)
			dc := &spec.DevContainer{Service: "app"}
			if mode != "default" {
				value := mode == "true"
				dc.OverrideCommand = &value
			}
			proj := &types.Project{Services: types.Services{"app": {Name: "app", ContainerSpec: types.ContainerSpec{Image: "alpine"}}}}
			if err := h.applyPrimaryCommand(context.Background(), proj, OverrideInputs{Spec: dc}); err != nil {
				t.Fatal(err)
			}
			svc := proj.Services["app"]
			if len(svc.Entrypoint) < 4 || svc.Entrypoint[0] != "/bin/sh" || !strings.Contains(svc.Entrypoint[2], `exec "$@"`) {
				t.Fatalf("missing wrapper: %#v", svc)
			}
			if svc.StdinOpen || svc.Tty {
				t.Fatal("wrapper added stdin/tty")
			}
			if mode == "true" {
				if len(svc.Entrypoint) != 4 || len(svc.Command) != 0 || requests() != 0 {
					t.Fatalf("override: %+v requests=%d", svc, requests())
				}
			} else if !reflect.DeepEqual([]string(svc.Entrypoint[4:]), []string{"/image", "$LITERAL"}) || !reflect.DeepEqual([]string(svc.Command), []string{"serve", "$$"}) || requests() != 1 {
				t.Fatalf("preserved command: %+v requests=%d", svc, requests())
			}
		})
	}
}

func TestPrimaryEmptyOverridesSuppressImageDefaultsWithoutInspect(t *testing.T) {
	for _, entry := range []types.ShellCommand{{}, {"/service"}, {""}} {
		h, requests := fakeImageDaemon(t, `{"Entrypoint":["/image"],"Cmd":["default"]}`)
		proj := &types.Project{Services: types.Services{"app": {Name: "app", ContainerSpec: types.ContainerSpec{Image: "alpine", Entrypoint: entry}}}}
		if err := h.applyPrimaryCommand(context.Background(), proj, OverrideInputs{Spec: &spec.DevContainer{Service: "app"}, Entrypoints: []string{"echo first", "echo second"}}); err != nil {
			t.Fatal(err)
		}
		svc := proj.Services["app"]
		if requests() != 0 || len(svc.Command) != 0 || !reflect.DeepEqual([]string(svc.Entrypoint[4:]), []string(entry)) {
			t.Fatalf("entry=%#v svc=%+v requests=%d", entry, svc, requests())
		}
		if !strings.Contains(svc.Entrypoint[2], "echo first\necho second\nexec") {
			t.Fatal(svc.Entrypoint)
		}
	}
}

func TestNativeComposeEmptyCommandAndEntryReset(t *testing.T) {
	for _, entry := range []types.ShellCommand{nil, {}, {""}, {"/service"}} {
		h, requests := fakeImageDaemon(t, `{"Entrypoint":["/image"],"Cmd":["default"]}`)
		svc := types.ServiceConfig{ContainerSpec: types.ContainerSpec{Image: "alpine", Entrypoint: entry, Command: types.ShellCommand{}}}
		if err := h.resolveEmptyCommand(context.Background(), &svc); err != nil {
			t.Fatal(err)
		}
		tr, err := translateService(&types.Project{Name: "sample"}, svc, "sample-app", nil, nil, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		want := []string(entry)
		if entry == nil {
			want = []string{"/image"}
			if requests() != 1 {
				t.Fatal("missing image fallback")
			}
		} else if requests() != 0 {
			t.Fatal("unneeded inspect")
		}
		if len(want) == 0 {
			want = []string{""}
		}
		if !reflect.DeepEqual([]string(tr.Config.Entrypoint), want) || tr.Config.Cmd == nil || len(tr.Config.Cmd) != 0 {
			t.Fatalf("entry=%#v config=%+v", entry, tr.Config)
		}
	}
}

func TestOverrideLiteralDollarsAndRemoteEnvExclusion(t *testing.T) {
	dir := t.TempDir()
	dc := &spec.DevContainer{Service: "app", DockerComposeFile: spec.StringOrSlice{filepath.Join(dir, "compose.yml")}}
	// LoadProject uses a native file and compose-go interpolation.
	writeComposeFixture(t, dir)
	out, err := OverrideYAML(OverrideInputs{Spec: dc, OverrideCommand: true, ContainerEnv: map[string]string{"VALUE": "$VALUE:${VALUE}:$$"}, RemoteEnv: map[string]string{"REMOTE": "exec"}, Labels: map[string]string{"literal": "$VALUE"}, Entrypoints: []string{`printf '%s' "$VALUE"`}})
	if err != nil {
		t.Fatal(err)
	}
	p, err := LoadProject(context.Background(), LoadInputs{Spec: dc, Workspace: dir, LocalEnv: map[string]string{"VALUE": "host"}, Override: out})
	if err != nil {
		t.Fatal(err)
	}
	svc := p.Services["app"]
	if *svc.Environment["VALUE"] != "$VALUE:${VALUE}:$$" || svc.Labels["literal"] != "$VALUE" {
		t.Fatalf("literal values changed: %+v", svc)
	}
	if _, ok := svc.Environment["REMOTE"]; ok {
		t.Fatal("remoteEnv reached container")
	}
	if !strings.Contains(svc.Command[0], `exec "$@"`) || !strings.Contains(svc.Command[0], `wait $!`) || !strings.Contains(svc.Command[0], `"$VALUE"`) {
		t.Fatal(svc.Command)
	}
	if svc.Tty || svc.StdinOpen {
		t.Fatal("override added stdin/tty")
	}
}

func writeComposeFixture(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte("services:\n  app:\n    image: alpine\n"), 0600); err != nil {
		t.Fatal(err)
	}
}
