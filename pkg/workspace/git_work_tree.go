// relationships:
//   references: [git, git_config, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
)

// gitSelectedRoot applies Git's repository format check to the repository dir
// whose default working-tree root is root, then selects the working tree as
// setup_explicit_git_dir in Git's setup.c does. GIT_WORK_TREE takes precedence
// and GitRoot applies it. core.bare leaves no working tree, and the upstream
// CLI resolves the empty cdup to the caller folder. Otherwise core.worktree
// selects the working tree. Rejected config, or a core.worktree that Git cannot
// resolve, is fatal. gitdir gives the repository folder that Git changes to
// before a relative core.worktree. cwd is Git's working folder with the
// default working tree; it returns the selected root and working folder.
func gitSelectedRoot(root, cwd, dir, folder string, gitdir func() (string, error)) (string, string) {
	bare, workTree, err := gitRepositoryFormat(dir, folder)
	if err != nil {
		return "", ""
	}
	if _, set := os.LookupEnv("GIT_WORK_TREE"); set {
		return root, cwd
	}
	switch {
	case bare:
		// Git stays in the caller folder.
		return filepath.Clean(folder), folder
	case workTree != nil:
		return gitConfigWorkTreeRoot(gitdir, *workTree, folder)
	}
	return root, cwd
}

// gitConfigWorkTreeRoot resolves a core.worktree value. Git resolves an
// absolute value as it does GIT_WORK_TREE. For a relative value it changes to
// the repository folder that gitdir gives and then to the value, so the value
// must name an accessible folder, and reads the physical cwd. An empty value
// fails.
func gitConfigWorkTreeRoot(gitdir func() (string, error), value, folder string) (string, string) {
	physical, err := gitPhysicalPath(folder)
	if err != nil {
		return "", ""
	}
	var workTree string
	if filepath.IsAbs(value) {
		workTree, err = gitCeilingPath(value)
	} else {
		workTree, err = gitChdirPath(gitdir, value)
	}
	if err != nil {
		return "", ""
	}
	return gitWorkTreeRoot(folder, physical, workTree)
}

// gitChdirPath returns the physical folder that chdir(gitdir) then
// chdir(value) selects.
func gitChdirPath(gitdir func() (string, error), value string) (string, error) {
	if value == "" {
		return "", fs.ErrNotExist
	}
	dir, err := gitdir()
	if err != nil {
		return "", err
	}
	return gitChdir(dir, value)
}

// gitChdirDir gives dir, which Git changes to as named.
func gitChdirDir(dir string) func() (string, error) {
	return func() (string, error) { return dir, nil }
}

// gitRealpathDir returns the folder that Git's realpath gives for path, as
// Git resolves a gitfile target or a .git folder found above the caller
// folder before it changes to it. A non-empty parent is the folder whose
// physical path, Git's cwd, a relative path starts from. That realpath has
// its own symbolic link limit and fails on a missing intermediate component.
func gitRealpathDir(parent, path string) func() (string, error) {
	return func() (string, error) {
		if parent == "" {
			return gitCeilingPath(path)
		}
		cwd, err := gitPhysicalPath(parent)
		if err != nil {
			return "", err
		}
		return gitCeilingPath(cwd + string(os.PathSeparator) + path)
	}
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
// absolute physical working tree. It also returns Git's working folder: Git
// changes to the working tree when folder is inside it, as
// setup_explicit_git_dir does, and otherwise stays in folder.
func gitWorkTreeRoot(folder, physical, workTree string) (string, string) {
	for current, lexical := physical, filepath.Clean(folder); ; current, lexical = filepath.Dir(current), filepath.Dir(lexical) {
		if current == workTree {
			return lexical, workTree
		}
		if filepath.Dir(current) == current {
			// Upstream trims JavaScript whitespace from the probe output before
			// resolving it. Absolute cdup starts with a separator, so only
			// its suffix can change. Go TrimSpace differs for BOM and NEL.
			return filepath.Clean(strings.TrimRightFunc(workTree, func(r rune) bool {
				return unicode.Is(unicode.Zs, r) || strings.ContainsRune("\t\n\v\f\r\u2028\u2029\ufeff", r)
			})), folder
		}
	}
}
