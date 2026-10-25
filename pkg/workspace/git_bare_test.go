// relationships:
//   references: [git, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

// Git tests each folder itself as a repository after its .git entry, so a bare
// repository or an administrative folder inside a working tree ends discovery.
func TestGitRootBareDirectoryMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	for _, name := range []string{"plain", "bare", "bare-child", "admin", "admin-child", "linked-admin", "invalid-bare", "empty-dotgit", "invalid-gitfile", "fatal-commondir", "ceiling", "common-env", "work-tree"} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			tree := mkdir(t, filepath.Join(base, "tree"))
			bare := filepath.Join(tree, "sub", "bare.git")
			for _, args := range [][]string{{"init", "-q", tree}, {"init", "-q", "--bare", bare}} {
				cmd := exec.Command(git, args...)
				cmd.Env = testgit.Env()
				if out, e := cmd.CombinedOutput(); e != nil {
					t.Fatalf("init: %v %s", e, out)
				}
			}
			remove := func(p string) {
				t.Helper()
				if e := os.RemoveAll(p); e != nil {
					t.Fatal(e)
				}
			}
			env := testgit.Env()
			set := func(key, value string) {
				env = append(env, key+"="+value)
				t.Setenv(key, value)
			}
			ceiling := base
			probe := bare
			switch name {
			case "plain":
				probe = filepath.Dir(bare)
			case "bare-child":
				probe = filepath.Join(bare, "refs", "heads")
			case "admin":
				probe = filepath.Join(tree, ".git")
			case "admin-child":
				probe = filepath.Join(tree, ".git", "objects")
			case "linked-admin":
				// A linked worktree's administrative folder shares the main
				// repository's objects and refs through commondir.
				probe = mkdir(t, filepath.Join(tree, ".git", "worktrees", "linked"))
				writeFile(t, filepath.Join(probe, "HEAD"), "ref: refs/heads/linked\n")
				writeFile(t, filepath.Join(probe, "commondir"), "../..\n")
			case "invalid-bare":
				remove(filepath.Join(bare, "refs"))
			case "empty-dotgit":
				// A .git folder that is not a repository does not hide the
				// repository test of its parent folder.
				mkdir(t, filepath.Join(bare, ".git"))
			case "invalid-gitfile":
				// An invalid .git file is fatal before the folder itself is tested.
				writeFile(t, filepath.Join(bare, ".git"), "not a gitfile\n")
			case "fatal-commondir":
				writeFile(t, filepath.Join(bare, "commondir"), "absent/child\n")
			case "ceiling":
				ceiling = bare
				probe = filepath.Join(bare, "refs", "heads")
			case "common-env":
				common := mkdir(t, filepath.Join(base, "common"))
				mkdir(t, filepath.Join(common, "objects"))
				mkdir(t, filepath.Join(common, "refs"))
				remove(filepath.Join(bare, "objects"))
				remove(filepath.Join(bare, "refs"))
				set("GIT_COMMON_DIR", common)
			case "work-tree":
				set("GIT_WORK_TREE", tree)
			}
			set("GIT_CEILING_DIRECTORIES", ceiling)
			cmd := exec.Command(git, "rev-parse", "--show-cdup")
			cmd.Dir = probe
			cmd.Env = env
			out, e := cmd.Output()
			want := ""
			if e == nil {
				want = filepath.Clean(filepath.Join(probe, strings.TrimSpace(string(out))))
			}
			got, e := GitRoot(probe)
			if e != nil || got != want {
				t.Fatalf("GitRoot = %q, %v; git = %q", got, e, want)
			}
			resolved, e := Resolve(probe, false, false, Options{MountGitRoot: true, Platform: "linux"})
			if e != nil {
				t.Fatal(e)
			}
			mount := want
			if mount == "" {
				mount = probe
			}
			if resolved.WorkspaceMount.Source != mount {
				t.Fatalf("mount = %q; want %q", resolved.WorkspaceMount.Source, mount)
			}
		})
	}
}
