//go:build !linux && !darwin

// relationships:
//   references: [git_work_tree]

package workspace

import (
	"os"
	"syscall"
)

// gitChdir returns the physical folder that chdir(dir) then chdir(value)
// selects. value is looked up from the physical folder that dir names, so the
// two lookups have separate symbolic link limits, as Git's chdir calls do.
// These hosts are not release targets: the joined path shares the host's path
// length limit, which Git's separate lookups do not.
func gitChdir(dir, value string) (string, error) {
	physical, err := gitPhysicalPath(dir)
	if err != nil {
		return "", err
	}
	path := physical + string(os.PathSeparator) + value
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", syscall.ENOTDIR
	}
	if err := syscall.Access(path, 1); err != nil {
		return "", err
	}
	return gitPhysicalPath(path)
}
