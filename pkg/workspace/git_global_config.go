// relationships:
//   references: [git, git_config_syntax, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// gitDeviceConfigLimit bounds what is read from a device named as a global
// config file. Git reads a device until it ends; native discovery rejects one
// that streams more than this, such as /dev/zero, which Git's parser also
// rejects.
const gitDeviceConfigLimit = 1 << 20

// gitGlobalConfigAdmitted follows Git's reads of its global config files. A
// file that Git rejects stops the probe, and upstream finds no Git root.
// GIT_CONFIG_GLOBAL, when set, names the only file. Otherwise Git reads
// $XDG_CONFIG_HOME/git/config, or $HOME/.config/git/config when
// XDG_CONFIG_HOME is empty or unset, and then $HOME/.gitconfig. Git first
// reads them when it starts, in read_very_early_config, before repository
// discovery; relative paths start at the probe cwd, folder. It reads them
// again after it changes to the working tree, where a file that it cannot open
// or read is also fatal. An absolute path names the same file both times; that
// second read of a relative path is not applied. Includes, typed values and
// the system and command-line config are not applied either.
// Reference: https://github.com/git/git/blob/master/config.c
func gitGlobalConfigAdmitted(folder string) bool {
	for _, path := range gitGlobalConfigPaths() {
		reread := filepath.IsAbs(path)
		if path != "" && !reread {
			path = folder + string(os.PathSeparator) + path
		}
		if !gitGlobalConfigFileAdmitted(path, reread) {
			return false
		}
	}
	return true
}

// gitGlobalConfigPaths follows git_global_config_paths and xdg_config_home in
// Git, in read order.
func gitGlobalConfigPaths() []string {
	if global, set := os.LookupEnv("GIT_CONFIG_GLOBAL"); set {
		return []string{global}
	}
	var paths []string
	home, homeSet := os.LookupEnv("HOME")
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		paths = append(paths, xdg+"/git/config")
	} else if homeSet {
		paths = append(paths, home+"/.config/git/config")
	}
	if homeSet {
		paths = append(paths, home+"/.gitconfig")
	}
	return paths
}

// gitGlobalConfigFileAdmitted follows access_or_die with ACCESS_EACCES_OK,
// then the file read. Git skips a file that access(2) reports missing, or
// denies with the real credentials, and stops on another access failure. It
// then opens the file with the effective credentials. A syntax error stops
// the probe. An open or read failure, such as a directory, stops it only when
// Git reads the file again after discovery, reread. Git would block reading a
// FIFO; it is rejected here without opening it.
func gitGlobalConfigFileAdmitted(path string, reread bool) bool {
	// syscall.Access checks the real credentials, as access(2) does; 4 is
	// R_OK.
	if err := syscall.Access(path, 4); err != nil {
		return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.EACCES)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode()&os.ModeNamedPipe != 0 {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return !reread
	}
	defer file.Close()
	var reader io.Reader = file
	if !info.Mode().IsRegular() {
		reader = io.LimitReader(file, gitDeviceConfigLimit+1)
	}
	content, err := io.ReadAll(reader)
	if err != nil {
		return !reread
	}
	if len(content) > gitDeviceConfigLimit && !info.Mode().IsRegular() {
		return false
	}
	_, ok := parseGitConfig(content)
	return ok
}
