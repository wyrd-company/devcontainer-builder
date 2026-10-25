// relationships:
//   references: [git, git_config, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"
)

// gitSelectedRoot applies Git's repository format check to the repository dir
// whose default working-tree root is root, then selects the working tree as
// setup_explicit_git_dir in Git's setup.c does. GIT_WORK_TREE takes precedence
// and GitRoot applies it. core.bare leaves no working tree, and the upstream
// CLI resolves the empty cdup to the caller folder. Otherwise core.worktree
// selects the working tree. Rejected config, or a core.worktree that Git cannot
// resolve, is fatal.
func gitSelectedRoot(root, dir, folder string) string {
	bare, workTree, err := gitRepositoryFormat(dir, folder)
	if err != nil {
		return ""
	}
	if _, set := os.LookupEnv("GIT_WORK_TREE"); set {
		return root
	}
	switch {
	case bare:
		return filepath.Clean(folder)
	case workTree != nil:
		return gitConfigWorkTreeRoot(dir, *workTree, folder)
	}
	return root
}

// gitConfigWorkTreeRoot resolves a core.worktree value for repository dir.
// Git resolves an absolute value as it does GIT_WORK_TREE. For a relative
// value it changes to dir and then to the value, so the value must name an
// accessible folder, and reads the physical cwd. An empty value fails.
func gitConfigWorkTreeRoot(dir, value, folder string) string {
	physical, err := gitPhysicalPath(folder)
	if err != nil {
		return ""
	}
	var workTree string
	if filepath.IsAbs(value) {
		workTree, err = gitCeilingPath(value)
	} else {
		workTree, err = gitChdirPath(dir, value)
	}
	if err != nil {
		return ""
	}
	return gitWorkTreeRoot(folder, physical, workTree)
}

// gitChdirPath returns the physical folder that chdir(dir) then chdir(value)
// selects. The kernel resolves symbolic links before .. components.
func gitChdirPath(dir, value string) (string, error) {
	if value == "" {
		return "", fs.ErrNotExist
	}
	path := dir + string(os.PathSeparator) + value
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", syscall.ENOTDIR
	}
	if err := syscall.Access(path, 1); err != nil {
		return "", err
	}
	return gitPhysicalPath(path)
}

// gitPhysicalPath returns the absolute physical path of folder, which Git
// reads as its cwd.
func gitPhysicalPath(folder string) (string, error) {
	physical, err := filepath.EvalSymlinks(folder)
	if err != nil {
		return "", err
	}
	return filepath.Abs(physical)
}

// gitWorkTreeRoot resolves upstream's `git rev-parse --show-cdup` result for
// the physical working tree workTree. Inside it, the cdup returns parent steps
// that preserve the caller's lexical path; outside it, the cdup is the
// absolute physical working tree.
func gitWorkTreeRoot(folder, physical, workTree string) string {
	for current, lexical := physical, filepath.Clean(folder); ; current, lexical = filepath.Dir(current), filepath.Dir(lexical) {
		if current == workTree {
			return lexical
		}
		if filepath.Dir(current) == current {
			// Upstream trims JavaScript whitespace from the probe output before
			// resolving it. Absolute cdup starts with a separator, so only
			// its suffix can change. Go TrimSpace differs for BOM and NEL.
			return filepath.Clean(strings.TrimRightFunc(workTree, func(r rune) bool {
				return unicode.Is(unicode.Zs, r) || strings.ContainsRune("\t\n\v\f\r\u2028\u2029\ufeff", r)
			}))
		}
	}
}
