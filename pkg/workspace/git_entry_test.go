// relationships:
//   references: [git, workspace, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

// When stat fails on a .git entry, Git continues to parent folders only if the
// entry is missing (ENOENT or ENOTDIR). Since Git 2.54 another stat failure, a
// gitfile it cannot open and a special file each make its probe fail, so
// upstream finds no Git root rather than a parent repository. A .git folder
// that stat reads but that is not a repository, such as one Git cannot read,
// does not stop discovery. The tree folder holds a repository; the case writes
// the .git entry in the caller folder or in a parent below tree.
func TestGitEntryAccessMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	// root is Git's root from the entry's folder, relative to the case folder:
	// "tree" when Git continues past the entry, "entry" when the entry selects
	// its own folder and "" when Git's probe fails. permission marks a case
	// that root's capabilities would bypass.
	cases := []struct {
		name       string
		entry      func(t *testing.T, base, dotGit string)
		root       string
		permission bool
	}{
		{"symlink-loop", func(t *testing.T, base, dotGit string) { symlink(t, ".git", dotGit) }, "", false},
		{"long-name", func(t *testing.T, base, dotGit string) { symlink(t, strings.Repeat("a", 300), dotGit) }, "", false},
		{"search-denied", func(t *testing.T, base, dotGit string) {
			locked := mkdir(t, filepath.Join(base, "locked", "x"))
			symlink(t, filepath.Join(locked, ".git"), dotGit)
			chmod(t, filepath.Dir(locked), 0)
		}, "", true},
		{"unreadable-gitfile", func(t *testing.T, base, dotGit string) {
			writeFile(t, dotGit, "gitdir: "+filepath.Join(base, "tree", ".git")+"\n")
			chmod(t, dotGit, 0)
		}, "", true},
		{"fifo", func(t *testing.T, base, dotGit string) {
			if err := unix.Mkfifo(dotGit, 0o600); err != nil {
				t.Fatal(err)
			}
		}, "", false},
		// Controls: Git reads a missing entry as absent, and an inaccessible
		// .git folder is not a repository; it continues to tree in each case.
		{"dangling", func(t *testing.T, base, dotGit string) { symlink(t, filepath.Join(base, "missing"), dotGit) }, "tree", false},
		{"not-a-directory", func(t *testing.T, base, dotGit string) {
			writeFile(t, filepath.Join(base, "file"), "")
			symlink(t, filepath.Join(base, "file", "x"), dotGit)
		}, "tree", false},
		{"unreadable-folder", func(t *testing.T, base, dotGit string) {
			mkdir(t, dotGit)
			chmod(t, dotGit, 0)
		}, "tree", true},
		// A readable gitfile to the same repository selects its own folder.
		{"readable-gitfile", func(t *testing.T, base, dotGit string) {
			writeFile(t, dotGit, "gitdir: "+filepath.Join(base, "tree", ".git")+"\n")
		}, "entry", false},
	}
	for _, route := range []string{"caller", "parent", "symlinked-folder"} {
		for _, c := range cases {
			t.Run(c.name+"/"+route, func(t *testing.T) {
				if c.permission && os.Geteuid() == 0 {
					t.Skip("root bypasses permissions")
				}
				base, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				tree := filepath.Join(base, "tree")
				cmd := exec.Command(git, "init", "-q", tree)
				cmd.Env = testgit.Env()
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("init: %s: %v", out, err)
				}
				entryFolder := mkdir(t, filepath.Join(tree, "mid"))
				folder := entryFolder
				switch route {
				case "parent":
					folder = mkdir(t, filepath.Join(entryFolder, "app"))
				case "symlinked-folder":
					folder = filepath.Join(base, "alias", "app")
					mkdir(t, filepath.Join(entryFolder, "app"))
					symlink(t, entryFolder, filepath.Join(base, "alias"))
				}
				c.entry(t, base, filepath.Join(entryFolder, ".git"))

				t.Setenv("GIT_CEILING_DIRECTORIES", base)
				cmd = exec.Command(git, "rev-parse", "--show-cdup")
				cmd.Dir, cmd.Env = folder, append(testgit.Env(), "GIT_CEILING_DIRECTORIES="+base)
				out, probeErr := cmd.Output()
				want := ""
				if probeErr == nil {
					want = filepath.Join(folder, strings.TrimSpace(string(out)))
				}
				if route == "caller" {
					expected := map[string]string{"": "", "tree": tree, "entry": entryFolder}[c.root]
					if want != expected {
						t.Fatalf("Git root = %q (%v); want %q", want, probeErr, expected)
					}
				}
				got, err := GitRoot(folder)
				if err != nil || got != want {
					t.Fatalf("GitRoot = %q, %v; Git = %q (%v)", got, err, want, probeErr)
				}
				// The upstream CLI mounts the Git root, or the workspace
				// folder when Git's probe fails.
				source := want
				if source == "" {
					source = folder
				}
				rel, err := filepath.Rel(source, folder)
				if err != nil {
					t.Fatal(err)
				}
				target := path.Join("/workspaces", filepath.Base(source))
				resolved, err := Resolve(folder, false, false, Options{MountGitRoot: true, Platform: "linux"})
				if err != nil || resolved.WorkspaceMount == nil || resolved.WorkspaceMount.Source != source ||
					resolved.WorkspaceMount.Target != target || resolved.WorkspaceFolder != path.Join(target, filepath.ToSlash(rel)) {
					t.Fatalf("Resolve = %+v %+v, %v; want source %q target %q", resolved, resolved.WorkspaceMount, err, source, target)
				}
			})
		}
	}
}

func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// chmod changes a fixture's mode and restores it for TempDir's removal.
func chmod(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o755) })
}
