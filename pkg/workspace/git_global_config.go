// relationships:
//   references: [git, git_config_syntax, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// gitGlobalConfigRead follows one of Git's reads of its global config files,
// from folder, and reports false when the read stops the probe.
// GIT_CONFIG_GLOBAL, when set, names the only file. Otherwise Git reads
// $XDG_CONFIG_HOME/git/config, or $HOME/.config/git/config when
// XDG_CONFIG_HOME is empty or unset, and then $HOME/.gitconfig; relative paths
// start at folder. Git first reads them when it starts, in
// read_very_early_config, before repository discovery, from the probe cwd. It
// reads them again after setup, second, from its working folder, where a file
// that it cannot open is also fatal. Includes, typed values and the system and
// command-line config are not applied.
// Reference: https://github.com/git/git/blob/master/config.c
func gitGlobalConfigRead(folder string, second bool) bool {
	for _, path := range gitGlobalConfigPaths() {
		if path == "" {
			// access(2) reports an empty path missing.
			continue
		}
		if !filepath.IsAbs(path) {
			path = folder + string(os.PathSeparator) + path
		}
		switch gitGlobalConfigFile(path) {
		case gitConfigFileRejected:
			return false
		case gitConfigFileUnopened:
			if second {
				return false
			}
		}
	}
	return true
}

// gitConfigFileResult is the result of one read of a config file.
type gitConfigFileResult int

const (
	// gitConfigFileAdmitted is a file that Git reads or skips.
	gitConfigFileAdmitted gitConfigFileResult = iota
	// gitConfigFileUnopened is a file that Git cannot open after access(2)
	// admits it, such as a folder. Git's first read warns and continues.
	gitConfigFileUnopened
	// gitConfigFileRejected is a file that stops the probe.
	gitConfigFileRejected
)

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

// gitGlobalConfigFile follows access_or_die with ACCESS_EACCES_OK, then the
// file read. Git skips a file that access(2) reports missing, or denies with
// the real credentials, and stops on another access failure. It then opens the
// file with the effective credentials; its fopen also fails on a folder. It
// parses the file as it reads it, so a syntax error stops the probe as soon
// as Git reads it, and a read error ends the file. Git would block reading a
// FIFO; it is rejected here without opening it.
func gitGlobalConfigFile(path string) gitConfigFileResult {
	// syscall.Access checks the real credentials, as access(2) does; 4 is
	// R_OK.
	if err := syscall.Access(path, 4); err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.EACCES) {
			return gitConfigFileAdmitted
		}
		return gitConfigFileRejected
	}
	info, err := os.Stat(path)
	if err != nil {
		return gitConfigFileUnopened
	}
	if info.Mode()&os.ModeNamedPipe != 0 {
		return gitConfigFileRejected
	}
	file, err := os.Open(path)
	if err != nil {
		return gitConfigFileUnopened
	}
	defer file.Close()
	if opened, err := file.Stat(); err != nil || opened.IsDir() {
		return gitConfigFileUnopened
	}
	if !parseGitConfigReader(file, nil) {
		return gitConfigFileRejected
	}
	return gitConfigFileAdmitted
}
