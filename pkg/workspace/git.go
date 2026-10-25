// relationships:
//   references: [workspace, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode"
)

// GitRoot returns the Git working tree selected from folder,
// or "" when no repository is found or its working tree is invalid. It follows Git's discovery without
// running git: walking up lexically (so symlinked names are kept, as
// upstream's `git rev-parse --show-cdup` result is), a .git folder that is a
// repository marks the root; a .git file marks the root when its gitdir is a
// repository, and otherwise ends discovery, as Git refuses an invalid gitfile.
// A folder that is itself a repository, such as a bare repository or a .git
// folder, selects the caller folder as upstream's empty cdup does.
// HEAD must pass Git's symbolic or detached syntax admission check.
// A commondir pointer selects the common objects and refs signatures.
// GIT_COMMON_DIR replaces that pointer and GIT_OBJECT_DIRECTORY overrides the
// objects signature, both relative to folder. Git's repository format check
// then reads the repository config: core.bare selects the caller folder for a
// discovered repository, and config that Git rejects gives no root.
// Gitfiles use a literal prefix and path payload, with Git's 1 MiB size limit.
// GIT_CEILING_DIRECTORIES limits parent discovery as in Git. GIT_WORK_TREE
// selects the working tree after repository discovery. Other environment
// overrides and Git's ownership check are not applied. A valid GIT_DIR
// selects the caller folder before GIT_WORK_TREE; core.worktree is not
// interpreted. Invalid GIT_DIR stops parent discovery.
func GitRoot(folder string) (string, error) {
	var root string
	var err error
	if dir, set := os.LookupEnv("GIT_DIR"); set {
		if dir == "" {
			return "", nil
		}
		if !filepath.IsAbs(dir) {
			// Resolve relative to the probe cwd, without cleaning symlink/..
			// components before the filesystem has followed them.
			dir = folder + string(os.PathSeparator) + dir
		}
		info, err := os.Stat(dir)
		if err != nil {
			return "", nil
		}
		if !info.IsDir() {
			// Git rejects special files; reading a FIFO could block discovery.
			if !info.Mode().IsRegular() {
				return "", nil
			}
			// GIT_DIR can name a gitfile, whose target is relative to it.
			target, err := gitfilePointer(dir)
			if err != nil || target == "" {
				return "", nil
			}
			if !filepath.IsAbs(target) {
				// Git anchors relative targets at the given gitfile path,
				// even when that file is a symlink. Split keeps dot components.
				parent, _ := filepath.Split(dir)
				target = parent + target
			}
			dir = target
		}
		valid, _ := isGitDir(dir, folder)
		if dir == "" || !valid {
			return "", nil
		}
		// Without a working tree, core.bare also gives the caller folder.
		if _, err := gitRepositoryFormat(dir, folder); err != nil {
			return "", nil
		}
		// An explicit repository disables parent discovery. Git defaults its
		// working tree to the probe cwd, including outside a discovered tree.
		root = filepath.Clean(folder)
	} else {
		root, err = discoverGitRoot(folder)
	}
	if err != nil || root == "" {
		return root, err
	}
	workTree, set := os.LookupEnv("GIT_WORK_TREE")
	if !set {
		return root, nil
	}
	if workTree == "" {
		return "", nil
	}
	physical, err := filepath.EvalSymlinks(folder)
	if err != nil {
		return "", nil
	}
	physical, err = filepath.Abs(physical)
	if err != nil {
		return "", nil
	}
	if !filepath.IsAbs(workTree) {
		// Preserve components until Git's physical path resolution, including
		// a symlink followed by .. and an invalid missing intermediate.
		workTree = physical + string(os.PathSeparator) + workTree
	}
	workTree, err = gitCeilingPath(workTree)
	if err != nil {
		// Upstream treats a failed rev-parse as no Git root.
		return "", nil
	}
	// --show-cdup returns parent steps inside the work tree, preserving the
	// caller's lexical path, or the absolute physical work tree outside it.
	for current, lexical := physical, filepath.Clean(folder); ; current, lexical = filepath.Dir(current), filepath.Dir(lexical) {
		if current == workTree {
			return lexical, nil
		}
		if filepath.Dir(current) == current {
			// Upstream trims JavaScript whitespace from the probe output before
			// resolving it. Absolute cdup starts with a separator, so only
			// its suffix can change. Go TrimSpace differs for BOM and NEL.
			return filepath.Clean(strings.TrimRightFunc(workTree, func(r rune) bool {
				return unicode.Is(unicode.Zs, r) || strings.ContainsRune("\t\n\v\f\r\u2028\u2029\ufeff", r)
			})), nil
		}
	}
}

