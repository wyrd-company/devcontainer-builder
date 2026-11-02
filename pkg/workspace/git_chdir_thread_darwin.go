// relationships:
//   references: [git_chdir_darwin, git_config_cwd_thread]

package workspace

import "golang.org/x/sys/unix"

// gitThreadChdir changes the calling thread's working folder only.
func gitThreadChdir(path string) error {
	return unix.PthreadChdir(path)
}

// gitThreadReset returns the calling thread to the process's working folder.
func gitThreadReset() error {
	return unix.PthreadFchdir(-1)
}
