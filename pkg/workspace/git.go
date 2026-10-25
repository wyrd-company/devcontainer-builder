// relationships:
//   references: [workspace, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// gitdirLine matches a gitfile's pointer, as upstream reads worktree .git files.
var gitdirLine = regexp.MustCompile(`(?m)^gitdir:\s*(.+)$`)

// GitRoot returns the top folder of the Git working tree that contains folder,
// or "" when folder is not inside one. It follows Git's discovery without
// running git: walking up lexically (so symlinked names are kept, as
// upstream's `git rev-parse --show-cdup` result is), a .git folder that is a
// repository marks the root; a .git file marks the root when its gitdir is a
// repository, and otherwise ends discovery, as Git refuses an invalid gitfile.
// GIT_CEILING_DIRECTORIES limits parent discovery as in Git. Other environment
// overrides and Git's ownership check are not applied.
func GitRoot(folder string) (string, error) {
	limit := gitCeilingLimit(folder, os.Getenv("GIT_CEILING_DIRECTORIES"))
	for current, depth := filepath.Clean(folder), 0; ; current, depth = filepath.Dir(current), depth+1 {
		if limit >= 0 && depth >= limit {
			return "", nil
		}
		dotGit := filepath.Join(current, ".git")
		info, err := os.Stat(dotGit)
		switch {
		case err == nil && info.IsDir():
			if isGitDir(dotGit) {
				return current, nil
			}
		case err == nil:
			gitdir, err := readGitfile(dotGit)
			if err != nil {
				return "", err
			}
			if gitdir == "" || !isGitDir(gitdir) {
				return "", nil
			}
			return current, nil
		case !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fs.ErrPermission):
			return "", err
		}
		if filepath.Dir(current) == current {
			return "", nil
		}
	}
}

// readGitfile returns the folder a .git file points to, resolved against the
// file's folder, or "" when the file has no gitdir line.
func readGitfile(path string) (string, error) {
	gitdir, err := gitfilePointer(path)
	if err != nil || gitdir == "" {
		return "", err
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(filepath.Dir(path), gitdir)
	}
	return gitdir, nil
}

// gitfilePointer returns the gitdir value of a .git file as written.
func gitfilePointer(path string) (string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	match := gitdirLine.FindSubmatch(content)
	if match == nil {
		return "", nil
	}
	return strings.TrimRight(string(match[1]), "\r"), nil
}

// isGitDir is Git's repository test: HEAD, plus objects and refs or, for a
// linked worktree's administrative folder, a commondir file.
func isGitDir(dir string) bool {
	if !isFile(filepath.Join(dir, "HEAD")) {
		return false
	}
	if isFile(filepath.Join(dir, "commondir")) {
		return true
	}
	return isDir(filepath.Join(dir, "objects")) && isDir(filepath.Join(dir, "refs"))
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// gitCeilingLimit counts parent steps to the closest strict ancestor ceiling.
// Git canonicalizes entries until an empty list entry disables symlink resolution.
// Count against the physical starting path, retaining lexical paths in GitRoot.
func gitCeilingLimit(folder, ceilings string) int {
	if ceilings == "" {
		return -1
	}
	physical, err := filepath.EvalSymlinks(folder)
	if err != nil {
		return -1
	}
	physical, err = filepath.Abs(physical)
	if err != nil {
		return -1
	}
	limit := -1
	resolve := true
	for _, entry := range strings.Split(ceilings, string(os.PathListSeparator)) {
		if entry == "" {
			resolve = false
			continue
		}
		if !filepath.IsAbs(entry) {
			continue
		}
		if resolve {
			entry, err = filepath.EvalSymlinks(entry)
			if err != nil {
				continue
			}
		}
		// Git removes one trailing separator from literal entries, but does
		// not collapse dot components or repeated separators.
		if !resolve && len(entry) > 1 {
			entry = strings.TrimSuffix(entry, string(os.PathSeparator))
		}
		for parent, depth := filepath.Dir(physical), 1; parent != physical; parent, depth = filepath.Dir(parent), depth+1 {
			if parent == entry && (limit < 0 || depth < limit) {
				limit = depth
				break
			}
			if filepath.Dir(parent) == parent {
				break
			}
		}
	}
	return limit
}
