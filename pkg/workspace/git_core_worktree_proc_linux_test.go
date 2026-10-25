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
// hides /proc, and "thread" stops each thread from having its own working
// folder.
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
}

func withoutExit(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(3)
}

// denyUnshare makes unshare fail with EPERM in every thread, as a seccomp
// profile that blocks it does. Go makes only native system calls, so the
// filter does not check the architecture.
func denyUnshare() error {
	filter := []unix.SockFilter{
		{Code: unix.BPF_LD | unix.BPF_W | unix.BPF_ABS, K: 0},
		{Code: unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K, Jt: 0, Jf: 1, K: unix.SYS_UNSHARE},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ERRNO | uint32(unix.EPERM)},
		{Code: unix.BPF_RET | unix.BPF_K, K: unix.SECCOMP_RET_ALLOW},
	}
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

// Without /proc, a thread of its own names the folder a relative
// core.worktree selects. The core.worktree tests run again, Git root and
// Resolve mount alike, in a process whose /proc is hidden.
func TestGitRootCoreWorktreeWithoutProc(t *testing.T) {
	out := runWithout(t, "proc", "")
	requirePassedWithout(t, out, append(coreWorktreeLimits, "TestGitRootCoreWorktreeIntermediatePathMax/passes"))
}

// Where threads cannot have their own working folders, as under a seccomp
// profile that blocks unshare, /proc names the folder.
func TestGitRootCoreWorktreeWithoutThread(t *testing.T) {
	out := runWithout(t, "thread", "")
	requirePassedWithout(t, out, append(coreWorktreeLimits, "TestGitRootCoreWorktreeIntermediatePathMax/passes"))
}

// Without /proc or a thread of its own, the joined path is resolved, so each
// folder the value passes must have a physical path within PATH_MAX. The
// layout that only Git's kernel lookup reaches does not run.
func TestGitRootCoreWorktreeWithoutProcOrThread(t *testing.T) {
	out := runWithout(t, "proc thread", "^TestGitRootCoreWorktreeIntermediatePathMax$/^passes$")
	requirePassedWithout(t, out, coreWorktreeLimits)
}

// runWithout runs this package's core.worktree tests, except skip, in a
// process without what without lists, and returns their verbose output. A
// process that hides /proc has its own user and mount namespaces.
func runWithout(t *testing.T, without, skip string) string {
	t.Helper()
	if os.Getenv(withoutEnv) != "" {
		t.Skip("this process already runs without " + os.Getenv(withoutEnv))
	}
	args := []string{"-test.run=^TestGitRootCoreWorktree", "-test.v"}
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