func discoverGitRoot(folder string) (string, error) {
	limit := gitCeilingLimit(folder, os.Getenv("GIT_CEILING_DIRECTORIES"))
	for current, depth := filepath.Clean(folder), 0; ; current, depth = filepath.Dir(current), depth+1 {
		if limit >= 0 && depth >= limit {
			return "", nil
		}
		dotGit := filepath.Join(current, ".git")
		info, err := os.Stat(dotGit)
		switch {
		case err == nil && info.IsDir():
			valid, err := isGitDir(dotGit, folder)
			if err != nil {
				// A failed common-directory read or resolution makes Git's
				// probe fail, rather than continuing to a parent repository.
				return "", nil
			}
			if valid {
				return gitDiscoveredRoot(current, dotGit, folder), nil
			}
		case err == nil:
			gitdir, err := readGitfile(dotGit)
			if err != nil {
				return "", err
			}
			valid, _ := isGitDir(gitdir, folder)
			if gitdir == "" || !valid {
				return "", nil
			}
			return gitDiscoveredRoot(current, gitdir, folder), nil
		case !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, fs.ErrPermission):
			return "", err
		}
		// Git then tests the folder itself, which finds a bare repository or an
		// administrative folder. Its probe then has no working tree, and the
		// upstream CLI resolves the empty cdup to the caller folder.
		valid, err := isGitDir(current, folder)
		if err != nil {
			return "", nil
		}
		if valid {
			if _, err := gitRepositoryFormat(current, folder); err != nil {
				return "", nil
			}
			return filepath.Clean(folder), nil
		}
		if filepath.Dir(current) == current {
			return "", nil
		}
	}
}

// gitDiscoveredRoot applies Git's repository format check to the repository
// dir discovered for root. core.bare leaves no working tree, and the upstream
// CLI resolves the empty cdup to the caller folder. Rejected config is fatal.
func gitDiscoveredRoot(root, dir, folder string) string {
	bare, err := gitRepositoryFormat(dir, folder)
	switch {
	case err != nil:
		return ""
	case bare:
		return filepath.Clean(folder)
	}
	return root
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

// gitfilePointer reads Git's literal gitfile format. Only trailing CR/LF bytes
// are removed; whitespace and interior newlines belong to the path. Git caps
// gitfiles at 1 MiB and uses a C string for the resulting filesystem path.
// Reference: https://github.com/git/git/blob/master/setup.c (read_gitfile_raw).
func gitfilePointer(path string) (string, error) {
	const maxGitfileSize = 1 << 20
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > maxGitfileSize {
		return "", nil
	}
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	// Bound the read too, in case the file grows after Stat.
	content, err := io.ReadAll(io.LimitReader(file, maxGitfileSize+1))
	if err != nil {
		return "", err
	}
	if len(content) > maxGitfileSize || !strings.HasPrefix(string(content), "gitdir: ") {
		return "", nil
	}
	value := strings.TrimRight(string(content[8:]), "\r\n")
	if end := strings.IndexByte(value, 0); end >= 0 {
		value = value[:end]
	}
	return value, nil
}

// isGitDir is Git's repository test: HEAD, plus accessible objects and refs
// in the common directory selected by GIT_COMMON_DIR or commondir. Paths retain
// components until filesystem traversal; cleaning first changes symlink/..
// and regular-file/.. semantics.
func isGitDir(dir, folder string) (bool, error) {
	if !validGitHEAD(dir + string(os.PathSeparator) + "HEAD") {
		return false, nil
	}
	common, _, err := gitCommon(dir, folder)
	if err != nil {
		return false, err
	}
	// Git uses access(X_OK), including for executable regular files. Linux
	// and macOS are the supported host platforms.
	objects := common + string(os.PathSeparator) + "objects"
	if override, set := os.LookupEnv("GIT_OBJECT_DIRECTORY"); set {
		objects = override
		if objects != "" && !filepath.IsAbs(objects) {
			objects = folder + string(os.PathSeparator) + objects
		}
	}
	return syscall.Access(objects, 1) == nil &&
		syscall.Access(common+string(os.PathSeparator)+"refs", 1) == nil, nil
}

// gitCommon follows get_common_dir in Git's setup.c. GIT_COMMON_DIR is used
// verbatim instead of reading commondir; an empty value leaves the absolute
// /objects, /refs and /config paths. shared reports that either selected the
// common folder, as for a linked worktree.
func gitCommon(dir, folder string) (common string, shared bool, err error) {
	if common, set := os.LookupEnv("GIT_COMMON_DIR"); set {
		if common != "" && !filepath.IsAbs(common) {
			common = folder + string(os.PathSeparator) + common
		}
		return common, true, nil
	}
	return gitCommonDirectory(dir)
}

// gitCommonDirectory follows get_common_dir_noenv in Git's setup.c. This is
// separate from the upstream CLI's permissive common-folder mount parser.
func gitCommonDirectory(dir string) (string, bool, error) {
	path := dir + string(os.PathSeparator) + "commondir"
	_, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return dir, false, nil
	}
	if err != nil {
		return "", false, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", false, err
	}
	// Do not read special files such as FIFOs during repository discovery.
	if !info.Mode().IsRegular() {
		return "", false, errors.New("Git common-directory pointer is not a regular file")
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", false, err
	}
	if len(content) == 0 {
		return "", false, errors.New("empty Git common-directory pointer")
	}
	common := strings.TrimRight(string(content), "\r\n")
	if end := strings.IndexByte(common, 0); end >= 0 {
		common = common[:end]
	}
	if !filepath.IsAbs(common) {
		common = dir + string(os.PathSeparator) + common
	}
	common, err = gitCeilingPath(common)
	return common, true, err
}

