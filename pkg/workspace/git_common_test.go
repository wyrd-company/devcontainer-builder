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

func TestGitRootCommonDirectoryMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	for _, name := range []string{
		"absent", "relative", "absolute", "crlf", "newline", "empty", "missing", "missing-intermediate",
		"missing-objects", "missing-refs", "no-local-signatures", "whitespace", "interior-newline", "nul",
		"nul-first", "symlink-parent", "regular-parent", "pointer-symlink", "pointer-dangling", "pointer-directory",
		"executable-signatures", "nonexecutable-objects", "nonexecutable-refs",
	} {
		for _, route := range []string{"discovered", "explicit", "gitfile", "explicit-gitfile"} {
			t.Run(name+"/"+route, func(t *testing.T) {
				base := t.TempDir()
				parent := mkdir(t, filepath.Join(base, "parent"))
				tree := mkdir(t, filepath.Join(parent, "tree"))
				sub := mkdir(t, filepath.Join(tree, "app"))
				for _, repo := range []string{parent, tree} {
					cmd := exec.Command(git, "init", "-q", repo)
					cmd.Env = testgit.Env()
					if out, e := cmd.CombinedOutput(); e != nil {
						t.Fatalf("init: %v %s", e, out)
					}
				}
				admin := filepath.Join(tree, ".git")
				if route == "gitfile" || route == "explicit-gitfile" {
					moved := filepath.Join(base, "admin")
					if e := os.Rename(admin, moved); e != nil {
						t.Fatal(e)
					}
					writeFile(t, admin, "gitdir: "+moved+"\n")
					admin = moved
				}
				common := mkdir(t, filepath.Join(base, "common"))
				mkdir(t, filepath.Join(common, "objects"))
				mkdir(t, filepath.Join(common, "refs"))
				rel, e := filepath.Rel(admin, common)
				if e != nil {
					t.Fatal(e)
				}
				content := rel + "\n"
				link := func(target, path string) {
					t.Helper()
					if e := os.Symlink(target, path); e != nil {
						t.Fatal(e)
					}
				}
				remove := func(path string) {
					t.Helper()
					if e := os.RemoveAll(path); e != nil {
						t.Fatal(e)
					}
				}
				switch name {
				case "absolute":
					content = common
				case "crlf":
					content = rel + "\r\n\r\n"
				case "newline":
					content = "\n"
				case "empty":
					content = ""
				case "missing":
					content = "missing\n"
				case "missing-intermediate":
					content = "missing/../" + rel
				case "missing-objects":
					remove(filepath.Join(common, "objects"))
				case "missing-refs":
					remove(filepath.Join(common, "refs"))
				case "no-local-signatures":
					remove(filepath.Join(admin, "objects"))
					remove(filepath.Join(admin, "refs"))
				case "whitespace", "interior-newline":
					suffix := " "
					if name == "interior-newline" {
						suffix = "\nline"
					}
					if e := os.Rename(common, common+suffix); e != nil {
						t.Fatal(e)
					}
					content = common + suffix + "\n"
				case "nul":
					content = common + "\x00ignored\n"
				case "nul-first":
					content = "\x00ignored"
				case "symlink-parent":
					remove(filepath.Join(admin, "objects"))
					remove(filepath.Join(admin, "refs"))
					mkdir(t, filepath.Join(common, "child"))
					link(filepath.Join(common, "child"), filepath.Join(admin, "link"))
					content = "link/.."
				case "regular-parent":
					writeFile(t, filepath.Join(common, "file"), "x")
					content = common + "/file/.."
				case "executable-signatures", "nonexecutable-objects", "nonexecutable-refs":
					for _, signature := range []string{"objects", "refs"} {
						p := filepath.Join(common, signature)
						remove(p)
						writeFile(t, p, "x")
						mode := os.FileMode(0755)
						if name == "nonexecutable-"+signature {
							mode = 0644
						}
						if e := os.Chmod(p, mode); e != nil {
							t.Fatal(e)
						}
					}
				}
				pointer := filepath.Join(admin, "commondir")
				switch name {
				case "absent":
				case "pointer-symlink":
					writeFile(t, filepath.Join(base, "pointer"), content)
					link(filepath.Join(base, "pointer"), pointer)
				case "pointer-dangling":
					link(filepath.Join(base, "missing"), pointer)
				case "pointer-directory":
					mkdir(t, pointer)
				default:
					writeFile(t, pointer, content)
				}
				env := append(testgit.Env(), "GIT_CEILING_DIRECTORIES="+base)
				t.Setenv("GIT_CEILING_DIRECTORIES", base)
				if route == "explicit" || route == "explicit-gitfile" {
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
				got, err := GitRoot(sub)
				if err != nil || got != want {
					t.Fatalf("GitRoot = %q, %v; git = %q (%v)", got, err, want, e)
				}
				resolved, err := Resolve(sub, false, false, Options{MountGitRoot: true, Platform: "linux"})
				if err != nil {
					t.Fatal(err)
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
