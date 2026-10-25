// relationships:
//   references: [git_work_tree, git_chdir_linux, git_chdir_darwin, git_chdir_other]

package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

// coreWorktreeChain is a relative core.worktree layout. The repository folder
// is reached through links symbolic links, and the value through valueLinks.
type coreWorktreeChain struct {
	route             string
	links, valueLinks int
}

func (c coreWorktreeChain) String() string {
	return fmt.Sprintf("%s-g%d-w%d", c.route, c.links, c.valueLinks)
}

// coreWorktreeChainRoutes reach the repository folder as Git discovery does:
// a .git found above the caller (discovered) or at it (discovered-top), the
// same through a symlinked caller folder (alias), GIT_DIR (explicit,
// explicit-relative, explicit-gitfile), a gitfile above or at the caller, and
// a caller inside the repository folder itself (self).
var coreWorktreeChainRoutes = []string{"discovered", "discovered-top", "alias", "explicit", "explicit-relative", "gitfile", "gitfile-top", "explicit-gitfile", "self"}

// symlinkChain creates prefix0 -> prefix1 -> ... -> final in folder with
// count links and returns its first name, or final without links.
func symlinkChain(t *testing.T, folder, prefix string, count int, final string) string {
	t.Helper()
	for i := count - 1; i >= 0; i-- {
		target := final
		if i < count-1 {
			target = fmt.Sprintf("%s%d", prefix, i+1)
		}
		if err := os.Symlink(target, filepath.Join(folder, fmt.Sprintf("%s%d", prefix, i))); err != nil {
			t.Fatal(err)
		}
	}
	if count == 0 {
		return final
	}
	return prefix + "0"
}

// chainAttempts bounds the observations of one layout. While mounts change
// anywhere on the host, Linux can fail one lookup of more than 20 symbolic
// links with ELOOP, below its limit of 40; this was measured on the gate host
// for Git and for plain stat. A difference in semantics recurs on every
// attempt, so only such a spurious failure is retried.
const chainAttempts = 5

// observeChain repeats observe until it succeeds or the attempts end, and
// returns the last failure.
func observeChain(observe func() error) error {
	var err error
	for range chainAttempts {
		if err = observe(); err == nil {
			return nil
		}
	}
	return err
}

// makeCoreWorktreeChain builds the layout under base, sets its environment,
// and returns the caller folder, the Git environment and the working tree.
// Without a git command it writes the minimal repository instead.
func makeCoreWorktreeChain(t *testing.T, git, base string, c coreWorktreeChain) (string, []string, string) {
	t.Helper()
	env := testgit.Env()
	tree := filepath.Join(base, "tree")
	if git == "" {
		makeRepo(t, tree)
	} else if out, err := exec.Command(git, "init", "-q", tree).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	mkdir(t, filepath.Join(tree, "app"))
	other := mkdir(t, filepath.Join(base, "other"))
	admin := filepath.Join(tree, ".git")
	gitfile := strings.Contains(c.route, "gitfile")
	if c.links > 0 || gitfile {
		moved := filepath.Join(base, "admin")
		if err := os.Rename(admin, moved); err != nil {
			t.Fatal(err)
		}
		if gitfile {
			target := moved
			if c.links > 0 {
				target = filepath.Join(tree, symlinkChain(t, tree, "a", c.links, "../admin"))
			}
			writeFile(t, admin, "gitdir: "+target+"\n")
		} else if err := os.Symlink(symlinkChain(t, tree, "a", c.links-1, "../admin"), admin); err != nil {
			t.Fatal(err)
		}
		admin = moved
	}
	value, err := filepath.Rel(admin, filepath.Join(base, symlinkChain(t, base, "w", c.valueLinks, "other")))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(admin, "config"), "[core]\n\trepositoryformatversion = 0\n\tworktree = "+value+"\n")
	set := func(key, value string) {
		env = append(env, key+"="+value)
		t.Setenv(key, value)
	}
	set("GIT_CEILING_DIRECTORIES", base)
	folder := filepath.Join(tree, "app")
	switch c.route {
	case "discovered-top", "gitfile-top":
		folder = tree
	case "alias":
		if err := os.Symlink("tree", filepath.Join(base, "alias")); err != nil {
			t.Fatal(err)
		}
		folder = filepath.Join(base, "alias", "app")
	case "explicit", "explicit-gitfile":
		set("GIT_DIR", filepath.Join(tree, ".git"))
	case "explicit-relative":
		set("GIT_DIR", filepath.Join("..", ".git"))
	case "self":
		folder = admin
	}
	return folder, env, other
}

