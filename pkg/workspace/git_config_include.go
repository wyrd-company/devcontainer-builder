// relationships:
//   references: [git, git_global_config, git_config_syntax, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"errors"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"
)

// gitConfigIncludeDepth is MAX_INCLUDE_DEPTH in Git's config.c: the number of
// nested include.path files that Git reads below one config file.
const gitConfigIncludeDepth = 10

// gitRepositoryConfig names the repository config files that Git's full
// config read reads after setup, as its repository format check found them.
type gitRepositoryConfig struct {
	// common is the common folder's config.
	common string
	// worktree is config.worktree when extensions.worktreeConfig applies, or
	// "".
	worktree string
}

// read follows Git's full config read after setup for the repository config
// files, from cwd, Git's working folder, and reports false when the read stops
// the probe. Git reads them after the system and global files, as the format
// check did, and also follows their includes. Git keeps GIT_COMMON_DIR as
// given, so a relative value starts at cwd. A file that access(2) denies, or
// that Git cannot open, stops the probe; a missing file is skipped.
// Reference: https://github.com/git/git/blob/master/config.c
func (c gitRepositoryConfig) read(cwd string) bool {
	common := c.common
	if dir, set := os.LookupEnv("GIT_COMMON_DIR"); set {
		if dir != "" && !filepath.IsAbs(dir) {
			dir = cwd + string(os.PathSeparator) + dir
		}
		common = dir + string(os.PathSeparator) + "config"
	}
	for _, path := range []string{common, c.worktree} {
		if path != "" && gitConfigFile(path, false) != gitConfigFileAdmitted {
			return false
		}
	}
	return true
}

// gitConfigParse opens the config file at path, which Git reads at include
// depth depth, and parses it as Git reads it. It follows each include.path
// entry when Git reads it, before the rest of the file.
func gitConfigParse(path string, depth int) gitConfigFileResult {
	file, err := os.Open(path)
	if err != nil {
		return gitConfigFileUnopened
	}
	defer file.Close()
	if opened, err := file.Stat(); err != nil || opened.IsDir() {
		return gitConfigFileUnopened
	}
	if !parseGitConfigReader(file, func(entry gitConfigEntry) bool {
		return entry.name != "include.path" || gitConfigInclude(path, entry.value, depth)
	}) {
		return gitConfigFileRejected
	}
	return gitConfigFileAdmitted
}

// gitConfigInclude follows handle_path_include in Git's config.c for an
// include.path value in the config file from, read at include depth depth,
// and reports false when Git rejects it. A key without a value, a path that
// Git cannot expand, a path that access(2) rejects other than a missing one,
// a file that Git cannot open or parse, and nesting deeper than
// gitConfigIncludeDepth are fatal. A relative path starts at the folder of
// from, as named. includeIf conditions are not evaluated.
func gitConfigInclude(from string, value *string, depth int) bool {
	if value == nil {
		return false
	}
	path, follow, ok := gitIncludePath(*value)
	if !ok {
		return false
	}
	if !follow {
		return true
	}
	if !filepath.IsAbs(path) {
		if slash := strings.LastIndexByte(from, '/'); slash >= 0 {
			path = from[:slash+1] + path
		}
	}
	// syscall.Access checks the real credentials, as access(2) does; 4 is
	// R_OK.
	if err := syscall.Access(path, 4); err != nil {
		return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
	}
	if depth >= gitConfigIncludeDepth {
		return false
	}
	return gitConfigParse(path, depth+1) == gitConfigFileAdmitted
}

// gitIncludePath follows interpolate_path in Git's path.c for an include
// path. A leading ~ names HOME, which must be set, and ~name names that
// account's home folder, which must exist. follow is false for a
// %(prefix)/ path: Git reads it below the prefix that its build names, and
// that file is not read. ok is false for a path that Git cannot expand.
// Reference: https://github.com/git/git/blob/master/path.c
func gitIncludePath(value string) (path string, follow, ok bool) {
	if strings.HasPrefix(value, "%(prefix)/") {
		return "", false, true
	}
	if !strings.HasPrefix(value, "~") {
		return value, true, true
	}
	name, rest := value[1:], ""
	if slash := strings.IndexByte(value, '/'); slash >= 0 {
		name, rest = value[1:slash], value[slash:]
	}
	if name == "" {
		home, set := os.LookupEnv("HOME")
		if !set {
			return "", false, false
		}
		return home + rest, true, true
	}
	account, err := user.Lookup(name)
	if err != nil {
		return "", false, false
	}
	return account.HomeDir + rest, true, true
}
