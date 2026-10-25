// relationships:
//   references: [git_chdir_linux, git_core_worktree_chdir_test]

package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Linux allows 40 symbolic links in one path lookup, and Git's realpath 33
// expansions. Expectations are fixed, independent of the host Git.
func TestGitRootCoreWorktreeChdirLimits(t *testing.T) {
	cases := []struct {
		chain coreWorktreeChain
		found bool
	}{
		// Each chdir has its own limit, though the two chains exceed one.
		{coreWorktreeChain{"discovered", 20, 25}, true},
		{coreWorktreeChain{"discovered-top", 20, 25}, true},
		{coreWorktreeChain{"explicit", 20, 25}, true},
		{coreWorktreeChain{"gitfile", 20, 25}, true},
		{coreWorktreeChain{"discovered", 1, 40}, true},
		{coreWorktreeChain{"discovered", 1, 41}, false},
		{coreWorktreeChain{"self", 0, 40}, true},
		// Git's realpath limits a .git above the caller and gitfile targets.
		{coreWorktreeChain{"discovered", 33, 0}, true},
		{coreWorktreeChain{"discovered", 34, 0}, false},
		{coreWorktreeChain{"gitfile", 34, 0}, false},
		{coreWorktreeChain{"explicit-gitfile", 34, 0}, false},
		// Git's realpath starts from the physical folder Git found.
		{coreWorktreeChain{"alias", 33, 0}, true},
		// Git changes to a .git at the caller or GIT_DIR as named.
		{coreWorktreeChain{"discovered-top", 34, 0}, true},
		{coreWorktreeChain{"explicit", 34, 0}, true},
		{coreWorktreeChain{"explicit-relative", 34, 0}, true},
	}
	for _, c := range cases {
		t.Run(c.chain.String(), func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			folder, _, other := makeCoreWorktreeChain(t, "", base, c.chain)
			want := ""
			if c.found {
				want = other
			}
			if got, err := GitRoot(folder); err != nil || got != want {
				t.Fatalf("GitRoot = %q, %v; want %q", got, err, want)
			}
		})
	}
}

// Git's chdir arguments and its final cwd each stay below PATH_MAX while
// joining the repository folder and the value would exceed it, through either
// the physical (physical-long) or the named (named-long) repository folder.
func TestGitRootCoreWorktreePathMax(t *testing.T) {
	git, _ := exec.LookPath("git")
	deep := func(root string, n int, c string) string {
		for i := 0; i < n; i++ {
			root = filepath.Join(root, strings.Repeat(c, 200)+string(rune('a'+i)))
		}
		return mkdir(t, root)
	}
	for _, c := range []struct {
		name                         string
		adminDepth, treeDepth, depth int
	}{
		{"physical-long", 10, 0, 13},
		{"named-long", 0, 10, 13},
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
			if len(admin)+len(value) < 4096 && len(tree)+len(value) < 4096 {
				t.Fatalf("joined lengths %d and %d stay below PATH_MAX", len(admin)+len(value), len(tree)+len(value))
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
