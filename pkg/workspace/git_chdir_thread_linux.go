//go:build linux && gitthreadcwd

// relationships:
//   references: [git_config_cwd_thread, git_chdir_thread_darwin]

package workspace

import "golang.org/x/sys/unix"

// Builds with the gitthreadcwd tag look up relative config file names as macOS
// builds do, from a thread's own working folder, so that the tests run that
// lookup on Linux. Release builds do not use this tag.

// gitThreadChdir gives the calling thread a working folder of its own and
// changes it to path.
func gitThreadChdir(path string) error {
	if err := unix.Unshare(unix.CLONE_FS); err != nil {
		return err
	}
	return unix.Chdir(path)
}

// gitThreadReset reports that the thread cannot return to the process's
// working folder: a thread that unshared it cannot share it again.
func gitThreadReset() error {
	return unix.EINVAL
}
