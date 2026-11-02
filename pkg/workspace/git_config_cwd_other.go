//go:build !linux && !darwin

// relationships:
//   references: [git_global_config, git_config_include]

package workspace

import (
	"os"
	"path/filepath"
	"syscall"
)

// gitConfigCwd is Git's working folder in one of its config reads. These
// hosts are not release targets: a relative name is joined to the folder's
// path, which shares the host's path length limit, while Git names it relative
// to its working folder.
type gitConfigCwd struct {
	folder string
}

// newGitConfigCwd returns the working folder folder for one config read.
func newGitConfigCwd(folder string) *gitConfigCwd { return &gitConfigCwd{folder: folder} }

// close does nothing.
func (c *gitConfigCwd) close() {}

// path returns the path that names name.
func (c *gitConfigCwd) path(name string) string {
	if filepath.IsAbs(name) {
		return name
	}
	return c.folder + string(os.PathSeparator) + name
}

// access follows access(name, R_OK), which checks the real credentials; 4 is
// R_OK.
func (c *gitConfigCwd) access(name string) error { return syscall.Access(c.path(name), 4) }

// open opens name for reading with the effective credentials.
func (c *gitConfigCwd) open(name string) (*os.File, error) { return os.Open(c.path(name)) }
