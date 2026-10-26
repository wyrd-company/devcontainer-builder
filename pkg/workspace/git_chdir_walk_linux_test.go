// relationships:
//   references: [git_chdir_linux, git_chdir_walk_linux, git_core_worktree_proc_linux_test]

package workspace

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// kernelName opens value from dir as gitChdir does and returns the
// descriptor and the name /proc gives it, or the kernel's error.
func kernelName(t *testing.T, dir, value string) (int, string, error) {
	t.Helper()
	const flags = unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC
	parent, err := unix.Open(dir, flags, 0)
	if err != nil {
		return -1, "", err
	}
	defer unix.Close(parent)
	target, err := unix.Openat(parent, value, flags, 0)
	if err != nil {
		return -1, "", err
	}
	t.Cleanup(func() { unix.Close(target) })
	name, err := os.Readlink("/proc/self/fd/" + strconv.Itoa(target))
	if err != nil {
		t.Fatal(err)
	}
	return target, name, nil
}

// requireWalkMatchesKernel checks that the walk names the folder the kernel
// opens, or fails with the kernel's error.
func requireWalkMatchesKernel(t *testing.T, dir, value string) {
	t.Helper()
	err := observeChain(func() error {
		target, want, kerr := kernelName(t, dir, value)
		got, err := gitWalkCwd(dir, value, target)
		if kerr != nil {
			if got != "" || !errors.Is(err, kerr) {
				return fmt.Errorf("gitWalkCwd = %q, %v; want the kernel's %v", got, err, kerr)
			}
			return nil
		}
		if got != want || err != nil {
			return fmt.Errorf("gitWalkCwd = %q, %v; want %q", got, err, want)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// linkChain makes n symbolic links under folder, each naming the next and
// the last naming target, and returns the first.
func linkChain(t *testing.T, folder, target string, n int) string {
	t.Helper()
	next := target
	for i := n; i > 0; i-- {
		link := filepath.Join(folder, "link"+strconv.Itoa(i))
		if err := os.Symlink(next, link); err != nil {
			t.Fatal(err)
		}
		next = link
	}
	return next
}

// The walk follows symbolic links before the components after them, as the
// kernel's lookup does, and names the folder by its physical path.
func TestGitWalkCwdMatchesKernel(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := mkdir(t, filepath.Join(base, "a"))
	mkdir(t, filepath.Join(a, "b", "c"))
	mkdir(t, filepath.Join(base, "x", "y"))
	writeFile(t, filepath.Join(a, "file"), "")
	symlink := func(target, link string) {
		t.Helper()
		if err := os.Symlink(target, filepath.Join(base, link)); err != nil {
			t.Fatal(err)
		}
	}
	symlink("../x/y", "a/rel")
	symlink(filepath.Join(base, "x"), "a/abs")
	symlink("b", "a/short")
	symlink("missing", "a/dangling")
	symlink("file", "a/tofile")
	symlink("loop", "a/loop")
	// A link longer than the first buffer the walk reads it into.
	symlink(strings.Repeat("./", 150)+"b/c", "a/long")
	chains := mkdir(t, filepath.Join(base, "chains"))
	symlink(linkChain(t, mkdir(t, filepath.Join(chains, "40")), filepath.Join(base, "x"), 40), "a/chain40")
	symlink(linkChain(t, mkdir(t, filepath.Join(chains, "40-relative")), "../../x", 39), "a/chain40-relative")
	symlink(linkChain(t, mkdir(t, filepath.Join(chains, "41")), filepath.Join(base, "x"), 40), "a/chain41")
	cases := []struct{ dir, value string }{
		{a, "b/c"},
		{a, "b//./c/"},
		{a, "rel"},
		{a, "rel/.."},
		{a, "rel/../../a/short/c"},
		{a, "abs/y"},
		{a, "abs/y/../.."},
		{a, "long"},
		{a, "long/.."},
		{a, "chain40"},
		{a, "chain40-relative/y"},
		{filepath.Join(a, "short"), "../rel"},
		{filepath.Join(a, "rel"), ".."},
		{filepath.Join(a, "short", "c"), strings.Repeat("../", 64)},
		{".", "."},
		// The kernel's lookup fails, and so does the walk.
		{a, "chain41"},
		{a, "missing"},
		{a, "dangling"},
		{a, "file"},
		{a, "tofile"},
		{a, "file/.."},
		{a, "loop"},
	}
	for _, c := range cases {
		name := strings.TrimPrefix(c.dir, base) + "+" + c.value
		if len(name) > 40 {
			name = name[:40] + "..."
		}
		t.Run(name, func(t *testing.T) {
			requireWalkMatchesKernel(t, c.dir, c.value)
		})
	}
}

// A folder that a lookup now selects is not named for the folder the
// kernel opened before it was renamed.
func TestGitWalkCwdRenamedFolder(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	moved := mkdir(t, filepath.Join(base, "a", "b"))
	target, want, err := kernelName(t, filepath.Join(base, "a"), "b")
	if err != nil || want != moved {
		t.Fatalf("kernel name = %q, %v; want %q", want, err, moved)
	}
	if err := os.Rename(moved, filepath.Join(base, "a", "old")); err != nil {
		t.Fatal(err)
	}
	mkdir(t, moved)
	if got, err := gitWalkCwd(filepath.Join(base, "a"), "b", target); got != "" || !errors.Is(err, unix.ESTALE) {
		t.Fatalf("gitWalkCwd = %q, %v; want \"\", ESTALE", got, err)
	}
}

// A link the kernel follows to an object rather than by its text, as /proc
// gives for a descriptor, does not name a folder that its text names.
func TestGitWalkCwdMagicLink(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	removed := mkdir(t, filepath.Join(base, "removed"))
	fd, err := unix.Open(removed, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { unix.Close(fd) })
	if err := os.Remove(removed); err != nil {
		t.Fatal(err)
	}
	// The kernel names the removed folder with this suffix.
	decoy := mkdir(t, removed+" (deleted)")
	target, name, err := kernelName(t, "/proc/self/fd", strconv.Itoa(fd))
	if err != nil || name != decoy {
		t.Fatalf("kernel name = %q, %v; want %q", name, err, decoy)
	}
	if got, err := gitWalkCwd("/proc/self/fd", strconv.Itoa(fd), target); got != "" || !errors.Is(err, unix.ESTALE) {
		t.Fatalf("gitWalkCwd = %q, %v; want \"\", ESTALE", got, err)
	}
}

// A name longer than PATH_MAX fails, as getcwd fails on it, though the walk
// reaches the folder.
func TestGitWalkCwdLongName(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	component := strings.Repeat("d", 200)
	deep := base
	for len(deep) <= unix.PathMax {
		parent := openDeep(t, deep)
		if err := unix.Mkdirat(parent, component, 0o755); err != nil {
			t.Fatal(err)
		}
		deep += "/" + component
	}
	target := openDeep(t, deep)
	if got, err := gitWalkCwd(filepath.Dir(deep), component, target); got != "" || !errors.Is(err, unix.ENAMETOOLONG) {
		t.Fatalf("gitWalkCwd = %q, %v; want \"\", ENAMETOOLONG", got, err)
	}
}

// openDeep opens the folder path, which may exceed PATH_MAX, one component
// at a time from the root.
func openDeep(t *testing.T, path string) int {
	t.Helper()
	const flags = unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC
	fd, err := unix.Open("/", flags, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		next, err := unix.Openat(fd, c, flags, 0)
		unix.Close(fd)
		if err != nil {
			t.Fatal(err)
		}
		fd = next
	}
	t.Cleanup(func() { unix.Close(fd) })
	return fd
}

// The walk closes every descriptor it opens, whether it names the folder or
// fails.
func TestGitWalkCwdClosesDescriptors(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := mkdir(t, filepath.Join(base, "a"))
	mkdir(t, filepath.Join(a, "b"))
	writeFile(t, filepath.Join(a, "file"), "")
	if err := os.Symlink("b", filepath.Join(a, "link")); err != nil {
		t.Fatal(err)
	}
	linkChain(t, mkdir(t, filepath.Join(base, "chain")), a, 41)
	target, _, err := kernelName(t, a, "b")
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := kernelName(t, base, "a")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		dir, value string
		target     int
		want       error
	}{
		{a, "link", target, nil},
		{a, "missing", target, unix.ENOENT},
		{a, "file/x", target, unix.ENOTDIR},
		{base, "chain/link1", target, unix.ELOOP},
		{a, "link", other, unix.ESTALE},
		{"/missing-" + strconv.Itoa(os.Getpid()), "b", target, unix.ENOENT},
	}
	before := openDescriptors(t)
	for range 50 {
		for _, c := range cases {
			if _, err := gitWalkCwd(c.dir, c.value, c.target); !errors.Is(err, c.want) {
				t.Fatalf("gitWalkCwd(%q, %q) error = %v; want %v", c.dir, c.value, err, c.want)
			}
		}
	}
	if after := openDescriptors(t); !slices.Equal(after, before) {
		t.Fatalf("open descriptors = %v; want %v", after, before)
	}
}

// gitChdir closes every descriptor it opens, whether it names the folder or
// fails, including where the folder cannot be searched.
func TestGitChdirClosesDescriptors(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mkdir(t, filepath.Join(base, "b"))
	writeFile(t, filepath.Join(base, "file"), "")
	cases := []struct {
		value string
		want  error
	}{
		{"b", nil},
		{"missing", unix.ENOENT},
		{"file", unix.ENOTDIR},
	}
	if os.Geteuid() != 0 {
		locked := mkdir(t, filepath.Join(base, "locked"))
		if err := os.Chmod(locked, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Chmod(locked, 0o700) })
		cases = append(cases, struct {
			value string
			want  error
		}{"locked", unix.EACCES})
	}
	before := openDescriptors(t)
	for range 50 {
		for _, c := range cases {
			if _, err := gitChdir(base, c.value); !errors.Is(err, c.want) {
				t.Fatalf("gitChdir(%q) error = %v; want %v", c.value, err, c.want)
			}
		}
	}
	if after := openDescriptors(t); !slices.Equal(after, before) {
		t.Fatalf("open descriptors = %v; want %v", after, before)
	}
}

// openDescriptors lists this process's open descriptors.
func openDescriptors(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	// ReadDir's own descriptor is listed, with the same number each time
	// unless another stays open.
	return names
}
