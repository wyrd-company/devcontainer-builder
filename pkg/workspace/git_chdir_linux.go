// relationships:
//   references: [git_work_tree, git_chdir_clone_linux]

package workspace

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"golang.org/x/sys/unix"
)

// gitChdir returns the physical folder that chdir(dir) then chdir(value)
// selects. Each lookup starts from the folder the previous one selected and
// has its own symbolic link and path length limits, as Git's two chdir calls
// do. The kernel resolves symbolic links before .. components.
func gitChdir(dir, value string) (string, error) {
	const flags = unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC
	parent, err := unix.Open(dir, flags, 0)
	if err != nil {
		return "", err
	}
	defer unix.Close(parent)
	target, err := unix.Openat(parent, value, flags, 0)
	if err != nil {
		return "", err
	}
	defer unix.Close(target)
	// chdir also needs search permission on the folder it selects, which an
	// O_PATH open does not check.
	if err := unix.Faccessat(parent, value, unix.X_OK, 0); err != nil {
		return "", err
	}
	// Git reads its working folder, which the kernel names from the folder
	// itself, as it names the descriptor's folder.
	if path, err := os.Readlink("/proc/self/fd/" + strconv.Itoa(target)); err == nil && filepath.IsAbs(path) {
		return path, nil
	}
	if path, isolated, err := gitDescriptorCwd(target); isolated {
		return path, err
	}
	// A seccomp profile that blocks unshare, as Docker's default profile
	// does, can still allow a new thread with its own working folder.
	if path, isolated, err := gitCloneCwd(target); isolated {
		return path, err
	}
	// Without /proc or a thread of its own, the joined path is resolved,
	// which also limits the length of each folder the value passes.
	return gitPhysicalPath(dir + "/" + value)
}

// gitDescriptorCwd returns the working folder of a thread that changes to
// the folder fd names, as Git's getcwd reads it. The thread has a working
// folder of its own, which it cannot give back, so it ends with its
// goroutine. isolated is false if the thread cannot have its own working
// folder.
func gitDescriptorCwd(fd int) (path string, isolated bool, err error) {
	type result struct {
		path     string
		isolated bool
		err      error
	}
	done := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		if err := unix.Unshare(unix.CLONE_FS); err != nil {
			done <- result{}
			return
		}
		if err := unix.Fchdir(fd); err != nil {
			done <- result{"", true, err}
			return
		}
		path, err := unix.Getwd()
		done <- result{path, true, err}
	}()
	r := <-done
	return r.path, r.isolated, r.err
}
