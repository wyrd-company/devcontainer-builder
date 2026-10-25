//go:build linux && (amd64 || arm64)

// relationships:
//   references: [git_chdir_clone_linux, git_core_worktree_proc_linux_test]

package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// openPath opens path as gitChdir opens a folder it selects.
func openPath(t *testing.T, path string) int {
	t.Helper()
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unix.Close(fd) })
	return fd
}

// requireThreadEnded waits for the thread tid to leave this process. The
// kernel clears the thread ID before the thread leaves the process's thread
// list, so the wait is bounded rather than immediate.
func requireThreadEnded(t *testing.T, tid int) {
	t.Helper()
	path := "/proc/self/task/" + strconv.Itoa(tid)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(time.Millisecond) {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("thread %d is still in this process", tid)
		}
	}
}

// The thread names a folder by its physical path, as Git's getcwd does.
func TestGitCloneCwdNamesFolder(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	want := mkdir(t, filepath.Join(base, "real", "deep"))
	if err := os.Symlink(filepath.Join(base, "real"), filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	fd := openPath(t, filepath.Join(base, "link", "deep"))
	if got, isolated, err := gitCloneCwd(fd); got != want || !isolated || err != nil {
		t.Fatalf("gitCloneCwd = %q, %v, %v; want %q, true, nil", got, isolated, err, want)
	}
}

// The thread is a thread of this process, started with every signal
// blocked, and it ends. The calling thread's signal mask and the process's
// working folder do not change.
func TestGitCloneThread(t *testing.T) {
	folder := t.TempDir()
	fd := openPath(t, folder)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	var before, after unix.Sigset_t
	if err := unix.PthreadSigmask(unix.SIG_BLOCK, nil, &before); err != nil {
		t.Fatal(err)
	}
	state, tid := gitCloneMapped(fd)
	if err := unix.PthreadSigmask(unix.SIG_BLOCK, nil, &after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("calling thread's signal mask = %x; want %x", after.Val[0], before.Val[0])
	}
	if tid <= 0 || tid == unix.Gettid() || tid == unix.Getpid() {
		t.Fatalf("thread ID = %d; want a new thread (caller %d, process %d)", tid, unix.Gettid(), unix.Getpid())
	}
	if state.pid != int64(unix.Getpid()) {
		t.Errorf("thread's process ID = %d; want %d", state.pid, unix.Getpid())
	}
	if state.mask != gitCloneBlocked {
		t.Errorf("thread's signal mask = %x; want %x", state.mask, uint64(gitCloneBlocked))
	}
	if state.tid != 0 {
		t.Errorf("thread ID after the thread ended = %d; want 0", state.tid)
	}
	if got, err := state.cwd(); got != folder || err != nil {
		t.Errorf("cwd = %q, %v; want %q", got, err, folder)
	}
	requireThreadEnded(t, tid)
	if got, err := os.Getwd(); got != cwd || err != nil {
		t.Errorf("Getwd = %q, %v; want %q", got, err, cwd)
	}
}

// The thread's chdir and getcwd errors are its result.
func TestGitCloneCwdErrors(t *testing.T) {
	base := t.TempDir()
	file := filepath.Join(base, "file")
	writeFile(t, file, "")
	removed := mkdir(t, filepath.Join(base, "removed"))
	removedFd := openPath(t, removed)
	if err := os.Remove(removed); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		fd   int
		want error
	}{
		{"closed", -1, unix.EBADF},
		{"file", openPath(t, file), unix.ENOTDIR},
		{"removed", removedFd, unix.ENOENT},
	}
	if os.Geteuid() != 0 {
		closed := mkdir(t, filepath.Join(base, "closed"))
		if err := os.Chmod(closed, 0o600); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, struct {
			name string
			fd   int
			want error
		}{"unsearchable", openPath(t, closed), unix.EACCES})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, isolated, err := gitCloneCwd(c.fd); got != "" || !isolated || !errors.Is(err, c.want) {
				t.Fatalf("gitCloneCwd = %q, %v, %v; want \"\", true, %v", got, isolated, err, c.want)
			}
		})
	}
}

