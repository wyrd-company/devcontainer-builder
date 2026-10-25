// relationships:
//   references: [git_work_tree]

package workspace

import "golang.org/x/sys/unix"

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
	unix.Close(target)
	// chdir also needs search permission on the folder it selects, which an
	// O_PATH open does not check.
	if err := unix.Faccessat(parent, value, unix.X_OK, 0); err != nil {
		return "", err
	}
	return gitPhysicalPath(dir + "/" + value)
}
