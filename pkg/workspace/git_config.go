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

// gitRepositoryFormat follows check_repository_format_gently in Git's setup.c.
// It reads the common folder's config, plus config.worktree when
// extensions.worktreeConfig is set. It reports whether core.bare applies and
// selects no working tree, and the core.worktree value that applies, if any.
// Without core.repositoryformatversion, or with a negative version, Git
// ignores both. A linked worktree or GIT_COMMON_DIR also ignores the common
// values. Extensions outside Git's version 0 set are not checked. Includes are
// not followed, as in the format check; Git's later full config read, which
// follows them, is not applied.
// Reference: https://github.com/git/git/blob/master/setup.c
func gitRepositoryFormat(dir, folder string) (bool, *string, error) {
	common, shared, err := gitCommon(dir, folder)
	if err != nil {
		return false, nil, err
	}
	entries, err := readGitConfig(common + string(os.PathSeparator) + "config")
	if err != nil {
		return false, nil, err
	}
	version, worktreeConfig := -1, false
	settings := gitWorktreeSettings{bare: -1}
	for _, entry := range entries {
		switch {
		case entry.name == "core.repositoryformatversion":
			value, ok := gitParseInt(entry.value)
			if !ok {
				return false, nil, errGitConfig
			}
			version = value
		case strings.HasPrefix(entry.name, "extensions."):
			// Git handles these version 0 extensions in every repository.
			switch strings.TrimPrefix(entry.name, "extensions.") {
			case "preciousobjects":
				if _, ok := gitParseBool(entry.value); !ok {
					return false, nil, errGitConfig
				}
			case "partialclone":
				if entry.value == nil {
					return false, nil, errGitConfig
				}
			case "worktreeconfig":
				value, ok := gitParseBool(entry.value)
				if !ok {
					return false, nil, errGitConfig
				}
				worktreeConfig = value
			}
		default:
			if !gitWorktreeConfig(entry, &settings) {
				return false, nil, errGitConfig
			}
		}
	}
	if version < 0 {
		return false, nil, nil
	}
	if version > 1 {
		return false, nil, errGitConfig
	}
	if worktreeConfig {
		entries, err := readGitConfig(dir + string(os.PathSeparator) + "config.worktree")
		if err != nil {
			return false, nil, err
		}
		for _, entry := range entries {
			if !gitWorktreeConfig(entry, &settings) {
				return false, nil, errGitConfig
			}
		}
		shared = false
	}
	if shared {
		return false, nil, nil
	}
	return settings.bare == 1, settings.workTree, nil
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

// readGitConfig reads a Git config file. A missing file has no entries.
// Git rejects other access failures and a directory. Git would block reading
// a FIFO; special files are rejected here without reading.
func readGitConfig(path string) ([]gitConfigEntry, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errGitConfig
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	entries, ok := parseGitConfig(content)
	if !ok {
		return nil, errGitConfig
	}
	return entries, nil
}
