// relationships:
//   references: [git, gitfile_test, git_work_tree, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

// Git prefixes a relative gitfile target with the gitfile's folder and keeps
// its components: the filesystem follows a symbolic link before a later ..,
// and rejects a regular file or a missing folder before one. The tree folder
// holds the gitfile and link, a symbolic link to deep/x/y. alias is a symbolic
// link to tree, so a caller below it keeps its symlinked name.
func TestGitfileTargetTraversalMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	// root is Git's root from tree/app, relative to the case folder; "" when Git
	// rejects the gitfile.
	cases := []struct {
		name, target string
		repos        map[string]string
		root         string
	}{
		{"physical-only", "link/../../admin", map[string]string{"deep/admin": ""}, "tree"},
		{"lexical-only", "link/../admin", map[string]string{"tree/admin": ""}, ""},
		{"lexical-bare-decoy", "link/../admin", map[string]string{"tree/admin": "[core]\n\tbare = true\n", "deep/x/admin": ""}, "tree"},
		{"physical-worktree", "link/../admin", map[string]string{"tree/admin": "", "deep/x/admin": "[core]\n\tworktree = $BASE/other\n"}, "other"},
		{"physical-relative-worktree", "link/../admin", map[string]string{"tree/admin": "", "deep/x/admin": "[core]\n\tworktree = ../../../other\n"}, "other"},
		{"physical-bare", "link/../../admin", map[string]string{"deep/admin": "[core]\n\tbare = true\n"}, "tree/app"},
		{"regular-file-parent", "file/../admin", map[string]string{"tree/admin": ""}, ""},
		{"missing-parent", "missing/../admin", map[string]string{"tree/admin": ""}, ""},
		{"physical-commondir", "link/../worktree", map[string]string{"main": "", "deep/x/worktree": "commondir:../../../main"}, "tree"},
		{"plain", "../admin", map[string]string{"admin": ""}, "tree"},
		{"dot-components", "./admin/./", map[string]string{"tree/admin": ""}, "tree"},
		{"absolute-link", "$BASE/tree/link/../../admin", map[string]string{"deep/admin": ""}, "tree"},
	}
	for _, route := range []string{"discovered", "discovered-above", "symlinked-folder", "explicit"} {
		for _, c := range cases {
			t.Run(c.name+"/"+route, func(t *testing.T) {
				base := t.TempDir()
				tree := mkdir(t, filepath.Join(base, "tree"))
				mkdir(t, filepath.Join(tree, "app"))
				mkdir(t, filepath.Join(base, "deep", "x", "y"))
				mkdir(t, filepath.Join(base, "other"))
				if err := os.Symlink(filepath.Join(base, "deep", "x", "y"), filepath.Join(tree, "link")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(tree, filepath.Join(base, "alias")); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(tree, "file"), "")
				for path, config := range c.repos {
					repo := filepath.Join(base, path)
					if common, ok := strings.CutPrefix(config, "commondir:"); ok {
						// A linked worktree's administrative folder: HEAD and a
						// commondir pointer, relative to this folder, to the
						// repository holding objects and refs.
						mkdir(t, repo)
						writeFile(t, filepath.Join(repo, "HEAD"), "ref: refs/heads/main\n")
						writeFile(t, filepath.Join(repo, "commondir"), common+"\n")
						continue
					}
					cmd := exec.Command(git, "init", "-q", "--bare", repo)
					cmd.Env = testgit.Env()
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("init %s: %s: %v", path, out, err)
					}
					// A bare init writes core.bare = true; the case config can set it again.
					config = "[core]\n\tbare = false\n" + strings.ReplaceAll(config, "$BASE", base)
					f, err := os.OpenFile(filepath.Join(repo, "config"), os.O_APPEND|os.O_WRONLY, 0)
					if err != nil {
						t.Fatal(err)
					}
					_, err = f.WriteString(config)
					if closeErr := f.Close(); err == nil {
						err = closeErr
					}
					if err != nil {
						t.Fatal(err)
					}
				}
				pointer := filepath.Join(tree, ".git")
				writeFile(t, pointer, "gitdir: "+strings.ReplaceAll(c.target, "$BASE", base)+"\n")

				folder := filepath.Join(tree, "app")
				env := append(testgit.Env(), "GIT_CEILING_DIRECTORIES="+base)
				t.Setenv("GIT_CEILING_DIRECTORIES", base)
				switch route {
				case "discovered":
					folder = tree
				case "symlinked-folder":
					folder = filepath.Join(base, "alias", "app")
				case "explicit":
					// GIT_DIR names the gitfile, as a control: this route keeps
					// the target's components.
					env = append(env, "GIT_DIR="+pointer)
					t.Setenv("GIT_DIR", pointer)
				}
				cmd := exec.Command(git, "rev-parse", "--show-cdup")
				cmd.Dir, cmd.Env = folder, env
				out, probeErr := cmd.Output()
				want := ""
				if probeErr == nil {
					cdup := strings.TrimSpace(string(out))
					if filepath.IsAbs(cdup) {
						want = filepath.Clean(cdup)
					} else {
						want = filepath.Join(folder, cdup)
					}
				}
				if route == "discovered-above" {
					expected := ""
					if c.root != "" {
						expected = filepath.Join(base, c.root)
					}
					if want != expected {
						t.Fatalf("Git root = %q (%v); want %q", want, probeErr, expected)
					}
				}
				got, err := GitRoot(folder)
				if err != nil || got != want {
					t.Fatalf("GitRoot = %q, %v; Git = %q (%v)", got, err, want, probeErr)
				}
				resolved, err := Resolve(folder, false, false, Options{MountGitRoot: true, Platform: "linux"})
				source := want
				if source == "" {
					source = folder
				}
				if err != nil || resolved.WorkspaceMount.Source != source {
					t.Fatalf("mount = %+v, %v; want source %q", resolved, err, source)
				}
			})
		}
	}
}
