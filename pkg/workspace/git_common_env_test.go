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

func TestGitRootCommonDirectoryEnvironmentMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	for _, route := range []string{"discovered", "explicit", "gitfile", "explicit-gitfile"} {
		for _, name := range []string{"unset", "admin", "external", "relative", "dot", "empty", "missing", "missing-objects", "missing-refs", "broken-commondir", "fatal-commondir", "objects-env", "objects-env-missing", "symlink-parent", "missing-intermediate"} {
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
				remove := func(p string) {
					t.Helper()
					if e := os.RemoveAll(p); e != nil {
						t.Fatal(e)
					}
				}
				// The environment replaces the administrative folder's common
				// signatures: valid values drop the local ones so only the
				// environment can satisfy Git, and invalid values keep them.
				common := mkdir(t, filepath.Join(base, "common"))
				mkdir(t, filepath.Join(common, "objects"))
				mkdir(t, filepath.Join(common, "refs"))
				value := common
				switch name {
				case "external", "relative", "objects-env", "symlink-parent":
					remove(filepath.Join(admin, "objects"))
					remove(filepath.Join(admin, "refs"))
				}
				var objects string
				switch name {
				case "admin":
					value = admin
				case "relative":
					value = "../../../common"
				case "dot":
					value = "."
				case "empty":
					value = ""
				case "missing":
					value = filepath.Join(base, "absent")
				case "missing-objects":
					remove(filepath.Join(common, "objects"))
				case "missing-refs":
					remove(filepath.Join(common, "refs"))
				case "broken-commondir":
					// Git reads no commondir file when the environment names one.
					writeFile(t, filepath.Join(admin, "commondir"), filepath.Join(base, "absent")+"\n")
					value = admin
				case "fatal-commondir":
					// Without the environment, Git fails resolving this pointer.
					writeFile(t, filepath.Join(admin, "commondir"), "absent/child\n")
					value = admin
				case "objects-env":
					remove(filepath.Join(common, "objects"))
					objects = mkdir(t, filepath.Join(base, "storage"))
				case "objects-env-missing":
					objects = filepath.Join(base, "absent")
				case "symlink-parent":
					child := mkdir(t, filepath.Join(base, "physical", "child"))
					if e := os.Symlink(child, filepath.Join(sub, "link")); e != nil {
						t.Fatal(e)
					}
					if e := os.Rename(common, filepath.Join(base, "physical", "target")); e != nil {
						t.Fatal(e)
					}
					value = "link/../target"
				case "missing-intermediate":
					value = "absent/../../../../common"
				}
				env := append(testgit.Env(), "GIT_CEILING_DIRECTORIES="+base)
				t.Setenv("GIT_CEILING_DIRECTORIES", base)
				if name != "unset" {
					env = append(env, "GIT_COMMON_DIR="+value)
					t.Setenv("GIT_COMMON_DIR", value)
				}
				if objects != "" {
					env = append(env, "GIT_OBJECT_DIRECTORY="+objects)
					t.Setenv("GIT_OBJECT_DIRECTORY", objects)
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
