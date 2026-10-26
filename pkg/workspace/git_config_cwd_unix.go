//go:build linux || darwin

// relationships:
//   references: [git_global_config, git_config_include, git_config_cwd_linux, git_config_cwd_darwin]

package workspace

import (
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// gitConfigCwd is Git's working folder in one of its config reads. Git names
// a config file relative to its working folder, as given, so a relative name
// has its own path length limit and is not joined to the folder's path. The
// folder is opened when a relative name is first looked up. If it cannot be
// opened, relative names are joined to its path.
type gitConfigCwd struct {
	folder string
	fd     int
	opened bool
}

// newGitConfigCwd returns the working folder folder for one config read.
func newGitConfigCwd(folder string) *gitConfigCwd {
	return &gitConfigCwd{folder: folder, fd: -1}
}

// close closes the folder.
func (c *gitConfigCwd) close() {
	if c.fd >= 0 {
		unix.Close(c.fd)
		c.fd = -1
	}
}

// at returns the folder descriptor and the name to look up for name.
func (c *gitConfigCwd) at(name string) (int, string) {
	if filepath.IsAbs(name) {
		return unix.AT_FDCWD, name
	}
	if !c.opened {
		c.opened = true
		if fd, err := unix.Open(c.folder, gitConfigCwdFlags|unix.O_DIRECTORY|unix.O_CLOEXEC, 0); err == nil {
			c.fd = fd
		}
	}
	if c.fd < 0 {
		return unix.AT_FDCWD, c.folder + string(os.PathSeparator) + name
	}
	return c.fd, name
}

// access follows access(name, R_OK), which checks the real credentials.
func (c *gitConfigCwd) access(name string) error {
	fd, path := c.at(name)
	return unix.Faccessat(fd, path, unix.R_OK, 0)
}

// open opens name for reading with the effective credentials, as fopen does.
// Opening a FIFO waits for a writer.
func (c *gitConfigCwd) open(name string) (*os.File, error) {
	fd, path := c.at(name)
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
