// relationships:
//   references: [git_chdir_linux, git_chdir_root_linux_test]

package workspace

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// secbitNoroot and secbitNorootLocked are the kernel's SECBIT_NOROOT and
// SECBIT_NOROOT_LOCKED securebits.
const (
	secbitNoroot       = 1 << 0
	secbitNorootLocked = 1 << 1
)

// chdir checks a folder's search permission with the effective credentials,
// which access checks with the real ones. A process that keeps capabilities
// only in its permitted set cannot select a folder that only they would
// search, and neither can a Git it starts that does not regain them.
func TestGitChdirEffectiveCredentials(t *testing.T) {
	for _, without := range []string{"", "proc", "proc thread"} {
		t.Run("without-"+strings.ReplaceAll(without, " ", "-"), func(t *testing.T) {
			runAsRoot(t, without, "TestGitChdirEffectiveCredentialsAsRoot")
		})
	}
}

func TestGitChdirEffectiveCredentialsAsRoot(t *testing.T) {
	skipUnlessRoot(t)
	// Go documents that a binary that uses cgo, as a race build does,
	// cannot make a system call on every thread.
	if _, _, errno := syscall.AllThreadsSyscall(unix.SYS_GETPID, 0, 0, 0); errno == unix.ENOTSUP {
		t.Skip("syscall.AllThreadsSyscall is unavailable in this binary")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(base, "tree")
	makeRepo(t, tree)
	writeFile(t, filepath.Join(tree, ".git", "config"), "[core]\n\trepositoryformatversion = 0\n\tworktree = ../../locked\n")
	t.Setenv("GIT_CEILING_DIRECTORIES", base)
	folder := mkdir(t, filepath.Join(tree, "app"))
	locked := mkdir(t, filepath.Join(base, "locked"))
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o700) })
	git, gitErr := exec.LookPath("git")
	// requireRoot checks the Git root and workspace mount, and that Git
	// selects the same working tree or fails.
	requireRoot := func(t *testing.T, want string) {
		t.Helper()
		if got, err := GitRoot(folder); got != want || err != nil {
			t.Errorf("GitRoot = %q, %v; want %q", got, err, want)
		}
		mount := want
		if mount == "" {
			mount = folder
		}
		resolved, err := Resolve(folder, false, false, Options{MountGitRoot: true, Platform: "linux"})
		if err != nil || resolved.WorkspaceMount.Source != mount {
			t.Errorf("Resolve mount = %q, %v; want %q", resolved.WorkspaceMount.Source, err, mount)
		}
		if gitErr != nil {
			return
		}
		cmd := exec.Command(git, "rev-parse", "--show-cdup")
		cmd.Dir = folder
		out, err := cmd.CombinedOutput()
		// Upstream resolves the trimmed cdup from the workspace folder.
		cdup := strings.TrimSpace(string(out))
		if !filepath.IsAbs(cdup) {
			cdup = filepath.Join(folder, cdup)
		}
		var exit *exec.ExitError
		switch {
		case want == "" && (!errors.As(err, &exit) || exit.ExitCode() != 128):
			t.Errorf("git rev-parse = %v, %q; want exit 128", err, out)
		case want != "" && (err != nil || filepath.Clean(cdup) != want):
			t.Errorf("git rev-parse = %v, %q; want %q", err, out, want)
		}
	}

	t.Run("effective", func(t *testing.T) {
		requireRoot(t, locked)
	})
	t.Run("permitted", func(t *testing.T) {
		// Git, started as root, would regain the capabilities; these stop
		// it, as they stop any program the process starts.
		allThreads(t, unix.SYS_PRCTL, unix.PR_SET_SECUREBITS, secbitNoroot|secbitNorootLocked, 0)
		allThreads(t, unix.SYS_PRCTL, unix.PR_CAP_AMBIENT, unix.PR_CAP_AMBIENT_CLEAR_ALL, 0)
		header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
		var data [2]unix.CapUserData
		if err := unix.Capget(&header, &data[0]); err != nil {
			t.Fatal(err)
		}
		data[0].Effective, data[1].Effective = 0, 0
		allThreads(t, unix.SYS_CAPSET, uintptr(unsafe.Pointer(&header)), uintptr(unsafe.Pointer(&data[0])), 0)
		runtime.KeepAlive(&header)
		runtime.KeepAlive(&data)
		header.Pid = 0
		if err := unix.Capget(&header, &data[0]); err != nil || data[0].Effective|data[1].Effective != 0 || data[0].Permitted == 0 {
			t.Fatalf("capabilities = %+v, %v; want permitted and no effective", data, err)
		}
		// The real credentials still search the folder; the effective ones
		// do not.
		if err := unix.Faccessat(unix.AT_FDCWD, locked, unix.X_OK, 0); err != nil {
			t.Fatalf("access with the real credentials = %v; want nil", err)
		}
		if got, err := gitChdir(base, "locked"); got != "" || !errors.Is(err, unix.EACCES) {
			t.Errorf("gitChdir = %q, %v; want \"\", EACCES", got, err)
		}
		requireRoot(t, "")
	})
}

// allThreads makes a system call on every thread of the process.
func allThreads(t *testing.T, trap, a1, a2, a3 uintptr) {
	t.Helper()
	if _, _, errno := syscall.AllThreadsSyscall(trap, a1, a2, a3); errno != 0 {
		t.Fatalf("AllThreadsSyscall(%d) = %v", trap, errno)
	}
}
