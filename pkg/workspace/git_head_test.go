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

func TestGitRootHEADMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	for _, tc := range []struct{ name, content, link string }{
		{"junk", "junk\n", ""},
		{"empty", "", ""},
		{"short-hex", strings.Repeat("a", 39), ""},
		{"nonhex", strings.Repeat("a", 39) + "g", ""},
		{"detached", strings.Repeat("a", 40), ""},
		{"detached-upper", strings.Repeat("A", 40) + "\n", ""},
		{"detached-trailing", strings.Repeat("a", 40) + "junk", ""},
		{"sha256", strings.Repeat("b", 64) + "\n", ""},
		{"symbolic", "ref: refs/heads/main\n", ""},
		{"symbolic-no-space", "ref:refs/heads/main", ""},
		{"symbolic-whitespace", "ref:\t\r\n refs/heads/main", ""},
		{"vertical-tab", "ref:\vrefs/heads/main", ""},
		{"form-feed", "ref:\frefs/heads/main", ""},
		{"symbolic-space-name", "ref: refs/heads/a b\n", ""},
		{"symbolic-prefix-only", "ref: refs/", ""},
		{"wrong-prefix", "ref: nope\n", ""},
		{"leading-space", " ref: refs/heads/main\n", ""},
		{"nul-before-ref", "ref: \x00refs/heads/main", ""},
		{"255-byte-prefix", "ref:" + strings.Repeat(" ", 246) + "refs/", ""},
		{"256-byte-prefix", "ref:" + strings.Repeat(" ", 247) + "refs/", ""},
		{"nul-after-hex", strings.Repeat("a", 40) + "\x00junk", ""},
		{"symlink-refs", "", "refs/heads/main"},
		{"symlink-wrong", "", "other"},
	} {
		for _, route := range []string{"discovered", "explicit", "gitfile"} {
			t.Run(tc.name+"/"+route, func(t *testing.T) {
				base := t.TempDir()
				repo := mkdir(t, filepath.Join(base, "repo"))
				sub := mkdir(t, filepath.Join(repo, "app"))
				cmd := exec.Command(git, "init", "-q", repo)
				cmd.Env = testgit.Env()
				if out, e := cmd.CombinedOutput(); e != nil {
					t.Fatalf("init %v: %s", e, out)
				}
				admin := filepath.Join(repo, ".git")
				head := filepath.Join(admin, "HEAD")
				if err := os.WriteFile(filepath.Join(admin, "other"), []byte("ref: refs/heads/main\n"), 0600); err != nil {
					t.Fatal(err)
				}
				if tc.link != "" {
					if err := os.Remove(head); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(tc.link, head); err != nil {
						t.Fatal(err)
					}
				} else if err := os.WriteFile(head, []byte(tc.content), 0600); err != nil {
					t.Fatal(err)
				}
				env := append(testgit.Env(), "GIT_CEILING_DIRECTORIES="+base)
				t.Setenv("GIT_CEILING_DIRECTORIES", base)
				if route == "explicit" {
					env = append(env, "GIT_DIR="+admin)
					t.Setenv("GIT_DIR", admin)
				}
				if route == "gitfile" {
					moved := filepath.Join(base, "admin")
					if err := os.Rename(admin, moved); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(admin, []byte("gitdir: "+moved+"\n"), 0600); err != nil {
						t.Fatal(err)
					}
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
