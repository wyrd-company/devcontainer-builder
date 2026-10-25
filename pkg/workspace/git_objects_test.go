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

func TestGitRootObjectDirectoryMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	for _, route := range []string{"discovered", "explicit", "gitfile", "explicit-gitfile"} {
		for _, name := range []string{"unset", "absolute", "relative", "empty", "missing", "missing-local", "missing-refs", "executable-file", "nonexecutable-file", "symlink-parent", "missing-intermediate", "common"} {
			t.Run(name+"/"+route, func(t *testing.T) {
				base := t.TempDir()
				tree := mkdir(t, filepath.Join(base, "tree"))
				sub := mkdir(t, filepath.Join(tree, "app", "deep"))
				cmd := exec.Command(git, "init", "-q", tree)
				cmd.Env = testgit.Env()
				if out, e := cmd.CombinedOutput(); e != nil {
					t.Fatalf("init: %v %s", e, out)
				}
				admin := filepath.Join(tree, ".git")
				if strings.Contains(route, "gitfile") {
					moved := filepath.Join(base, "admin")
					if e := os.Rename(admin, moved); e != nil {
						t.Fatal(e)
					}
					writeFile(t, admin, "gitdir: "+moved+"\n")
					admin = moved
				}
				objects := mkdir(t, filepath.Join(base, "storage"))
				value := objects
				remove := func(p string) {
					t.Helper()
					if e := os.RemoveAll(p); e != nil {
						t.Fatal(e)
					}
				}
				switch name {
				case "relative":
					value = "../../../storage"
				case "empty":
					value = ""
				case "missing":
					value = filepath.Join(base, "absent")
				case "missing-local":
					remove(filepath.Join(admin, "objects"))
				case "missing-refs":
					remove(filepath.Join(admin, "refs"))
				case "executable-file", "nonexecutable-file":
					remove(objects)
					writeFile(t, objects, "x")
					mode := os.FileMode(0755)
					if name == "nonexecutable-file" {
						mode = 0644
					}
					if e := os.Chmod(objects, mode); e != nil {
						t.Fatal(e)
					}
				case "symlink-parent":
					child := mkdir(t, filepath.Join(objects, "child"))
					if e := os.Symlink(child, filepath.Join(sub, "link")); e != nil {
						t.Fatal(e)
					}
					mkdir(t, filepath.Join(objects, "target"))
					value = "link/../target"
				case "missing-intermediate":
					value = "absent/../../storage"
				case "common":
					common := mkdir(t, filepath.Join(base, "common"))
					mkdir(t, filepath.Join(common, "refs"))
					writeFile(t, filepath.Join(admin, "commondir"), common+"\n")
				}
				env := append(testgit.Env(), "GIT_CEILING_DIRECTORIES="+base)
				t.Setenv("GIT_CEILING_DIRECTORIES", base)
				if name != "unset" {
					env = append(env, "GIT_OBJECT_DIRECTORY="+value)
					t.Setenv("GIT_OBJECT_DIRECTORY", value)
				}
				if strings.HasPrefix(route, "explicit") {
					dir := admin
					if route == "explicit-gitfile" {
						dir = filepath.Join(tree, ".git")
					}
					env = append(env, "GIT_DIR="+dir)
					t.Setenv("GIT_DIR", dir)
				}
				probe := exec.Command(git, "rev-parse", "--show-cdup")
				probe.Dir = sub
				probe.Env = env
				out, e := probe.Output()
				want := ""
				if e == nil {
					want = filepath.Clean(filepath.Join(sub, strings.TrimSpace(string(out))))
				}
				got, e := GitRoot(sub)
				if e != nil || got != want {
					t.Fatalf("GitRoot = %q, %v; git = %q", got, e, want)
				}
				resolved, e := Resolve(sub, false, false, Options{MountGitRoot: true, Platform: "linux"})
				if e != nil {
					t.Fatal(e)
				}
				mount := want
				if mount == "" {
					mount = sub
				}
				if resolved.WorkspaceMount.Source != mount {
					t.Fatalf("mount = %q; want %q", resolved.WorkspaceMount.Source, mount)
				}
			})
		}
	}
}
