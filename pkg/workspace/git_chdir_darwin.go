// relationships:
//   references: [git_work_tree, git_chdir_thread_darwin]

package workspace

import (
	"runtime"

	"golang.org/x/sys/unix"
)

// gitChdir returns the physical folder that chdir(dir) then chdir(value)
// selects. A thread of its own makes Git's two chdir calls and reads its
// working folder as Git does, so each lookup has the limits and permission
// checks of Git's. The process's working folder does not change.
func gitChdir(dir, value string) (string, error) {
	type result struct {
		path string
		err  error
	}
	done := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		path, err := gitThreadCwd(dir, value)
		// A thread that cannot return to the process's working folder ends
		// with this goroutine.
		if gitThreadReset() == nil {
			runtime.UnlockOSThread()
		}
		done <- result{path, err}
	}()
	r := <-done
	return r.path, r.err
}

// gitThreadCwd changes the calling thread to dir and then to value, and
// returns its working folder.
func gitThreadCwd(dir, value string) (string, error) {
	if err := gitThreadChdir(dir); err != nil {
		return "", err
	}
	if err := gitThreadChdir(value); err != nil {
		return "", err
	}
	// Git also fails when its working folder reaches PATH_MAX, as it then
	// resolves that path.
	return unix.Getwd()
}
