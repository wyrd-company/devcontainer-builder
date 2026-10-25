// relationships:
//   references: [git_chdir_linux, git_core_worktree_chdir_test]

package workspace

import (
	"fmt"
	"os"
	"path/filepath"
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
			err = observeChain(func() error {
				if got, err := GitRoot(folder); err != nil || got != want {
					return fmt.Errorf("GitRoot = %q, %v; want %q", got, err, want)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// chdir needs only search permission on the folder a relative core.worktree
// names, and Linux's getcwd names that folder without read permission. Other
// hosts' getcwd may need it, as Git's does there. The expectation is fixed,
// independent of the host Git.
func TestGitRootCoreWorktreeSearchOnlyFolder(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses permissions")
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(base, "tree")
	makeRepo(t, tree)
	makeWorktreeObstacles(t, base)
	writeFile(t, filepath.Join(tree, ".git", "config"), "[core]\n\trepositoryformatversion = 0\n\tworktree = ../../sealed\n")
	t.Setenv("GIT_CEILING_DIRECTORIES", base)
	folder := mkdir(t, filepath.Join(tree, "app"))
	if got, err := GitRoot(folder); err != nil || got != filepath.Join(base, "sealed") {
		t.Fatalf("GitRoot = %q, %v; want %q", got, err, filepath.Join(base, "sealed"))
	}
}
