// relationships:
//   references: [git_work_tree]

package workspace

import (
	"os"
	"path/filepath"
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
	// itself, as it names the descriptor's folder. Without /proc, the joined
	// path is resolved, which also limits the length of each folder the value
	// passes.
	if path, err := os.Readlink("/proc/self/fd/" + strconv.Itoa(target)); err == nil && filepath.IsAbs(path) {
		return path, nil
	}
	return gitPhysicalPath(dir + "/" + value)
}
