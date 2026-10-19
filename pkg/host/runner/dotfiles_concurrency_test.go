package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

// gitSSHWrapper serves git over a test SSH command. Each invocation takes the
// next mode from PLAN: "block" waits on the GATE FIFO and then fails like a
// broken connection, "hold" waits and then serves, "fail" fails at once, and
// anything else serves the repository from the local file system.
const gitSSHWrapper = `#!/bin/sh
n=$(($(cat "$PLAN_COUNT" 2>/dev/null || echo 0) + 1))
echo $n > "$PLAN_COUNT"
mode=$(echo "$PLAN" | cut -d, -f$n)
for last; do :; done
case "$mode" in
block) read _ < "$GATE"; exit 128 ;;
hold) read _ < "$GATE"; exec sh -c "$last" ;;
fail) exit 128 ;;
*) exec sh -c "$last" ;;
esac
`

type gitFixture struct {
	home, repo, gate string
	env              []string
}

func newGitFixture(t *testing.T, plan string) gitFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is required")
	}
	root := t.TempDir()
	f := gitFixture{home: filepath.Join(root, "home"), repo: filepath.Join(root, "repo.git"), gate: filepath.Join(root, "gate")}
	src := filepath.Join(root, "src")
	for _, dir := range []string{f.home, src} {
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(src, "install.sh"), []byte("#!/bin/sh\necho ran >> \"$HOME/installer-runs\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(root, "test-ssh")
	if err := os.WriteFile(wrapper, []byte(gitSSHWrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(f.gate, 0o600); err != nil {
		t.Fatal(err)
	}
	f.env = append(testgit.Env(), "HOME="+f.home, "GIT_SSH_COMMAND="+wrapper, "GIT_SSH_VARIANT=ssh",
		"PLAN="+plan, "PLAN_COUNT="+filepath.Join(root, "plan-count"), "GATE="+f.gate)
	for _, args := range [][]string{{"-C", src, "init", "-q", "-b", "main"}, {"-C", src, "add", "."}, {"-C", src, "commit", "-q", "-m", "dotfiles"}, {"clone", "-q", "--bare", src, f.repo}} {
		cmd := exec.Command("git", args...)
		cmd.Env = f.env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return f
}

// execer runs every command, the dotfiles script included, on the host shell.
func (f gitFixture) execer() *fakeExecer {
	return &fakeExecer{handler: func(req ExecRequest) (*ExecResult, error) {
		cmd := exec.Command(req.Cmd[0], req.Cmd[1:]...)
		cmd.Dir = f.home
		cmd.Env = f.env
		out, err := cmd.CombinedOutput()
		if req.Stderr != nil {
			_, _ = req.Stderr.Write(out)
		}
		if exitErr, ok := err.(*exec.ExitError); ok {
			return &ExecResult{ExitCode: exitErr.ExitCode(), Stdout: out}, nil
		}
		return &ExecResult{Stdout: out}, err
	}}
}

func (f gitFixture) runtime() Runtime {
	return Runtime{UserDataFolder: filepath.Join(f.home, ".devcontainer"), Dotfiles: Dotfiles{Repository: "ssh://example.invalid" + f.repo}}
}

func (f gitFixture) run(t *testing.T) DotfilesOutcome {
	t.Helper()
	result, err := RunWithResult(context.Background(), f.execer(), nil, f.runtime(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	return result.Dotfiles
}

// startBlocked starts a run whose clone waits on the gate, and returns once
// git is waiting. release lets the clone continue; done reports its outcome.
func (f gitFixture) startBlocked(t *testing.T) (release func(), done <-chan DotfilesOutcome) {
	t.Helper()
	outcome := make(chan DotfilesOutcome, 1)
	go func() {
		result, _ := RunWithResult(context.Background(), f.execer(), nil, f.runtime(), Options{})
		outcome <- result.Dotfiles
	}()
	opened := make(chan *os.File, 1)
	go func() {
		gate, err := os.OpenFile(f.gate, os.O_WRONLY, 0)
		if err == nil {
			opened <- gate
		}
	}()
	select {
	case gate := <-opened:
		return func() { _, _ = gate.WriteString("go\n"); _ = gate.Close() }, outcome
	case result := <-outcome:
		t.Fatalf("first run finished before its clone reached the transport: %q", result)
	}
	return nil, nil
}

func (f gitFixture) installerRuns() int {
	body, _ := os.ReadFile(filepath.Join(f.home, "installer-runs"))
	return strings.Count(string(body), "ran\n")
}

// assertPublished checks the clone, the marker and that no staging folder remains.
func (f gitFixture) assertPublished(t *testing.T) {
	t.Helper()
	// Exactly the repository: a clone moved into the target would nest.
	entries, err := os.ReadDir(filepath.Join(f.home, "dotfiles"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if err != nil || strings.Join(names, ",") != ".git,install.sh" {
		t.Errorf("published clone: %v %v", names, err)
	}
	if _, err := os.Stat(filepath.Join(f.home, ".devcontainer", ".dotfilesMarker")); err != nil {
		t.Errorf("marker: %v", err)
	}
	if staged, _ := filepath.Glob(filepath.Join(f.home, ".dotfiles.clone.*")); len(staged) != 0 {
		t.Errorf("staging folders remain: %v", staged)
	}
}

// A run never reads another run's partial clone: the partial clone is in a
// staging folder, and the target appears only when a clone is complete.
// Real git and a real shell; the reproduction of a concurrent run that
// recorded success over a partial clone.
func TestDotfilesConcurrentCloneNeverUsesPartialClone(t *testing.T) {
	assertPartialStaged := func(t *testing.T, f gitFixture) {
		t.Helper()
		if _, err := os.Stat(filepath.Join(f.home, "dotfiles")); !os.IsNotExist(err) {
			t.Fatalf("target exists during the first clone: %v", err)
		}
		if staged, _ := filepath.Glob(filepath.Join(f.home, ".dotfiles.clone.*", ".git")); len(staged) != 1 {
			t.Fatalf("partial clone not staged: %v", staged)
		}
	}
	t.Run("concurrent clone succeeds", func(t *testing.T) {
		f := newGitFixture(t, "block,serve")
		release, first := f.startBlocked(t)
		assertPartialStaged(t, f)
		if got := f.run(t); got != DotfilesInstalled {
			t.Fatalf("second run: %q", got)
		}
		release()
		if got := <-first; got != DotfilesFailed {
			t.Fatalf("first run: %q", got)
		}
		if got := f.run(t); got != DotfilesSkipped {
			t.Fatalf("third run: %q", got)
		}
		f.assertPublished(t)
		if n := f.installerRuns(); n != 1 {
			t.Fatalf("installer ran %d times", n)
		}
	})
	t.Run("concurrent clone fails, then a retry installs", func(t *testing.T) {
		f := newGitFixture(t, "block,fail,serve")
		release, first := f.startBlocked(t)
		assertPartialStaged(t, f)
		if got := f.run(t); got != DotfilesFailed {
			t.Fatalf("second run: %q", got)
		}
		release()
		if got := <-first; got != DotfilesFailed {
			t.Fatalf("first run: %q", got)
		}
		if _, err := os.Stat(filepath.Join(f.home, ".devcontainer", ".dotfilesMarker")); !os.IsNotExist(err) {
			t.Fatalf("marker after failed clones: %v", err)
		}
		if got := f.run(t); got != DotfilesInstalled {
			t.Fatalf("retry: %q", got)
		}
		f.assertPublished(t)
		if n := f.installerRuns(); n != 1 {
			t.Fatalf("installer ran %d times", n)
		}
	})
	t.Run("blocked clone completes after another run published", func(t *testing.T) {
		f := newGitFixture(t, "hold,serve")
		release, first := f.startBlocked(t)
		assertPartialStaged(t, f)
		if got := f.run(t); got != DotfilesInstalled {
			t.Fatalf("second run: %q", got)
		}
		release()
		// The first run's rename is refused; it installs from the published clone.
		if got := <-first; got != DotfilesInstalled {
			t.Fatalf("first run: %q", got)
		}
		f.assertPublished(t)
		if n := f.installerRuns(); n != 2 {
			t.Fatalf("installer ran %d times, want 2 (both runs checked the marker before either wrote it)", n)
		}
	})
}

func TestGitFixtureIgnoresInheritedRepository(t *testing.T) {
	testgit.AssertIsolated(t, func() {
		f := newGitFixture(t, "serve")
		if got := f.run(t); got != DotfilesInstalled {
			t.Fatalf("fixture install = %q", got)
		}
		f.assertPublished(t)
	})
}
