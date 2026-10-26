// relationships:
//   references: [git_chdir_linux, git_core_worktree_chdir_test, git_core_worktree_test]

package workspace

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"golang.org/x/sys/unix"
)

// withoutEnv lists what a test process removes before its tests run: "proc"
// hides /proc, "thread" stops a thread from unsharing its working folder,
// and "clone" stops a new thread from having its own working folder.
const withoutEnv = "DCB_WORKSPACE_TEST_WITHOUT"

// withoutUnavailable starts the message of a process that cannot hide /proc.
const withoutUnavailable = "without /proc unavailable: "

func init() {
	without := strings.Fields(os.Getenv(withoutEnv))
	if slices.Contains(without, "proc") {
		// The process has its own user and mount namespaces, so these mounts
		// end with it and the host's mounts do not change.
		err := unix.Mount("", "/", "", unix.MS_REC|unix.MS_PRIVATE, "")
		if err == nil {
			err = unix.Mount("tmpfs", "/proc", "tmpfs", 0, "")
		}
		if err != nil {
			prefix := "without /proc: "
			if errors.Is(err, unix.EPERM) {
				prefix = withoutUnavailable
			}
			withoutExit(prefix + err.Error())
		}
		if _, err := os.Readlink("/proc/self/fd/0"); err == nil {
			withoutExit("without /proc: /proc/self/fd is still present")
		}
	}
	if slices.Contains(without, "thread") {
		if err := denyUnshare(); err != nil {
			withoutExit("without a thread working folder: " + err.Error())
		}
		runtime.LockOSThread()
		if err := unix.Unshare(unix.CLONE_FS); !errors.Is(err, unix.EPERM) {
			withoutExit(fmt.Sprintf("without a thread working folder: unshare = %v; want EPERM", err))
		}
		runtime.UnlockOSThread()
	}
	if slices.Contains(without, "clone") {
		if err := denyThreadClone(); err != nil {
			withoutExit("without a new thread's working folder: " + err.Error())
		}
		if err := requireThreadCloneDenied(); err != nil {
			withoutExit("without a new thread's working folder: " + err.Error())
		}
	}
}

func withoutExit(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(3)
}

// denyUnshare makes unshare fail with EPERM in every thread, as a seccomp
// profile that blocks it does. Go makes only native system calls, so the
// filter does not check the architecture.
func denyUnshare() error {
	return installFilter([]unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 0, Jf: 1, K: unix.SYS_UNSHARE},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
	})
}

// denyThreadClone makes clone fail with EPERM for a thread (CLONE_THREAD)
// that does not share the process's working folder (no CLONE_FS). Go's
// threads share it, and its processes are not threads. The filter reads the
// low half of the flags, as on little-endian hosts.
func denyThreadClone() error {
	return installFilter([]unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 0, Jf: 4, K: unix.SYS_CLONE},
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 16},
		{Code: unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K, Jt: 0, Jf: 2, K: unix.CLONE_THREAD},
		{Code: unix.BPF_JMP | unix.BPF_JSET | unix.BPF_K, Jt: 1, Jf: 0, K: unix.CLONE_FS},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
	})
}

// requireThreadCloneDenied checks that the filter denies a thread without
// CLONE_FS and allows one with it. Neither probe can start a thread: without
// CLONE_SIGHAND the kernel rejects CLONE_THREAD with EINVAL after the filter
// allows it.
func requireThreadCloneDenied() error {
	probe := func(flags uintptr) error {
		_, _, errno := unix.RawSyscall6(unix.SYS_CLONE, flags, 0, 0, 0, 0, 0)
		return errno
	}
	if err := probe(unix.CLONE_THREAD); !errors.Is(err, unix.EPERM) {
		return fmt.Errorf("clone without CLONE_FS = %v; want EPERM", err)
	}
	if err := probe(unix.CLONE_THREAD | unix.CLONE_FS); !errors.Is(err, unix.EINVAL) {
		return fmt.Errorf("clone with CLONE_FS = %v; want EINVAL", err)
	}
	return nil
}

// installFilter adds a seccomp filter to every thread.
func installFilter(filter []unix.SockFilter) error {
	prog := unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return err
	}
	_, _, errno := unix.Syscall(unix.SYS_SECCOMP, unix.SECCOMP_SET_MODE_FILTER, unix.SECCOMP_FILTER_FLAG_TSYNC, uintptr(unsafe.Pointer(&prog)))
	if errno != 0 {
		return errno
	}
	return nil
}

// coreWorktreeLimits names the tests of Git's separate lookups and the
// working folder.
var coreWorktreeLimits = []string{
	"TestGitRootCoreWorktreeSelection",
	"TestGitRootCoreWorktreeChdirLimits",
	"TestGitRootCoreWorktreePathMax/physical-long",
	"TestGitRootCoreWorktreePathMax/named-long",
	"TestGitRootCoreWorktreeIntermediatePathMax/ends",
	"TestGitRootCoreWorktreeKeepsWorkingFolder",
}

