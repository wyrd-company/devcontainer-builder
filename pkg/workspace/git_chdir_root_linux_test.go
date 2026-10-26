// relationships:
//   references: [git_chdir_linux, git_chdir_walk_linux, git_core_worktree_proc_linux_test]

package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// runAsRoot runs the test name in a process that is root of its own user
// namespace, without what without lists, and checks that it passed. A
// test that reports it cannot run there skips this one.
func runAsRoot(t *testing.T, without, name string) {
	t.Helper()
	out := runWithout(t, strings.TrimSpace("root "+without), "^"+name+"$", "")
	if strings.Contains(out, "--- SKIP: "+name+" (") {
		t.Skipf("%s skipped:\n%s", name, out)
	}
	if !strings.Contains(out, "--- PASS: "+name+" (") {
		t.Fatalf("%s did not pass:\n%s", name, out)
	}
	if strings.Contains(out, "--- SKIP: "+name+"/") {
		t.Skipf("%s passed with a skipped case:\n%s", name, out)
	}
}

// skipUnlessRoot skips a test that runs only as root of its own user
// namespace.
func skipUnlessRoot(t *testing.T) {
	t.Helper()
	if !slices.Contains(strings.Fields(os.Getenv(withoutEnv)), "root") {
		t.Skip("runs as root of its own user namespace")
	}
}

// bindMount mounts source at target until the test ends.
func bindMount(t *testing.T, source, target string) {
	t.Helper()
	if err := unix.Mount(source, target, "", unix.MS_BIND, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unix.Unmount(target, unix.MNT_DETACH) })
}

// The walk crosses mounts as the kernel's lookup does: into a mount, out of
// a mount's root with .., and over a folder another mount covers.
func TestGitWalkCwdMounts(t *testing.T) {
	runAsRoot(t, "", "TestGitWalkCwdMountsAsRoot")
}

func TestGitWalkCwdMountsAsRoot(t *testing.T) {
	skipUnlessRoot(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := mkdir(t, filepath.Join(base, "a"))
	mkdir(t, filepath.Join(a, "b", "c", "d"))
	m := mkdir(t, filepath.Join(base, "m"))
	over := mkdir(t, filepath.Join(base, "over", "x"))
	mkdir(t, filepath.Join(base, "cover", "y"))
	bindMount(t, filepath.Join(a, "b"), m)
	bindMount(t, filepath.Join(base, "cover"), filepath.Dir(over))
	if err := os.Symlink(filepath.Join(m, "c"), filepath.Join(a, "tom")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../m/c/d", filepath.Join(a, "rel")); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ dir, value string }{
		{a, "b/c/d"},
		{m, "c/d/../.."},
		{m, ".."},
		{filepath.Join(m, "c"), "../../a/b/c"},
		{a, "tom/.."},
		{a, "rel/../../.."},
		{filepath.Join(base, "over"), "y/.."},
		{filepath.Join(base, "over"), "."},
		{filepath.Join(base, "over"), "x"},
	}
	for _, c := range cases {
		t.Run(strings.TrimPrefix(c.dir, base)+"+"+c.value, func(t *testing.T) {
			requireWalkMatchesKernel(t, c.dir, c.value)
		})
	}
	// A folder of another file system with the same inode number is not
	// the folder the kernel opened.
	t.Run("other-file-system", func(t *testing.T) {
		first := mkdir(t, filepath.Join(base, "first"))
		second := mkdir(t, filepath.Join(base, "second"))
		for _, folder := range []string{first, second} {
			if err := unix.Mount("tmpfs", folder, "tmpfs", 0, ""); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { unix.Unmount(folder, unix.MNT_DETACH) })
		}
		var a, b unix.Stat_t
		if err := unix.Stat(first, &a); err != nil {
			t.Fatal(err)
		}
		if err := unix.Stat(second, &b); err != nil {
			t.Fatal(err)
		}
		if a.Ino != b.Ino || a.Dev == b.Dev {
			t.Fatalf("file system roots = inode %d device %d, inode %d device %d; want one inode on two devices", a.Ino, a.Dev, b.Ino, b.Dev)
		}
		target, _, err := kernelName(t, base, "first")
		if err != nil {
			t.Fatal(err)
		}
		if got, err := gitWalkCwd(base, "second", target); got != "" || !errors.Is(err, unix.ESTALE) {
			t.Fatalf("gitWalkCwd = %q, %v; want \"\", ESTALE", got, err)
		}
	})
	// The same folder seen through another mount is not the folder the
	// kernel opened.
	t.Run("other-mount", func(t *testing.T) {
		target, name, err := kernelName(t, base, "m")
		if err != nil || name != m {
			t.Fatalf("kernel name = %q, %v; want %q", name, err, m)
		}
		if got, err := gitWalkCwd(a, "b", target); got != "" || !errors.Is(err, unix.ESTALE) {
			t.Fatalf("gitWalkCwd = %q, %v; want \"\", ESTALE", got, err)
		}
	})
}

