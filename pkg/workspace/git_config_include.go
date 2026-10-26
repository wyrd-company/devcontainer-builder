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
		common = dir + string(os.PathSeparator) + "config"
	}
	folder := newGitConfigCwd(cwd)
	defer folder.close()
	for _, path := range []string{common, c.worktree} {
		if path != "" && gitConfigFile(folder, path, false) != gitConfigFileAdmitted {
			return false
		}
	}
	return true
}

// gitConfigParse opens the config file at path, from folder, which Git reads
// at include depth depth, and parses it as Git reads it. It follows each
// include.path entry when Git reads it, before the rest of the file.
func gitConfigParse(folder *gitConfigCwd, path string, depth int) gitConfigFileResult {
	file, err := folder.open(path)
	if err != nil {
		return gitConfigFileUnopened
	}
	defer file.Close()
	if opened, err := file.Stat(); err != nil || opened.IsDir() {
		return gitConfigFileUnopened
	}
	if !parseGitConfigReader(file, func(entry gitConfigEntry) bool {
		return entry.name != "include.path" || gitConfigInclude(folder, path, entry.value, depth)
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
// from, as named, so it stays relative to folder, Git's working folder, when
// from is. includeIf conditions are not evaluated.
func gitConfigInclude(folder *gitConfigCwd, from string, value *string, depth int) bool {
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
	if err := folder.access(path); err != nil {
		return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
	}
	if depth >= gitConfigIncludeDepth {
		return false
	}
	return gitConfigParse(folder, path, depth+1) == gitConfigFileAdmitted
}

// gitIncludePath follows interpolate_path in Git's path.c for an include
// path. A leading ~ names HOME, which must be set, and ~name names that
// account's home folder, which must exist. A %(prefix)/ path names the rest
// of the path, as system_path in Git's exec-cmd.c does: an absolute rest as
// it is, and a relative one below the prefix that Git's build names. follow
// is false for that relative rest, which is not read. ok is false for a path
// that Git cannot expand.
// Reference: https://github.com/git/git/blob/master/path.c
func gitIncludePath(value string) (path string, follow, ok bool) {
	if rest, prefixed := strings.CutPrefix(value, "%(prefix)/"); prefixed {
		return rest, strings.HasPrefix(rest, "/"), true
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