// Without /proc, a thread that unshares its working folder names the folder
// a relative core.worktree selects. The core.worktree tests run again, Git
// root and Resolve mount alike, in a process whose /proc is hidden.
func TestGitRootCoreWorktreeWithoutProc(t *testing.T) {
	out := runWithout(t, "proc", coreWorktreeRun, "")
	requirePassedWithout(t, out, append(coreWorktreeLimits, "TestGitRootCoreWorktreeIntermediatePathMax/passes"))
}

// Under a seccomp profile that blocks unshare, /proc names the folder.
func TestGitRootCoreWorktreeWithoutThread(t *testing.T) {
	out := runWithout(t, "thread", coreWorktreeRun, "")
	requirePassedWithout(t, out, append(coreWorktreeLimits, "TestGitRootCoreWorktreeIntermediatePathMax/passes"))
}

// Where seccomp blocks both kinds of thread, /proc names the folder.
func TestGitRootCoreWorktreeWithoutThreads(t *testing.T) {
	out := runWithout(t, "thread clone", coreWorktreeRun, "")
	requirePassedWithout(t, out, append(coreWorktreeLimits, "TestGitRootCoreWorktreeIntermediatePathMax/passes"))
}

// Without /proc, under a seccomp profile that blocks unshare, the lookups
// are repeated one component at a time.
func TestGitRootCoreWorktreeWithoutProcOrThread(t *testing.T) {
	out := runWithout(t, "proc thread", coreWorktreeRun, "")
	requirePassedWithout(t, out, append(coreWorktreeLimits, "TestGitRootCoreWorktreeIntermediatePathMax/passes"))
}

// Without /proc, where seccomp also blocks a new thread with its own working
// folder, a thread that unshares its working folder names the folder.
func TestGitRootCoreWorktreeWithoutProcOrClone(t *testing.T) {
	out := runWithout(t, "proc clone", coreWorktreeRun, "")
	requirePassedWithout(t, out, append(coreWorktreeLimits, "TestGitRootCoreWorktreeIntermediatePathMax/passes"))
}

// Where /proc is hidden and seccomp blocks both kinds of thread, the lookups
// are repeated one component at a time, including through a folder whose
// physical path exceeds PATH_MAX.
func TestGitRootCoreWorktreeWithoutProcOrThreads(t *testing.T) {
	out := runWithout(t, "proc thread clone", coreWorktreeRun, "")
	requirePassedWithout(t, out, append(coreWorktreeLimits, "TestGitRootCoreWorktreeIntermediatePathMax/passes"))
}

// coreWorktreeRun selects the core.worktree tests.
const coreWorktreeRun = "^TestGitRootCoreWorktree"

// runWithout runs this package's tests that run selects, except skip, in a
// process without what without lists, and returns their verbose output. A
// process that hides /proc has its own user and mount namespaces.
func runWithout(t *testing.T, without, run, skip string) string {
	t.Helper()
	if os.Getenv(withoutEnv) != "" {
		t.Skip("this process already runs without " + os.Getenv(withoutEnv))
	}
	args := []string{"-test.run=" + run, "-test.v"}
	if skip != "" {
		args = append(args, "-test.skip="+skip)
	}
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), withoutEnv+"="+without)
	namespaces := slices.Contains(strings.Fields(without), "proc")
	if namespaces {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			Cloneflags:  syscall.CLONE_NEWUSER | syscall.CLONE_NEWNS,
			UidMappings: []syscall.SysProcIDMap{{ContainerID: os.Getuid(), HostID: os.Getuid(), Size: 1}},
			GidMappings: []syscall.SysProcIDMap{{ContainerID: os.Getgid(), HostID: os.Getgid(), Size: 1}},
			// The test process keeps its own user, so permissions still
			// apply; it can only mount.
			AmbientCaps: []uintptr{unix.CAP_SYS_ADMIN},
		}
	}
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		if !namespaces {
			t.Fatal(err)
		}
		t.Skipf("user and mount namespaces unavailable: %v", err)
	}
	if strings.Contains(string(out), withoutUnavailable) {
		t.Skipf("%s", out)
	}
	if err != nil {
		t.Fatalf("core.worktree tests without %s: %v\n%s", without, err, withoutFailures(out))
	}
	return string(out)
}

// requirePassedWithout checks that each named test passed, adding the tests
// that need Git or that permissions apply to where they can run.
func requirePassedWithout(t *testing.T, out string, passed []string) {
	t.Helper()
	passed = slices.Clone(passed)
	if _, err := exec.LookPath("git"); err == nil {
		passed = append(passed, "TestGitRootCoreWorktreeMatchesGit", "TestGitRootCoreWorktreeChdirMatchesGit")
	}
	if os.Geteuid() != 0 {
		passed = append(passed,
			"TestGitRootCoreWorktreeSearchOnlyRepository/discovered",
			"TestGitRootCoreWorktreeSearchOnlyRepository/explicit",
			"TestGitRootCoreWorktreeSearchOnlyFolder",
		)
	}
	for _, name := range passed {
		if !strings.Contains(out, "--- PASS: "+name+" (") {
			t.Errorf("%s did not pass", name)
		}
	}
}

// withoutFailures drops the run and pass lines of verbose test output.
func withoutFailures(out []byte) string {
	var b strings.Builder
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "=== RUN") || strings.HasPrefix(trimmed, "--- PASS") {
			continue
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}