// Threads run concurrently while the collector runs and the process
// receives signals; each names its folder, ends and leaves the process's
// working folder.
func TestGitCloneCwdConcurrent(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	const goroutines, calls = 8, 125
	folders := make([]string, goroutines)
	fds := make([]int, goroutines)
	for i := range folders {
		folders[i] = mkdir(t, filepath.Join(base, strconv.Itoa(i)))
		fds[i] = openPath(t, folders[i])
	}
	stop := make(chan struct{})
	var load sync.WaitGroup
	load.Add(2)
	go func() {
		defer load.Done()
		for {
			select {
			case <-stop:
				return
			default:
				runtime.GC()
			}
		}
	}()
	go func() {
		defer load.Done()
		for {
			select {
			case <-stop:
				return
			default:
				// Go ignores SIGURG unless it preempts a goroutine.
				_ = syscall.Kill(os.Getpid(), syscall.SIGURG)
			}
		}
	}()
	tids := make(chan int, goroutines*calls)
	errs := make(chan error, goroutines*calls)
	var work sync.WaitGroup
	for i := range goroutines {
		work.Add(1)
		go func() {
			defer work.Done()
			for range calls {
				state, tid := gitCloneRun(fds[i])
				tids <- tid
				if got, err := state.cwd(); got != folders[i] || err != nil {
					errs <- errors.New("cwd = " + strconv.Quote(got) + ": " + errString(err))
				}
			}
		}()
	}
	work.Wait()
	close(stop)
	load.Wait()
	close(tids)
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	for tid := range tids {
		if tid <= 0 {
			t.Fatalf("thread did not start: %d", tid)
		}
		requireThreadEnded(t, tid)
	}
	if got, err := os.Getwd(); got != cwd || err != nil {
		t.Errorf("Getwd = %q, %v; want %q", got, err, cwd)
	}
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// A thread that the kernel ends before it finishes is still waited for, and
// its folder is an error.
func TestGitCloneCwdThreadEnded(t *testing.T) {
	out := runWithout(t, "fchdir", "^TestGitCloneCwdThreadKilled$", "")
	if !strings.Contains(out, "--- PASS: TestGitCloneCwdThreadKilled (") {
		t.Fatalf("TestGitCloneCwdThreadKilled did not pass:\n%s", out)
	}
}

// TestGitCloneCwdThreadKilled runs where fchdir ends its thread.
func TestGitCloneCwdThreadKilled(t *testing.T) {
	if !slices.Contains(strings.Fields(os.Getenv(withoutEnv)), "fchdir") {
		t.Skip("runs where fchdir ends its thread")
	}
	fd := openPath(t, t.TempDir())
	for range 2 {
		state, tid := gitCloneRun(fd)
		if tid <= 0 {
			t.Fatalf("thread did not start: %d", tid)
		}
		if state.pid != int64(unix.Getpid()) || state.result != 0 {
			t.Fatalf("thread state = pid %d, result %d; want pid %d, result 0", state.pid, state.result, unix.Getpid())
		}
		if got, err := state.cwd(); got != "" || !errors.Is(err, unix.EINVAL) {
			t.Fatalf("cwd = %q, %v; want \"\", EINVAL", got, err)
		}
	}
}

// A folder outside the process's root is an error, as Git's getcwd reports
// it.
func TestGitCloneCwdUnreachable(t *testing.T) {
	out := runWithout(t, "proc", "^TestGitCloneCwdDetached$", "")
	if !strings.Contains(out, "--- PASS: TestGitCloneCwdDetached (") {
		t.Fatalf("TestGitCloneCwdDetached did not pass:\n%s", out)
	}
}

// TestGitCloneCwdDetached runs in a process with its own mount namespace.
func TestGitCloneCwdDetached(t *testing.T) {
	if !slices.Contains(strings.Fields(os.Getenv(withoutEnv)), "proc") {
		t.Skip("runs in a process with its own mount namespace")
	}
	mount := t.TempDir()
	if err := unix.Mount("tmpfs", mount, "tmpfs", 0, ""); err != nil {
		t.Fatal(err)
	}
	folder := mkdir(t, filepath.Join(mount, "folder"))
	fd := openPath(t, folder)
	if err := unix.Unmount(mount, unix.MNT_DETACH); err != nil {
		t.Fatal(err)
	}
	if got, isolated, err := gitCloneCwd(fd); got != "" || !isolated || !errors.Is(err, unix.ENOENT) {
		t.Fatalf("gitCloneCwd = %q, %v, %v; want \"\", true, ENOENT", got, isolated, err)
	}
}
