// relationships:
//   references: [git, git_work_tree_test, workspace]

package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

func TestValidGitDirMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	base := t.TempDir()
	repo := mkdir(t, filepath.Join(base, "repo"))
	sub := mkdir(t, filepath.Join(repo, "app", "src"))
	plain := mkdir(t, filepath.Join(base, "plain"))
	cmd := exec.Command(git, "init", "-q", repo)
	cmd.Env = testgit.Env()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %s: %v", out, err)
	}
	gitdir := filepath.Join(repo, ".git")
	pointer := filepath.Join(base, "pointer")
	if err := os.WriteFile(pointer, []byte("gitdir: repo/.git\n"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, folder, dir string
		tree              *string
	}{
		{"nested-absolute", sub, gitdir, nil},
		{"nested-relative", sub, "../../.git", nil},
		{"outside", plain, gitdir, nil},
		{"gitfile", plain, pointer, nil},
		{"lexical-caller", filepath.Join(alias, "app", "src"), gitdir, nil},
		{"work-tree-ancestor", sub, gitdir, selectionString(repo)},
		{"work-tree-outside", plain, gitdir, selectionString(repo)},
		{"work-tree-relative", plain, gitdir, selectionString("../repo")},
		{"work-tree-empty", sub, gitdir, selectionString("")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GIT_DIR", tc.dir)
			t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(sub))
			env := append(testgit.Env(), "GIT_DIR="+tc.dir, "GIT_CEILING_DIRECTORIES="+filepath.Dir(sub))
			if tc.tree != nil {
				t.Setenv("GIT_WORK_TREE", *tc.tree)
				env = append(env, "GIT_WORK_TREE="+*tc.tree)
			}
			cmd := exec.Command(git, "rev-parse", "--show-cdup")
			cmd.Dir = tc.folder
			cmd.Env = env
			out, probeErr := cmd.Output()
			want := ""
			if probeErr == nil {
				cdup := strings.TrimSpace(string(out))
				if filepath.IsAbs(cdup) {
					want = filepath.Clean(cdup)
				} else {
					want = filepath.Join(tc.folder, cdup)
				}
			}
			got, err := GitRoot(tc.folder)
			if err != nil || got != want {
				t.Fatalf("GitRoot = %q, %v; Git = %q (%v)", got, err, want, probeErr)
			}
			resolved, err := Resolve(tc.folder, false, false, Options{MountGitRoot: true, Platform: "linux"})
			source := want
			if source == "" {
				source = tc.folder
			}
			if err != nil || resolved.WorkspaceMount.Source != source {
				t.Fatalf("mount = %+v, %v; want source %q", resolved, err, source)
			}
		})
	}
}

func selectionString(value string) *string { return &value }