// validGitHEAD follows Git's validate_headref repository admission test, not
// full refname validation. Git inspects at most 255 bytes and accepts a refs/
// symbolic target or a hexadecimal object ID followed by arbitrary bytes.
func validGitHEAD(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(path)
		return err == nil && strings.HasPrefix(target, "refs/")
	}
	if !info.Mode().IsRegular() {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	content, err := io.ReadAll(io.LimitReader(file, 255))
	if err != nil {
		return false
	}
	value := string(content)
	if end := strings.IndexByte(value, 0); end >= 0 {
		value = value[:end]
	}
	if rest, ok := strings.CutPrefix(value, "ref:"); ok {
		if strings.HasPrefix(strings.TrimLeft(rest, " \t\n\r"), "refs/") {
			return true
		}
	}
	if len(value) < 40 {
		return false
	}
	for _, c := range []byte(value[:40]) {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func isFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
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
			entry, err = gitCeilingPath(entry)
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

// gitCeilingPath follows Git's strbuf_realpath component rules for ceilings
// and explicit working trees. In particular,
// existing regular files can precede dot components; Go's EvalSymlinks rejects
// those paths. Missing intermediate components still invalidate the ceiling.
// Reference: https://github.com/git/git/blob/master/abspath.c
func gitCeilingPath(path string) (string, error) {
	separator := func(r rune) bool { return r == '/' || r == rune(os.PathSeparator) }
	root := func(p string) (string, string) {
		volume := filepath.VolumeName(p)
		return volume + string(os.PathSeparator), strings.TrimLeftFunc(p[len(volume):], separator)
	}
	resolved, remaining := root(path)
	links := 0
	for remaining != "" {
		remaining = strings.TrimLeftFunc(remaining, separator)
		end := strings.IndexFunc(remaining, separator)
		if end < 0 {
			end = len(remaining)
		}
		component := remaining[:end]
		remaining = remaining[end:]
		switch component {
		case "", ".":
			continue
		case "..":
			resolved = filepath.Dir(resolved)
			continue
		}
		candidate := filepath.Join(resolved, component)
		info, err := os.Lstat(candidate)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) || remaining != "" {
				return "", err
			}
			resolved = candidate
			continue
		}
		if info.Mode()&os.ModeSymlink == 0 {
			resolved = candidate
			continue
		}
		// Git allows 33 expansions, then rejects a further symbolic link.
		if links > 32 {
			return "", errors.New("too many symbolic links in Git ceiling")
		}
		links++
		target, err := os.Readlink(candidate)
		if err != nil {
			return "", err
		}
		if filepath.IsAbs(target) {
			resolved, target = root(target)
		}
		remaining = target + remaining
	}
	return resolved, nil
}
