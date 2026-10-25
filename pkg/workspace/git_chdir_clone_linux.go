//go:build linux && (amd64 || arm64)

// relationships:
//   references: [git_chdir_linux]

package workspace

import (
	"os"
	"runtime"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/unix"
)

// gitCloneFlags makes a thread of this process that shares its memory,
// descriptors and signal handlers. CLONE_FS is clear, so the thread starts
// with a copy of the process's working folder, and its chdir changes only
// that copy. The kernel stores the thread's ID in gitCloneState.tid before
// the thread runs, and clears it and wakes its futex when the thread ends.
const gitCloneFlags = unix.CLONE_VM | unix.CLONE_FILES | unix.CLONE_SIGHAND | unix.CLONE_THREAD |
	unix.CLONE_PARENT_SETTID | unix.CLONE_CHILD_CLEARTID

// The thread's system calls and arguments, for go_asm.h.
const (
	gitCloneSysClone       = unix.SYS_CLONE
	gitCloneSysGetpid      = unix.SYS_GETPID
	gitCloneSysSigprocmask = unix.SYS_RT_SIGPROCMASK
	gitCloneSysFchdir      = unix.SYS_FCHDIR
	gitCloneSysGetcwd      = unix.SYS_GETCWD
	gitCloneSysExit        = unix.SYS_EXIT
	gitCloneSigsetSize     = 8
	gitClonePathSize       = unix.PathMax
)

// gitCloneFutexWait is the kernel's FUTEX_WAIT operation, which the kernel's
// wake of a cleared thread ID matches.
const gitCloneFutexWait = 0

// gitCloneStackSize is the thread's stack. Its system calls do not use the
// stack.
const gitCloneStackSize = 64 << 10

// gitCloneBlocked is the signal mask the kernel reports with every signal
// blocked: it cannot block SIGKILL or SIGSTOP.
const gitCloneBlocked = ^uint64(0) &^ (1<<(unix.SIGKILL-1) | 1<<(unix.SIGSTOP-1))

// gitCloneState is the memory the thread reads and writes. It is mapped
// outside the Go heap and holds no Go pointers.
type gitCloneState struct {
	fd     int64
	pid    int64  // the thread's process ID
	mask   uint64 // the thread's blocked signals
	result int64  // getcwd's length, or the negative errno of fchdir or getcwd
	tid    int32
	path   [gitClonePathSize]byte
}

// gitCloneThread starts a thread that saves its process ID and signal mask,
// changes its working folder to state.fd, saves getcwd's answer and ends. It
// returns clone's result: the thread's ID, or a negative errno. The thread
// makes only system calls: it runs no Go code and uses only state and stack.
//
//go:noescape
func gitCloneThread(stack uintptr, state *gitCloneState) int64

// gitCloneCwd returns the working folder of a thread that changes to the
// folder fd names, as Git's getcwd reads it. The thread has its own copy of
// the process's working folder, so no other working folder changes, and the
// credentials of the thread that creates it; it ends before gitCloneCwd
// returns. isolated is false if the thread cannot start.
func gitCloneCwd(fd int) (path string, isolated bool, err error) {
	state, tid := gitCloneRun(fd)
	if tid <= 0 {
		return "", false, nil
	}
	path, err = state.cwd()
	return path, true, err
}

// gitCloneRun runs the thread for fd and returns a copy of its state after
// it ends, and its thread ID. The ID is not positive if the thread did not
// start.
func gitCloneRun(fd int) (state gitCloneState, tid int) {
	type result struct {
		state gitCloneState
		tid   int
	}
	done := make(chan result, 1)
	go func() {
		var r result
		r.state, r.tid = gitCloneMapped(fd)
		done <- r
	}()
	r := <-done
	return r.state, r.tid
}

// gitCloneMapped maps the thread's guard page, stack and state, runs the
// thread, waits for it to end and unmaps them. The calling goroutine's
// thread blocks every signal while it creates the thread, so the new thread
// starts with every signal blocked and never runs a Go signal handler.
func gitCloneMapped(fd int) (gitCloneState, int) {
	page := os.Getpagesize()
	stack := roundUp(gitCloneStackSize, page)
	size := page + stack + roundUp(int(unsafe.Sizeof(gitCloneState{})), page)
	mem, err := unix.Mmap(-1, 0, size, unix.PROT_NONE, unix.MAP_PRIVATE|unix.MAP_ANONYMOUS|unix.MAP_STACK)
	if err != nil {
		return gitCloneState{}, 0
	}
	defer unix.Munmap(mem)
	// The first page stays a guard below the stack.
	if err := unix.Mprotect(mem[page:], unix.PROT_READ|unix.PROT_WRITE); err != nil {
		return gitCloneState{}, 0
	}
	top := page + stack
	state := (*gitCloneState)(unsafe.Pointer(&mem[top]))
	state.fd = int64(fd)

	runtime.LockOSThread()
	var all, saved unix.Sigset_t
	for i := range all.Val {
		all.Val[i] = ^uint64(0)
	}
	if err := unix.PthreadSigmask(unix.SIG_SETMASK, &all, &saved); err != nil {
		runtime.UnlockOSThread()
		return gitCloneState{}, 0
	}
	tid := gitCloneThread(uintptr(unsafe.Pointer(&mem[top])), state)
	if unix.PthreadSigmask(unix.SIG_SETMASK, &saved, nil) == nil {
		runtime.UnlockOSThread()
	}
	// Otherwise this thread keeps every signal blocked, so it ends with
	// this goroutine.
	if tid <= 0 {
		return gitCloneState{}, 0
	}
	// The mapping stays until the kernel clears the thread ID, after which
	// the thread no longer runs in user space.
	for {
		t := atomic.LoadInt32(&state.tid)
		if t == 0 {
			break
		}
		// EAGAIN and EINTR return to the check; any other error does too,
		// after other goroutines run.
		_, _, errno := unix.Syscall6(unix.SYS_FUTEX, uintptr(unsafe.Pointer(&state.tid)), gitCloneFutexWait, uintptr(t), 0, 0, 0)
		if errno != 0 && errno != unix.EAGAIN && errno != unix.EINTR {
			runtime.Gosched()
		}
	}
	return *state, int(tid)
}

// cwd returns the working folder the thread read. It fails if the thread
// was not a thread of this process with every signal blocked, or did not
// finish.
func (s *gitCloneState) cwd() (string, error) {
	if s.pid != int64(unix.Getpid()) || s.mask != gitCloneBlocked {
		return "", unix.EINVAL
	}
	if s.result < 0 {
		return "", unix.Errno(-s.result)
	}
	// As unix.Getwd checks the kernel's answer: the length includes the NUL,
	// and an unreachable folder is not an absolute path.
	n := s.result
	if n < 1 || n > int64(len(s.path)) || s.path[n-1] != 0 {
		return "", unix.EINVAL
	}
	if s.path[0] != '/' {
		return "", unix.ENOENT
	}
	return string(s.path[:n-1]), nil
}

func roundUp(n, unit int) int {
	return (n + unit - 1) / unit * unit
}
