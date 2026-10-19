package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/lifecycle"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func isMarkerCheck(req ExecRequest) bool {
	return len(req.Cmd) == 3 && strings.HasPrefix(req.Cmd[2], "test -f ")
}
func isDotfilesScript(req ExecRequest) bool {
	return len(req.Cmd) == 3 && strings.HasPrefix(req.Cmd[2], "# Clone & install dotfiles")
}
func isMarkerWrite(req ExecRequest) bool {
	return len(req.Cmd) == 3 && strings.Contains(req.Cmd[2], "mv -f ")
}

func dotfilesRuntime() Runtime {
	return Runtime{User: "dev", CreatedAt: "c", StartedAt: "s", UserDataFolder: "/home/dev/.devcontainer", Env: map[string]string{"EXAMPLE": "value"},
		Dotfiles: Dotfiles{Repository: "https://example.invalid/dotfiles.git"}}
}

// noMarker answers the marker check with "absent" and every other command
// with success, unless override handles the request.
func noMarker(override func(ExecRequest) (*ExecResult, error)) *fakeExecer {
	return &fakeExecer{handler: func(req ExecRequest) (*ExecResult, error) {
		if override != nil {
			if res, err := override(req); res != nil || err != nil {
				return res, err
			}
		}
		if isMarkerCheck(req) {
			return &ExecResult{ExitCode: 1}, nil
		}
		return &ExecResult{}, nil
	}}
}

func collectLogs(logs *[]string) Options {
	return Options{Logger: func(format string, args ...any) { *logs = append(*logs, fmt.Sprintf(format, args...)) }}
}

