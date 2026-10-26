//go:build darwin || (linux && gitthreadcwd)

// relationships:
//   references: [git_global_config, git_config_include, git_config_cwd_unix, git_chdir_thread_darwin, git_chdir_thread_linux]

package workspace

import (
	"os"
	"path/filepath"
	"runtime"

	"golang.org/x/sys/unix"
)

// gitConfigCwd is Git's working folder in one of its config reads. Git names
// a config file relative to its working folder, as given, so a relative name
// has its own path length limit and is not joined to the folder's path. When
// a relative name is first looked up, a thread of its own changes its working
// folder to the folder, which needs only search permission on it, as Git's
// chdir does. That thread looks up each relative name of the read. If it
// cannot change to the folder, relative names are joined to its path.
type gitConfigCwd struct {
	folder  string
	started bool
	calls   chan func()
}

// newGitConfigCwd returns the working folder folder for one config read.
func newGitConfigCwd(folder string) *gitConfigCwd {
	return &gitConfigCwd{folder: folder}
}

// close ends the folder's thread.
func (c *gitConfigCwd) close() {
	if c.calls != nil {
		close(c.calls)
		c.calls = nil
	}
}

// lookup calls look with the path that names name: on the folder's thread
// for a relative name, or the absolute or joined path on the caller's thread.
func (c *gitConfigCwd) lookup(name string, look func(path string)) {
	if filepath.IsAbs(name) {
		look(name)
		return
	}
	if !c.started {
		c.started = true
		c.calls = gitConfigCwdThread(c.folder)
	}
	if c.calls == nil {
		look(c.folder + string(os.PathSeparator) + name)
		return
	}
	done := make(chan struct{})
	c.calls <- func() {
		defer close(done)
		look(name)
	}
	<-done
}

// gitConfigCwdThread starts a thread whose own working folder is folder, and
// returns the channel that it runs calls from until the channel is closed, or
// nil if the thread cannot change to folder.
func gitConfigCwdThread(folder string) chan func() {
	calls := make(chan func())
	entered := make(chan bool, 1)
	go func() {
		runtime.LockOSThread()
		if err := gitThreadChdir(folder); err != nil {
			entered <- false
		} else {
			entered <- true
			for call := range calls {
				call()
			}
		}
		// A thread that cannot return to the process's working folder ends
		// with this goroutine.
		if gitThreadReset() == nil {
			runtime.UnlockOSThread()
		}
	}()
	if !<-entered {
		return nil
	}
	return calls
}

// access follows access(name, R_OK), which checks the real credentials.
func (c *gitConfigCwd) access(name string) (err error) {
	c.lookup(name, func(path string) { err = gitConfigAccessAt(unix.AT_FDCWD, path) })
	return err
}

// open opens name for reading with the effective credentials, as fopen does.
func (c *gitConfigCwd) open(name string) (file *os.File, err error) {
	c.lookup(name, func(path string) { file, err = gitConfigOpenAt(unix.AT_FDCWD, path, name) })
	return file, err
}
