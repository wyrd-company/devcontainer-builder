// relationships:
//   references: [gitfile_test, workspace, ../../reference/cli/src/spec-node/utils]

package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

func TestCommonDirGitfileParserRemainsPermissive(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	tree := filepath.Join(base, "tree")
	for _, args := range [][]string{
		{"init", "-q", repo},
		{"-C", repo, "commit", "-q", "--allow-empty", "-m", "initial"},
		{"-C", repo, "worktree", "add", "-q", "--relative-paths", tree, "-b", "topic"},
	} {
		cmd := exec.Command(git, args...)
		cmd.Env = testgit.Env()
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	pointer := "../repo/.git/worktrees/tree"
	for _, tc := range []struct{ name, content string }{
		{"literal", "gitdir: " + pointer + "\n"},
		{"leading-junk", "junk\ngitdir: " + pointer + "\n"},
		{"missing-space", "gitdir:" + pointer + "\n"},
		{"tab-separator", "gitdir:\t" + pointer + "\n"},
		{"newline-separator", "gitdir:\n" + pointer + "\n"},
		{"extra-line", "gitdir: " + pointer + "\njunk\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(tree, ".git"), []byte(tc.content), 0600); err != nil {
				t.Fatal(err)
			}
			resolved, err := Resolve(tree, false, false, Options{MountGitRoot: true, MountGitWorktreeCommonDir: true, Platform: "linux"})
			if err != nil {
				t.Fatal(err)
			}
			if resolved.WorkspaceMount.Source != tree || resolved.WorkspaceMount.Target != "/workspaces/tree" || resolved.WorkspaceFolder != "/workspaces/tree" {
				t.Fatalf("workspace mount/folder = %+v", resolved)
			}
			if resolved.AdditionalMount == nil || resolved.AdditionalMount.Source != filepath.Join(repo, ".git") || resolved.AdditionalMount.Target != "/workspaces/repo/.git" {
				t.Fatalf("additional mount = %+v; want common repository directory", resolved.AdditionalMount)
			}
		})
	}
}