// fsCasefoldFl is the kernel's FS_CASEFOLD_FL inode flag.
const fsCasefoldFl = 0x40000000

// A folder whose name the file system matches without case is named as it
// is stored, as the kernel names it, wherever /proc or a thread that
// unshares its working folder is available.
func TestGitChdirCaseInsensitive(t *testing.T) {
	for _, without := range []string{"", "thread", "proc"} {
		t.Run("without-"+without, func(t *testing.T) {
			runAsRoot(t, without, "TestGitChdirCaseInsensitiveAsRoot")
		})
	}
}

func TestGitChdirCaseInsensitiveAsRoot(t *testing.T) {
	skipUnlessRoot(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	folder := mkdir(t, filepath.Join(base, "casefold"))
	if err := unix.Mount("tmpfs", folder, "tmpfs", 0, "casefold"); err != nil {
		t.Skipf("tmpfs without case folding: %v", err)
	}
	t.Cleanup(func() { unix.Unmount(folder, unix.MNT_DETACH) })
	ci := mkdir(t, filepath.Join(folder, "ci"))
	fd, err := unix.Open(ci, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	flags, err := unix.IoctlGetUint32(fd, unix.FS_IOC_GETFLAGS)
	if err == nil {
		err = unix.IoctlSetPointerInt(fd, unix.FS_IOC_SETFLAGS, int(flags|fsCasefoldFl))
	}
	unix.Close(fd)
	if err != nil {
		t.Fatal(err)
	}
	want := mkdir(t, filepath.Join(ci, "stored"))
	if got, err := gitChdir(folder, "ci/STORED"); got != want || err != nil {
		t.Fatalf("gitChdir = %q, %v; want %q", got, err, want)
	}
}

// seccompNotif and seccompNotifResp are the kernel's struct seccomp_notif
// and struct seccomp_notif_resp.
type seccompNotif struct {
	ID    uint64
	PID   uint32
	Flags uint32
	Nr    int32
	Arch  uint32
	IP    uint64
	Args  [6]uint64
}

type seccompNotifResp struct {
	ID    uint64
	Val   int64
	Error int32
	Flags uint32
}

// holdLookups makes every thread wait for the returned listener when it
// calls fchdir or reads a symbolic link from a descriptor, as the lookups
// that name a folder without /proc do.
func holdLookups(t *testing.T) int {
	t.Helper()
	// The filter reads the low half of the descriptor argument.
	var fdcwd int32 = unix.AT_FDCWD
	atFdcwd := uint32(fdcwd)
	filter := []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 4, Jf: 0, K: unix.SYS_FCHDIR},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 0, Jf: 2, K: unix.SYS_READLINKAT},
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 16},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 0, Jf: 1, K: atFdcwd},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_USER_NOTIF},
	}
	prog := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	flags := unix.SECCOMP_FILTER_FLAG_TSYNC | unix.SECCOMP_FILTER_FLAG_TSYNC_ESRCH | unix.SECCOMP_FILTER_FLAG_NEW_LISTENER
	listener, _, errno := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, uintptr(flags), uintptr(unsafe.Pointer(&prog)))
	runtime.KeepAlive(filter)
	if errno != 0 {
		t.Fatalf("seccomp listener: %v", errno)
	}
	t.Cleanup(func() { unix.Close(int(listener)) })
	return int(listener)
}

// heldLookup is a lookup that waits at the listener.
type heldLookup struct {
	listener int
	notif    seccompNotif
	done     chan lookupResult
}

type lookupResult struct {
	path string
	err  error
}

// startHeldLookup starts gitChdir(dir, value) and waits until a thread that
// it uses waits at the listener.
func startHeldLookup(t *testing.T, listener int, dir, value string) *heldLookup {
	t.Helper()
	h := &heldLookup{listener: listener, done: make(chan lookupResult, 1)}
	go func() {
		path, err := gitChdir(dir, value)
		h.done <- lookupResult{path, err}
	}()
	h.notif = h.receive(t)
	if h.notif.Nr != unix.SYS_FCHDIR && h.notif.Nr != unix.SYS_READLINKAT {
		t.Fatalf("held system call %d", h.notif.Nr)
	}
	return h
}