func hasLog(logs []string, prefix string) bool {
	for _, line := range logs {
		if strings.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// Upstream runLifecycleHooks installs dotfiles after postCreateCommand and
// before postStartCommand, with the remote user and remote environment.
func TestDotfilesRunAfterPostCreateBeforePostStart(t *testing.T) {
	f := noMarker(nil)
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

// The marker is checked first and written only after the install succeeded.
func TestDotfilesRunWithoutLifecyclePhases(t *testing.T) {
	f := noMarker(nil)
	result, err := RunWithResult(context.Background(), f, nil, dotfilesRuntime(), Options{})
	if err != nil || result.Dotfiles != DotfilesInstalled {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	calls := f.callsCopy()
	if len(calls) != 3 || !isMarkerCheck(calls[0]) || !isDotfilesScript(calls[1]) || !isMarkerWrite(calls[2]) {
		t.Fatalf("calls=%v", calls)
	}
	for _, c := range []ExecRequest{calls[0], calls[2]} {
		if !strings.Contains(c.Cmd[2], "'/home/dev/.devcontainer/.dotfilesMarker'") || c.User != "dev" {
			t.Fatalf("marker command=%+v", c)
		}
	}
}

func TestDotfilesNotConfigured(t *testing.T) {
	f := noMarker(nil)
	rt := dotfilesRuntime()
	rt.Dotfiles = Dotfiles{InstallCommand: "install.sh"}
	result, err := RunWithResult(context.Background(), f, nil, rt, Options{})
	if err != nil || result.Dotfiles != DotfilesNotRun || len(f.callsCopy()) != 0 {
		t.Fatalf("result=%+v err=%v calls=%v", result, err, f.callsCopy())
	}
}

func TestDotfilesMarkerFoundSkipsInstall(t *testing.T) {
	f := &fakeExecer{} // every command, including the marker check, succeeds
	result, err := RunWithResult(context.Background(), f, nil, dotfilesRuntime(), Options{})
	if err != nil || result.Dotfiles != DotfilesSkipped {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if calls := f.callsCopy(); len(calls) != 1 || !isMarkerCheck(calls[0]) {
		t.Fatalf("install ran although the marker exists: %v", calls)
	}
}

// A marker check that fails is never "found": the install runs.
func TestDotfilesMarkerCheckErrorInstalls(t *testing.T) {
	for name, check := range map[string]func() (*ExecResult, error){
		"transport": func() (*ExecResult, error) { return nil, fmt.Errorf("exec broke") },
		"exit":      func() (*ExecResult, error) { return &ExecResult{ExitCode: 2}, nil },
	} {
		t.Run(name, func(t *testing.T) {
			f := noMarker(func(req ExecRequest) (*ExecResult, error) {
				if isMarkerCheck(req) {
					return check()
				}
				return nil, nil
			})
			result, err := RunWithResult(context.Background(), f, nil, dotfilesRuntime(), Options{})
			if err != nil || result.Dotfiles != DotfilesInstalled {
				t.Fatalf("result=%+v err=%v calls=%v", result, err, f.callsCopy())
			}
		})
	}
}

// A failed install is a warning, as upstream. No marker is written, so the
// next run retries, and later lifecycle commands still run.
func TestDotfilesFailureWarnsWritesNoMarkerAndContinues(t *testing.T) {
	for name, fail := range map[string]func() (*ExecResult, error){
		"exit":      func() (*ExecResult, error) { return &ExecResult{ExitCode: 128}, nil },
		"transport": func() (*ExecResult, error) { return nil, fmt.Errorf("exec broke") },
	} {
		t.Run(name, func(t *testing.T) {
			f := noMarker(func(req ExecRequest) (*ExecResult, error) {
				if isDotfilesScript(req) {
					return fail()
				}
				return nil, nil
			})
			var logs []string
			plan := []lifecycle.Phase{{Hook: lifecycle.HookPostStart, When: lifecycle.WhenStart, Command: spec.Command{Shell: "echo start"}}}
			result, err := RunWithResult(context.Background(), f, plan, dotfilesRuntime(), collectLogs(&logs))
			if err != nil || result.Dotfiles != DotfilesFailed || len(result.RanPhases()) != 1 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if !hasLog(logs, "warning: dotfiles install from https://example.invalid/dotfiles.git failed") {
				t.Fatalf("no warning: %q", logs)
			}
			for _, c := range f.callsCopy() {
				if isMarkerWrite(c) {
					t.Fatalf("marker written after a failed install: %v", c)
				}
			}
		})
	}
}

// A marker that cannot be written after a successful install is a warning.
func TestDotfilesMarkerWriteFailureWarns(t *testing.T) {
	f := noMarker(func(req ExecRequest) (*ExecResult, error) {
		if isMarkerWrite(req) {
			return &ExecResult{ExitCode: 1}, nil
		}
		return nil, nil
	})
	var logs []string
	result, err := RunWithResult(context.Background(), f, nil, dotfilesRuntime(), collectLogs(&logs))
	if err != nil || result.Dotfiles != DotfilesInstalled || !hasLog(logs, "warning: write dotfiles marker /home/dev/.devcontainer/.dotfilesMarker: exit 1") {
		t.Fatalf("result=%+v err=%v logs=%q", result, err, logs)
	}
}

// shellExecer runs marker commands on the host shell, so that the marker
// file is real; install answers the dotfiles script.
func shellExecer(ctx context.Context, install func() (*ExecResult, error)) *fakeExecer {
	return &fakeExecer{handler: func(req ExecRequest) (*ExecResult, error) {
		if isDotfilesScript(req) {
			return install()
		}
		out, err := exec.CommandContext(ctx, req.Cmd[0], req.Cmd[1:]...).CombinedOutput()
		if exitErr, ok := err.(*exec.ExitError); ok {
			return &ExecResult{ExitCode: exitErr.ExitCode(), Stderr: out}, nil
		}
		return &ExecResult{Stdout: out}, err
	}}
}

func scriptRuns(f *fakeExecer) int {
	n := 0
	for _, c := range f.callsCopy() {
		if isDotfilesScript(c) {
			n++
		}
	}
	return n
}

// An install cancelled midway leaves no marker; the next run installs, and
// only then does a later run skip.
func TestDotfilesCancelledInstallRetries(t *testing.T) {
	rt := dotfilesRuntime()
	rt.UserDataFolder = filepath.Join(t.TempDir(), "data")
	marker := filepath.Join(rt.UserDataFolder, ".dotfilesMarker")
	ctx, cancel := context.WithCancel(context.Background())
	first := shellExecer(ctx, func() (*ExecResult, error) { cancel(); return nil, ctx.Err() })
	result, err := RunWithResult(ctx, first, nil, rt, Options{})
	if err != nil || result.Dotfiles != DotfilesFailed {
		t.Fatalf("cancelled run: %+v %v", result, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("marker after a cancelled install: %v", err)
	}
	second := shellExecer(context.Background(), func() (*ExecResult, error) { return &ExecResult{}, nil })
	if result, err = RunWithResult(context.Background(), second, nil, rt, Options{}); err != nil || result.Dotfiles != DotfilesInstalled || scriptRuns(second) != 1 {
		t.Fatalf("retry: %+v %v", result, err)
	}
	if entries, err := os.ReadDir(rt.UserDataFolder); err != nil || len(entries) != 1 || entries[0].Name() != ".dotfilesMarker" {
		t.Fatalf("marker folder after success: %v %v", entries, err)
	}
	third := shellExecer(context.Background(), func() (*ExecResult, error) { return &ExecResult{}, nil })
	if result, err = RunWithResult(context.Background(), third, nil, rt, Options{}); err != nil || result.Dotfiles != DotfilesSkipped || scriptRuns(third) != 0 {
		t.Fatalf("after success: %+v %v", result, err)
	}
}

// A user data folder below a regular file can hold no marker: the check is
// not "found", the install runs, the write warns, and the next run installs.
func TestDotfilesUnusableMarkerFolder(t *testing.T) {
	rt := dotfilesRuntime()
	parent := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt.UserDataFolder = filepath.Join(parent, "data")
	for run := 1; run <= 2; run++ {
		f := shellExecer(context.Background(), func() (*ExecResult, error) { return &ExecResult{}, nil })
		var logs []string
		result, err := RunWithResult(context.Background(), f, nil, rt, collectLogs(&logs))
		if err != nil || result.Dotfiles != DotfilesInstalled || scriptRuns(f) != 1 || !hasLog(logs, "warning: write dotfiles marker") {
			t.Fatalf("run %d: %+v %v logs=%q", run, result, err, logs)
		}
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

// The marker write leaves exactly the marker, and the check finds it.
func TestDotfilesMarkerCommandsOnShell(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "data dir", ".dotfilesMarker")
	run := func(script string) int {
		err := exec.Command("/bin/sh", "-c", script).Run()
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		} else if err != nil {
			t.Fatal(err)
		}
		return 0
	}
	if run(markerExistsCommand(marker)) == 0 {
		t.Fatal("marker found before it was written")
	}
	var inodes []uint64
	for i := 0; i < 2; i++ {
		if code := run(writeMarkerCommand(marker)); code != 0 {
			t.Fatalf("write %d: exit %d", i, code)
		}
		info, err := os.Stat(marker)
		if err != nil {
			t.Fatal(err)
		}
		inodes = append(inodes, info.Sys().(*syscall.Stat_t).Ino)
	}
	// A rename puts a new file in place; an in-place write keeps the inode.
	if inodes[0] == inodes[1] {
		t.Fatal("marker rewritten in place, not renamed from a temporary file")
	}
	if run(markerExistsCommand(marker)) != 0 {
		t.Fatal("marker not found after the write")
	}
	if entries, err := os.ReadDir(filepath.Dir(marker)); err != nil || len(entries) != 1 {
		t.Fatalf("marker folder: %v %v", entries, err)
	}
}
