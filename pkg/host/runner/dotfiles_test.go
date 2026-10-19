package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/lifecycle"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func isMarkerClaim(req ExecRequest) bool {
	return len(req.Cmd) == 3 && strings.Contains(req.Cmd[2], "set -o noclobber")
}
func isDotfilesScript(req ExecRequest) bool {
	return len(req.Cmd) == 3 && strings.HasPrefix(req.Cmd[2], "# Clone & install dotfiles")
}
func isMarkerRelease(req ExecRequest) bool {
	return len(req.Cmd) == 3 && strings.HasPrefix(req.Cmd[2], "rm -f ")
}

func dotfilesRuntime() Runtime {
	return Runtime{User: "dev", CreatedAt: "c", StartedAt: "s", UserDataFolder: "/home/dev/.devcontainer", Env: map[string]string{"EXAMPLE": "value"},
		Dotfiles: Dotfiles{Repository: "https://example.invalid/dotfiles.git"}}
}

// Upstream runLifecycleHooks installs dotfiles after postCreateCommand and
// before postStartCommand, with the remote user and remote environment.
func TestDotfilesRunAfterPostCreateBeforePostStart(t *testing.T) {
	f := &fakeExecer{}
	plan := []lifecycle.Phase{
		{Hook: lifecycle.HookPostCreate, When: lifecycle.WhenOnce, Command: spec.Command{Shell: "echo create"}},
		{Hook: lifecycle.HookPostStart, When: lifecycle.WhenStart, Command: spec.Command{Shell: "echo start"}},
	}
	result, err := RunWithResult(context.Background(), f, plan, dotfilesRuntime(), Options{})
	if err != nil || result.Dotfiles != DotfilesInstalled {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	var order []string
	for _, c := range f.callsCopy() {
		switch {
		case isDotfilesScript(c):
			order = append(order, "dotfiles")
			if c.User != "dev" || c.Env["EXAMPLE"] != "value" {
				t.Errorf("dotfiles exec user=%q env=%v", c.User, c.Env)
			}
		case len(c.Cmd) == 3 && (c.Cmd[2] == "echo create" || c.Cmd[2] == "echo start"):
			order = append(order, c.Cmd[2])
		}
	}
	if strings.Join(order, ",") != "echo create,dotfiles,echo start" {
		t.Fatalf("order=%v", order)
	}
}

func TestDotfilesRunWithoutLifecyclePhases(t *testing.T) {
	f := &fakeExecer{}
	result, err := RunWithResult(context.Background(), f, nil, dotfilesRuntime(), Options{})
	if err != nil || result.Dotfiles != DotfilesInstalled {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	calls := f.callsCopy()
	if len(calls) != 2 || !isMarkerClaim(calls[0]) || !isDotfilesScript(calls[1]) {
		t.Fatalf("calls=%v", calls)
	}
	if !strings.Contains(calls[0].Cmd[2], "'/home/dev/.devcontainer/.dotfilesMarker'") || calls[0].User != "dev" {
		t.Fatalf("marker claim=%+v", calls[0])
	}
}

func TestDotfilesNotConfigured(t *testing.T) {
	f := &fakeExecer{}
	rt := dotfilesRuntime()
	rt.Dotfiles = Dotfiles{InstallCommand: "install.sh"}
	result, err := RunWithResult(context.Background(), f, nil, rt, Options{})
	if err != nil || result.Dotfiles != DotfilesNotRun || len(f.callsCopy()) != 0 {
		t.Fatalf("result=%+v err=%v calls=%v", result, err, f.callsCopy())
	}
}

func TestDotfilesMarkerFoundSkipsInstall(t *testing.T) {
	f := &fakeExecer{handler: func(req ExecRequest) (*ExecResult, error) {
		if isMarkerClaim(req) {
			return &ExecResult{ExitCode: 1}, nil
		}
		return &ExecResult{}, nil
	}}
	result, err := RunWithResult(context.Background(), f, nil, dotfilesRuntime(), Options{})
	if err != nil || result.Dotfiles != DotfilesSkipped {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	for _, c := range f.callsCopy() {
		if isDotfilesScript(c) {
			t.Fatal("install ran although the marker exists")
		}
	}
}

// A failed install is a warning, as upstream; the marker is released so the
// next run retries, and later lifecycle commands still run.
func TestDotfilesFailureWarnsReleasesMarkerAndContinues(t *testing.T) {
	for _, tc := range []struct {
		name string
		fail func() (*ExecResult, error)
	}{
		{"exit", func() (*ExecResult, error) { return &ExecResult{ExitCode: 128}, nil }},
		{"transport", func() (*ExecResult, error) { return nil, fmt.Errorf("exec broke") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeExecer{handler: func(req ExecRequest) (*ExecResult, error) {
				if isDotfilesScript(req) {
					return tc.fail()
				}
				return &ExecResult{}, nil
			}}
			var logs []string
			plan := []lifecycle.Phase{{Hook: lifecycle.HookPostStart, When: lifecycle.WhenStart, Command: spec.Command{Shell: "echo start"}}}
			opts := Options{Logger: func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) }}
			result, err := RunWithResult(context.Background(), f, plan, dotfilesRuntime(), opts)
			if err != nil || result.Dotfiles != DotfilesFailed || len(result.RanPhases()) != 1 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			warned := false
			for _, line := range logs {
				warned = warned || strings.HasPrefix(line, "warning: dotfiles install from https://example.invalid/dotfiles.git failed")
			}
			if !warned {
				t.Fatalf("no warning: %q", logs)
			}
			var released bool
			for _, c := range f.callsCopy() {
				released = released || (isMarkerRelease(c) && strings.Contains(c.Cmd[2], "'/home/dev/.devcontainer/.dotfilesMarker'") && c.User == "dev")
			}
			if !released {
				t.Fatalf("marker not released: %v", f.callsCopy())
			}
		})
	}
}

func TestDotfilesRepositoryURL(t *testing.T) {
	for in, want := range map[string]string{
		"example-owner/dotfiles":          "https://github.com/example-owner/dotfiles.git",
		"https://example.invalid/d.git":   "https://example.invalid/d.git",
		"git@example.invalid:owner/d.git": "git@example.invalid:owner/d.git",
		"/srv/dotfiles.git":               "/srv/dotfiles.git",
		"./dotfiles":                      "./dotfiles",
		"../dotfiles":                     "../dotfiles",
	} {
		if got := (Dotfiles{Repository: in}).repositoryURL(); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}

// The generated script runs on a real shell with a stub git that "clones" a
// prepared directory. HOME is the test directory, so "~" resolves there.
func runDotfilesScript(t *testing.T, d Dotfiles, files map[string]string, withGit bool) (string, string, int) {
	t.Helper()
	home, bin, source := t.TempDir(), t.TempDir(), t.TempDir()
	for name, body := range files {
		p := filepath.Join(source, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if withGit {
		stub := "#!/bin/sh\n[ \"$1 $2 $3\" = 'clone --depth 1' ] || exit 2\necho \"$4\" > \"$HOME/cloned-from\"\ncp -R '" + source + "' \"$5\"\n"
		if err := os.WriteFile(filepath.Join(bin, "git"), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, tool := range []string{"sh", "ls", "grep", "ln", "chmod", "cp", "cat"} {
		if p, err := exec.LookPath(tool); err == nil {
			_ = os.Symlink(p, filepath.Join(bin, tool))
		}
	}
	cmd := exec.Command("/bin/sh", "-c", d.script())
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "PATH=" + bin}
	out, err := cmd.CombinedOutput()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatal(err)
	}
	return home, string(out), code
}

func TestDotfilesScriptDefaultSearchOrder(t *testing.T) {
	files := map[string]string{
		"setup.sh":   "#!/bin/sh\necho setup > \"$HOME/ran\"\n",
		"bootstrap":  "#!/bin/sh\necho bootstrap > \"$HOME/ran\"\n",
		"unrelated":  "#!/bin/sh\necho unrelated > \"$HOME/ran\"\n",
		".dotconfig": "x",
	}
	home, out, code := runDotfilesScript(t, Dotfiles{Repository: "example-owner/dotfiles"}, files, true)
	ran, _ := os.ReadFile(filepath.Join(home, "ran"))
	from, _ := os.ReadFile(filepath.Join(home, "cloned-from"))
	if code != 0 || strings.TrimSpace(string(ran)) != "bootstrap" || strings.TrimSpace(string(from)) != "https://github.com/example-owner/dotfiles.git" {
		t.Fatalf("code=%d ran=%q from=%q out=%s", code, ran, from, out)
	}
	if _, err := os.Stat(filepath.Join(home, "dotfiles", "bootstrap")); err != nil {
		t.Fatalf("default target ~/dotfiles: %v", err)
	}
}

func TestDotfilesScriptLinksWithoutInstallScript(t *testing.T) {
	home, out, code := runDotfilesScript(t, Dotfiles{Repository: "/srv/d.git", TargetPath: "~/custom"}, map[string]string{".exampletoolrc": "x"}, true)
	link, err := os.Readlink(filepath.Join(home, ".exampletoolrc"))
	if code != 0 || err != nil || link != filepath.Join(home, "custom", ".exampletoolrc") {
		t.Fatalf("code=%d link=%q err=%v out=%s", code, link, err, out)
	}
}

func TestDotfilesScriptInstallCommand(t *testing.T) {
	files := map[string]string{
		"install.sh":     "#!/bin/sh\necho default > \"$HOME/ran\"\n",
		"tools/apply.sh": "#!/bin/sh\necho explicit > \"$HOME/ran\"\n",
	}
	home, out, code := runDotfilesScript(t, Dotfiles{Repository: "/srv/d.git", InstallCommand: "tools/apply.sh"}, files, true)
	ran, _ := os.ReadFile(filepath.Join(home, "ran"))
	if code != 0 || strings.TrimSpace(string(ran)) != "explicit" {
		t.Fatalf("code=%d ran=%q out=%s", code, ran, out)
	}
	_, out, code = runDotfilesScript(t, Dotfiles{Repository: "/srv/d.git", InstallCommand: "missing.sh"}, files, true)
	if code != 126 || !strings.Contains(out, "Could not locate missing.sh...") {
		t.Fatalf("missing command: code=%d out=%s", code, out)
	}
	_, out, code = runDotfilesScript(t, Dotfiles{Repository: "/srv/d.git", InstallCommand: "fails.sh"}, map[string]string{"fails.sh": "#!/bin/sh\nexit 3\n"}, true)
	if code != 3 {
		t.Fatalf("failing command: code=%d out=%s", code, out)
	}
	_, out, code = runDotfilesScript(t, Dotfiles{Repository: "/srv/d.git"}, files, false)
	if code != 127 || !strings.Contains(out, "git not found") {
		t.Fatalf("no git: code=%d out=%s", code, out)
	}
}

// The marker claim creates the folder and file once; a second claim fails, so
// a concurrent or later run skips the install.
func TestDotfilesMarkerClaimOnShell(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "data dir", ".dotfilesMarker")
	claim := func() int {
		err := exec.Command("/bin/sh", "-c", createFileCommand(marker)).Run()
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return 0
	}
	if code := claim(); code != 0 {
		t.Fatalf("first claim exit %d", code)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal(err)
	}
	if code := claim(); code == 0 {
		t.Fatal("second claim succeeded")
	}
}
