// relationships:
//   references: [workspace, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"syscall"
)

// errGitConfig marks repository configuration that Git rejects. Git's probe
// then fails, and the upstream CLI finds no Git root.
var errGitConfig = errors.New("Git rejects the repository configuration")

// gitWorktreeSettings holds the core.bare and core.worktree values that
// Git's repository format check applies. bare is -1 when unset.
type gitWorktreeSettings struct {
	bare     int
	workTree *string
}

// gitFormat is the result of Git's repository format check.
type gitFormat struct {
	// bare reports that core.bare applies and selects no working tree.
	bare bool
	// workTree is the core.worktree value that applies, if any.
	workTree *string
	// config names the repository config files that Git's full config read
	// reads after setup.
	config gitRepositoryConfig
}

// gitRepositoryFormat follows check_repository_format_gently in Git's setup.c.
// It reads the common folder's config, plus config.worktree when
// extensions.worktreeConfig is set. It reports whether core.bare applies and
// selects no working tree, and the core.worktree value that applies, if any.
// Without core.repositoryformatversion, or with a negative version, Git
// ignores both. A linked worktree or GIT_COMMON_DIR also ignores the common
// values. Extensions follow gitExtensions. Includes are not followed, as in
// the format check; GitRoot reads the same files again, with their includes,
// for Git's full config read after setup. Git runs the check from folder: the
// folder where it found a .git entry or a repository, or the probe cwd for
// GIT_DIR. A relative GIT_COMMON_DIR starts there.
// Reference: https://github.com/git/git/blob/master/setup.c
func gitRepositoryFormat(dir, folder string) (gitFormat, error) {
	common, shared, err := gitCommon(dir, folder)
	if err != nil {
		return gitFormat{}, err
	}
	format := gitFormat{config: gitRepositoryConfig{common: common + string(os.PathSeparator) + "config"}}
	version := -1
	var extensions gitExtensions
	settings := gitWorktreeSettings{bare: -1}
	err = readGitConfig(format.config.common, func(entry gitConfigEntry) bool {
		switch {
		case entry.name == "core.repositoryformatversion":
			value, ok := gitParseInt(entry.value)
			if !ok {
				return false
			}
			version = value
		case strings.HasPrefix(entry.name, "extensions."):
			return extensions.add(strings.TrimPrefix(entry.name, "extensions."), entry.value)
		default:
			return gitWorktreeConfig(entry, &settings)
		}
		return true
	})
	if err != nil {
		return gitFormat{}, err
	}
	if version < 0 {
		return format, nil
	}
	if version > 1 || !extensions.verify(version) {
		return gitFormat{}, errGitConfig
	}
	if extensions.worktreeConfig {
		worktree := dir + string(os.PathSeparator) + "config.worktree"
		err := readGitConfig(worktree, func(entry gitConfigEntry) bool {
			return gitWorktreeConfig(entry, &settings)
		})
		if err != nil {
			return gitFormat{}, err
		}
		format.config.worktree = worktree
		shared = false
	}
	if shared {
		return format, nil
	}
	format.bare = settings.bare == 1
	format.workTree = settings.workTree
	return format, nil
}

// gitWorktreeConfig follows read_worktree_config in Git's setup.c. It records
// core.bare and core.worktree, and reports false for a value that Git rejects.
// The last core.worktree value wins.
func gitWorktreeConfig(entry gitConfigEntry, settings *gitWorktreeSettings) bool {
	switch entry.name {
	case "core.bare":
		value, ok := gitParseBool(entry.value)
		if !ok {
			return false
		}
		settings.bare = 0
		if value {
			settings.bare = 1
		}
	case "core.worktree":
		if entry.value == nil {
			return false
		}
		settings.workTree = entry.value
	}
	return true
}

// readGitConfig reads one repository-format config stream, giving each entry
// to entry as it is parsed. A missing file has no entries. A directory or
// another access failure is rejected. Like Git, it opens a FIFO without a
// time limit, stops at the first syntax or typed-value error, and treats a
// read error as the end of the file. Includes are not followed in this read.
func readGitConfig(path string, entry func(gitConfigEntry) bool) error {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	if info, err := file.Stat(); err != nil || info.IsDir() {
		return errGitConfig
	}
	if !parseGitConfigReader(file, entry) {
		return errGitConfig
	}
	return nil
}
