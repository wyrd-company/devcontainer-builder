//go:build linux || darwin

// relationships:
//   references: [git_config_cwd_linux, git_config_cwd_thread]

package workspace

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// gitConfigAccessAt follows access(path, R_OK) from the folder that fd names,
// which checks the real credentials.
func gitConfigAccessAt(fd int, path string) error {
	return unix.Faccessat(fd, path, unix.R_OK, 0)
}

// gitConfigOpenAt opens path, from the folder that fd names, for reading with
// the effective credentials, as fopen does. name names the file in an error.
// Opening a FIFO waits for a writer.
func gitConfigOpenAt(fd int, path, name string) (*os.File, error) {
	for {
		file, err := unix.Openat(fd, path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return nil, &os.PathError{Op: "open", Path: name, Err: err}
		}
		return os.NewFile(uintptr(file), name), nil
	}
}
