// relationships:
//   references: [git_work_tree, git_chdir_linux, git_chdir_other]

package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

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
				t.Fatalf("GitRoot(%s) = %q, %v; git = %q", folder, got, e, want)
			}
			resolved, e := Resolve(folder, false, false, Options{MountGitRoot: true, Platform: "linux"})
			if e != nil {
				t.Fatal(e)
			}
			mount := want
			if mount == "" {
				mount = folder
			}
			if resolved.WorkspaceMount.Source != mount {
				t.Fatalf("mount(%s) = %q; want %q", folder, resolved.WorkspaceMount.Source, mount)
			}
		})
	}
}