// receive returns the next notification. The lookup must not finish first.
func (h *heldLookup) receive(t *testing.T) seccompNotif {
	t.Helper()
	for {
		select {
		case r := <-h.done:
			t.Fatalf("lookup finished without waiting: %q, %v", r.path, r.err)
		default:
		}
		fds := []unix.PollFd{{Fd: int32(h.listener), Events: unix.POLLIN}}
		if n, err := unix.Poll(fds, 10); err != nil && !errors.Is(err, unix.EINTR) {
			t.Fatal(err)
		} else if n == 0 {
			continue
		}
		var n seccompNotif
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(h.listener), unix.SECCOMP_IOCTL_NOTIF_RECV, uintptr(unsafe.Pointer(&n)))
		if errno != 0 {
			t.Fatal(errno)
		}
		return n
	}
}

// release lets the held system call continue and returns the lookup's
// result. A signal can end the wait, after which the thread's restarted call
// waits again.
func (h *heldLookup) release(t *testing.T) lookupResult {
	t.Helper()
	for {
		resp := seccompNotifResp{ID: h.notif.ID, Flags: unix.SECCOMP_USER_NOTIF_FLAG_CONTINUE}
		_, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(h.listener), unix.SECCOMP_IOCTL_NOTIF_SEND, uintptr(unsafe.Pointer(&resp)))
		if errno == 0 {
			return <-h.done
		}
		if errno != unix.ENOENT {
			t.Fatal(errno)
		}
		next := h.receive(t)
		if next.PID != h.notif.PID || next.Nr != h.notif.Nr {
			t.Fatalf("restarted call = thread %d call %d; want thread %d call %d", next.PID, next.Nr, h.notif.PID, h.notif.Nr)
		}
		h.notif = next
	}
}

// effectiveCaps returns the effective capabilities of the thread tid.
func effectiveCaps(t *testing.T, tid uint32) uint64 {
	t.Helper()
	header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3, Pid: int32(tid)}
	var data [2]unix.CapUserData
	if err := unix.Capget(&header, &data[0]); err != nil {
		t.Fatal(err)
	}
	return uint64(data[1].Effective)<<32 | uint64(data[0].Effective)
}

// A credential change that every thread of the process makes while a lookup
// waits applies to the lookup's later system calls, with or without the
// thread that unshares its working folder.
func TestGitChdirHeldCredentials(t *testing.T) {
	for _, without := range []string{"proc", "proc thread"} {
		t.Run("without-"+strings.ReplaceAll(without, " ", "-"), func(t *testing.T) {
			runAsRoot(t, without, "TestGitChdirHeldCredentialsAsRoot")
		})
	}
}

func TestGitChdirHeldCredentialsAsRoot(t *testing.T) {
	skipUnlessRoot(t)
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	locked := mkdir(t, filepath.Join(base, "locked"))
	want := mkdir(t, filepath.Join(locked, "x"))
	if err := os.Symlink("locked/x", filepath.Join(base, "link")); err != nil {
		t.Fatal(err)
	}
	// Only a capability that overrides file permissions passes these.
	for _, folder := range []string{want, locked} {
		if err := os.Chmod(folder, 0); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		os.Chmod(locked, 0o700)
		os.Chmod(want, 0o700)
	})
	listener := holdLookups(t)

	t.Run("kept", func(t *testing.T) {
		h := startHeldLookup(t, listener, base, "link")
		if caps := effectiveCaps(t, h.notif.PID); caps == 0 {
			t.Fatalf("held thread has no capabilities")
		}
		if r := h.release(t); r.path != want || r.err != nil {
			t.Fatalf("gitChdir = %q, %v; want %q", r.path, r.err, want)
		}
	})
	t.Run("dropped", func(t *testing.T) {
		// Go documents that a binary that uses cgo, as a race build does,
		// cannot make a system call on every thread.
		if _, _, errno := syscall.AllThreadsSyscall(unix.SYS_GETPID, 0, 0, 0); errno == unix.ENOTSUP {
			t.Skip("syscall.AllThreadsSyscall is unavailable in this binary")
		}
		h := startHeldLookup(t, listener, base, "link")
		header := unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
		var data [2]unix.CapUserData
		_, _, errno := syscall.AllThreadsSyscall(unix.SYS_CAPSET, uintptr(unsafe.Pointer(&header)), uintptr(unsafe.Pointer(&data[0])), 0)
		runtime.KeepAlive(&header)
		runtime.KeepAlive(&data)
		if errno != 0 {
			t.Fatalf("AllThreadsSyscall(capset) = %v", errno)
		}
		if caps := effectiveCaps(t, h.notif.PID); caps != 0 {
			t.Errorf("held thread's capabilities after the change = %x; want 0", caps)
		}
		if r := h.release(t); r.path != "" || !errors.Is(r.err, unix.EACCES) {
			t.Fatalf("gitChdir = %q, %v; want \"\", EACCES", r.path, r.err)
		}
	})
}