// Git changes to the repository folder and then to a relative core.worktree,
// so each lookup has its own symbolic link limit. A .git folder found above
// the caller and a gitfile target first pass through Git's own realpath,
// which has a lower limit. Upstream mounts the probe's cdup, or the caller
// folder when the probe fails.
func TestGitRootCoreWorktreeChdirMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	var cases []coreWorktreeChain
	for _, route := range coreWorktreeChainRoutes {
		for _, links := range [][2]int{{0, 0}, {20, 25}, {25, 25}, {1, 40}, {1, 41}, {33, 0}, {33, 20}, {34, 0}, {40, 1}} {
			if route == "self" && links[0] > 0 {
				continue
			}
			// Discovery stats .git through the caller's symlinked folder, one
			// lookup past the limit, before core.worktree applies.
			if route == "alias" && links[0] == 40 {
				continue
			}
			cases = append(cases, coreWorktreeChain{route, links[0], links[1]})
		}
	}
	for _, c := range cases {
		t.Run(c.String(), func(t *testing.T) {
			folder, env, _ := makeCoreWorktreeChain(t, git, t.TempDir(), c)
			err := observeChain(func() error {
				cmd := exec.Command(git, "rev-parse", "--show-cdup")
				cmd.Dir = folder
				cmd.Env = env
				out, e := cmd.Output()
				want := ""
				if e == nil {
					cdup := strings.TrimSpace(string(out))
					want = filepath.Join(folder, cdup)
					if filepath.IsAbs(cdup) {
						want = filepath.Clean(cdup)
					}
				}
				if got, e := GitRoot(folder); e != nil || got != want {
					return fmt.Errorf("GitRoot(%s) = %q, %v; git = %q", folder, got, e, want)
				}
				resolved, e := Resolve(folder, false, false, Options{MountGitRoot: true, Platform: "linux"})
				if e != nil {
					return e
				}
				mount := want
				if mount == "" {
					mount = folder
				}
				if resolved.WorkspaceMount.Source != mount {
					return fmt.Errorf("mount(%s) = %q; want %q", folder, resolved.WorkspaceMount.Source, mount)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// Git's chdir arguments and its final cwd each stay below the host's PATH_MAX
// while joining the repository folder and the value would exceed it, through
// either the physical (physical-long) or the named (named-long) repository
// folder.
func TestGitRootCoreWorktreePathMax(t *testing.T) {
	git, _ := exec.LookPath("git")
	deep := func(root string, n int, c string) string {
		for i := 0; i < n; i++ {
			root = filepath.Join(root, strings.Repeat(c, 200)+string(rune('a'+i)))
		}
		return mkdir(t, root)
	}
	// Each folder name takes 201 bytes, so Linux's PATH_MAX of 4096 gives
	// depths of 10 and 13.
	half, most := unix.PathMax/2/201, unix.PathMax*2/3/201
	for _, c := range []struct {
		name                         string
		adminDepth, treeDepth, depth int
	}{
		{"physical-long", half, 0, most},
		{"named-long", 0, half, most},
	} {
		t.Run(c.name, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			tree := deep(filepath.Join(base, "t"), c.treeDepth, "x")
			makeRepo(t, tree)
			admin := filepath.Join(deep(filepath.Join(base, "a"), c.adminDepth, "y"), "admin")
			if err := os.Rename(filepath.Join(tree, ".git"), admin); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(admin, filepath.Join(tree, ".git")); err != nil {
				t.Fatal(err)
			}
			other := mkdir(t, filepath.Join(deep(filepath.Join(base, "o"), c.depth, "z"), "other"))
			value, err := filepath.Rel(admin, other)
			if err != nil {
				t.Fatal(err)
			}
			if len(admin)+len(value) < unix.PathMax && len(tree)+len(value) < unix.PathMax {
				t.Fatalf("joined lengths %d and %d stay below PATH_MAX", len(admin)+len(value), len(tree)+len(value))
			}
			for _, path := range []string{tree, admin, value, other} {
				if len(path) >= unix.PathMax {
					t.Fatalf("%d bytes reach PATH_MAX: %s", len(path), path)
				}
			}
			writeFile(t, filepath.Join(admin, "config"), "[core]\n\trepositoryformatversion = 0\n\tworktree = "+value+"\n")
			t.Setenv("GIT_CEILING_DIRECTORIES", base)
			folder := mkdir(t, filepath.Join(tree, "app"))
			if git != "" {
				cmd := exec.Command(git, "rev-parse", "--show-cdup")
				cmd.Dir = folder
				if out, err := cmd.Output(); err != nil || strings.TrimSpace(string(out)) != other {
					t.Fatalf("git = %q, %v; want %q", out, err, other)
				}
			}
			if got, err := GitRoot(folder); err != nil || got != other {
				t.Fatalf("GitRoot = %q, %v; want %q", got, err, other)
			}
		})
	}
}

// The value passes, through symbolic links, a physical folder whose path
// exceeds the host's PATH_MAX. Git's chdir resolves it in the kernel, so a
// value that climbs back to a short folder selects it (passes). Git resolves
// its final cwd again, which fails when the value ends in that folder (ends).
func TestGitRootCoreWorktreeIntermediatePathMax(t *testing.T) {
	for _, c := range []struct {
		name  string
		climb bool
	}{
		{"passes", true},
		{"ends", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			tree := filepath.Join(base, "tree")
			makeRepo(t, tree)
			other := mkdir(t, filepath.Join(base, "other"))
			// A path longer than PATH_MAX is created one folder at a time.
			var names []string
			parent, err := unix.Open(base, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i <= unix.PathMax/200; i++ {
				name := strings.Repeat("k", 199) + string(rune('a'+i))
				names = append(names, name)
				err := unix.Mkdirat(parent, name, 0o755)
				var child int
				if err == nil {
					child, err = unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
				}
				unix.Close(parent)
				if err != nil {
					t.Fatal(err)
				}
				parent = child
			}
			unix.Close(parent)
			// low names the first half absolutely; deep in it names the rest.
			half := len(names) / 2
			low := filepath.Join(append([]string{base}, names[:half]...)...)
			if err := os.Symlink(low, filepath.Join(base, "low")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(names[half:]...), filepath.Join(low, "deep")); err != nil {
				t.Fatal(err)
			}
			if physical := len(base) + 201*len(names); physical <= unix.PathMax {
				t.Fatalf("physical path of %d bytes stays below PATH_MAX", physical)
			}
			value, want := "../../low/deep", ""
			if c.climb {
				value, want = value+strings.Repeat("/..", len(names))+"/other", other
			}
			writeFile(t, filepath.Join(tree, ".git", "config"), "[core]\n\trepositoryformatversion = 0\n\tworktree = "+value+"\n")
			t.Setenv("GIT_CEILING_DIRECTORIES", base)
			folder := mkdir(t, filepath.Join(tree, "app"))
			if git, err := exec.LookPath("git"); err == nil {
				cmd := exec.Command(git, "rev-parse", "--show-cdup")
				cmd.Dir = folder
				out, err := cmd.Output()
				if got := strings.TrimSpace(string(out)); (err == nil) != (want != "") || got != want {
					t.Fatalf("git = %q, %v; want %q", out, err, want)
				}
			}
			if got, err := GitRoot(folder); err != nil || got != want {
				t.Fatalf("GitRoot = %q, %v; want %q", got, err, want)
			}
			mount := want
			if mount == "" {
				mount = folder
			}
			resolved, err := Resolve(folder, false, false, Options{MountGitRoot: true, Platform: "linux"})
			if err != nil || resolved.WorkspaceMount.Source != mount {
				t.Fatalf("mount = %q, %v; want %q", resolved.WorkspaceMount.Source, err, mount)
			}
		})
	}
}

// chdir needs only search permission on the repository folder, which Git
// changes to before a relative core.worktree.
func TestGitRootCoreWorktreeSearchOnlyRepository(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permissions")
	}
	git, _ := exec.LookPath("git")
	for _, route := range []string{"discovered", "explicit"} {
		t.Run(route, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			tree := filepath.Join(base, "tree")
			makeRepo(t, tree)
			other := mkdir(t, filepath.Join(base, "other"))
			admin := filepath.Join(tree, ".git")
			writeFile(t, filepath.Join(admin, "config"), "[core]\n\trepositoryformatversion = 0\n\tworktree = ../../other\n")
			t.Setenv("GIT_CEILING_DIRECTORIES", base)
			if route == "explicit" {
				t.Setenv("GIT_DIR", admin)
			}
			folder := mkdir(t, filepath.Join(tree, "app"))
			if err := os.Chmod(admin, 0o100); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(admin, 0o755) })
			if git != "" {
				cmd := exec.Command(git, "rev-parse", "--show-cdup")
				cmd.Dir = folder
				if out, err := cmd.Output(); err != nil || strings.TrimSpace(string(out)) != other {
					t.Fatalf("git = %q, %v; want %q", out, err, other)
				}
			}
			if got, err := GitRoot(folder); err != nil || got != other {
				t.Fatalf("GitRoot = %q, %v; want %q", got, err, other)
			}
		})
	}
}

// Following a relative core.worktree leaves the process's working folder, and
// that of every thread, unchanged.
func TestGitRootCoreWorktreeKeepsWorkingFolder(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(base, "tree")
	makeRepo(t, tree)
	other := mkdir(t, filepath.Join(base, "other"))
	writeFile(t, filepath.Join(tree, ".git", "config"), "[core]\n\trepositoryformatversion = 0\n\tworktree = ../../other\n")
	t.Setenv("GIT_CEILING_DIRECTORIES", base)
	folder := mkdir(t, filepath.Join(tree, "app"))
	cwd := mkdir(t, filepath.Join(base, "cwd"))
	writeFile(t, filepath.Join(cwd, "marker"), "")
	t.Chdir(cwd)
	for range 20 {
		if got, err := GitRoot(folder); err != nil || got != other {
			t.Fatalf("GitRoot = %q, %v; want %q", got, err, other)
		}
	}
	// Each locked goroutine takes an idle thread, as a later caller would.
	errs := make(chan error, 64)
	for range cap(errs) {
		go func() {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			_, err := os.Stat("marker")
			errs <- err
		}()
	}
	for range cap(errs) {
		if err := <-errs; err != nil {
			t.Fatalf("relative lookup after GitRoot: %v", err)
		}
	}
	if got, err := os.Getwd(); err != nil || got != cwd {
		t.Fatalf("Getwd = %q, %v; want %q", got, err, cwd)
	}
}
