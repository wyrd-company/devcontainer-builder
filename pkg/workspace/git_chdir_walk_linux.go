// relationships:
//   references: [git_chdir_linux]

package workspace

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// gitWalkMaxLinks is the kernel's limit of symbolic links in one lookup.
const gitWalkMaxLinks = 40

// gitWalkCwd names the folder target refers to, which chdir(dir) and then
// chdir(value) select, without a thread of its own or /proc. Each lookup is
// repeated one component at a time from a descriptor, so it can pass folders
// whose paths exceed PATH_MAX, and the physical name is kept as a string.
// The name is accepted only if statx finds the mount and inode that target
// refers to there; statx also fails on a name that does not fit in PATH_MAX,
// as getcwd does.
func gitWalkCwd(dir, value string, target int) (string, error) {
	w := &gitWalker{fd: -1}
	defer w.close()
	if !filepath.IsAbs(dir) {
		if err := w.cwd(); err != nil {
			return "", err
		}
	}
	if err := w.lookup(dir); err != nil {
		return "", err
	}
	if err := w.lookup(value); err != nil {
		return "", err
	}
	same, err := gitSameFolder(target, w.name)
	if err != nil {
		return "", err
	}
	if !same {
		return "", unix.ESTALE
	}
	return w.name, nil
}

// gitWalker holds a descriptor of the folder a lookup has reached and that
// folder's physical name.
type gitWalker struct {
	fd   int
	name string
}

const gitWalkFlags = unix.O_PATH | unix.O_CLOEXEC

// cwd moves the walker to the process's working folder, where a relative
// path starts.
func (w *gitWalker) cwd() error {
	fd, err := unix.Open(".", gitWalkFlags|unix.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	name, err := unix.Getwd()
	if err != nil {
		unix.Close(fd)
		return err
	}
	w.move(fd, name)
	return nil
}

// root moves the walker to the process's root, where an absolute path or
// symbolic link starts.
func (w *gitWalker) root() error {
	fd, err := unix.Open("/", gitWalkFlags|unix.O_DIRECTORY, 0)
	if err != nil {
		return err
	}
	w.move(fd, "/")
	return nil
}

// move replaces the walker's folder with fd, whose name is name.
func (w *gitWalker) move(fd int, name string) {
	w.close()
	w.fd, w.name = fd, name
}

func (w *gitWalker) close() {
	if w.fd >= 0 {
		unix.Close(w.fd)
		w.fd = -1
	}
}

// lookup repeats the kernel's lookup of path from the walker's folder, with
// its own symbolic link limit. Symbolic links are followed before the
// components after them, and .. is the kernel's own, so it leaves a mount
// at its mount point.
func (w *gitWalker) lookup(path string) error {
	if strings.HasPrefix(path, "/") {
		if err := w.root(); err != nil {
			return err
		}
	}
	pending := strings.Split(path, "/")
	links := 0
	for len(pending) > 0 {
		c := pending[0]
		pending = pending[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			parent, err := unix.Openat(w.fd, "..", gitWalkFlags|unix.O_DIRECTORY, 0)
			if err != nil {
				return err
			}
			w.move(parent, filepath.Dir(w.name))
			continue
		}
		next, err := unix.Openat(w.fd, c, gitWalkFlags|unix.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		var st unix.Stat_t
		if err := unix.Fstat(next, &st); err != nil {
			unix.Close(next)
			return err
		}
		switch st.Mode & unix.S_IFMT {
		case unix.S_IFDIR:
			w.move(next, filepath.Join(w.name, c))
		case unix.S_IFLNK:
			links++
			if links > gitWalkMaxLinks {
				unix.Close(next)
				return unix.ELOOP
			}
			link, err := gitReadlink(next)
			unix.Close(next)
			if err != nil {
				return err
			}
			if strings.HasPrefix(link, "/") {
				if err := w.root(); err != nil {
					return err
				}
			}
			pending = append(strings.Split(link, "/"), pending...)
		default:
			unix.Close(next)
			return unix.ENOTDIR
		}
	}
	return nil
}

// gitReadlink returns the text of the symbolic link fd refers to.
func gitReadlink(fd int) (string, error) {
	for size := 128; ; size *= 2 {
		buf := make([]byte, size)
		n, err := unix.Readlinkat(fd, "", buf)
		if err != nil {
			return "", err
		}
		// A full buffer may hold only part of the text.
		if n < size {
			return string(buf[:n]), nil
		}
	}
}

// gitSameFolder reports whether name refers to the folder fd refers to: the
// same device and inode and, where the kernel reports mount IDs, the same
// mount.
func gitSameFolder(fd int, name string) (bool, error) {
	const mask = unix.STATX_INO | unix.STATX_MNT_ID
	var a, b unix.Statx_t
	if err := unix.Statx(fd, "", unix.AT_EMPTY_PATH, mask, &a); err != nil {
		return false, err
	}
	if err := unix.Statx(unix.AT_FDCWD, name, 0, mask, &b); err != nil {
		return false, err
	}
	same := a.Ino == b.Ino && a.Dev_major == b.Dev_major && a.Dev_minor == b.Dev_minor
	if a.Mask&b.Mask&unix.STATX_MNT_ID != 0 {
		same = same && a.Mnt_id == b.Mnt_id
	}
	return same, nil
}
