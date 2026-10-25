// relationships:
//   references: [git, git_dir_selection_test, workspace]

package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

func TestGitfileFormatMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	base := t.TempDir()
	repo := mkdir(t, filepath.Join(base, "repo"))
	cmd := exec.Command(git, "init", "-q", repo)
	cmd.Env = testgit.Env()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %s: %v", out, err)
	}
	gitdir := filepath.Join(repo, ".git")
	// A second repository with whitespace and a newline in its directory name
	// proves those bytes remain part of the payload, rather than separators.
	unusual := mkdir(t, filepath.Join(base, " admin\t\nfolder "))
	cmd = exec.Command(git, "init", "-q", unusual)
	cmd.Env = testgit.Env()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init unusual: %s: %v", out, err)
	}
	if err := os.Rename(filepath.Join(unusual, ".git"), unusual+" admin "); err != nil {
		t.Fatal(err)
	}
	unusual += " admin "
	for _, tc := range []struct {
		name, content string
		valid         bool
	}{
		{"literal", "gitdir: " + gitdir + "\n", true},
		{"relative", "gitdir: ../../.git\n", true},
		{"no-final-newline", "gitdir: " + gitdir, true},
		{"crlf", "gitdir: " + gitdir + "\r\n", true},
		{"multiple-final-newlines", "gitdir: " + gitdir + "\n\r\n", true},
		{"interior-newline-path", "gitdir: " + unusual + "\n", true},
		{"relative-leading-whitespace-path", "gitdir: ../../../ admin\t\nfolder  admin \n", true},
		{"nul-terminated-path", "gitdir: " + gitdir + "\x00ignored\n", true},
		{"size-at-limit", "gitdir: " + gitdir + strings.Repeat("\n", (1<<20)-8-len(gitdir)), true},
		{"size-over-limit", "gitdir: " + gitdir + strings.Repeat("\n", (1<<20)+1-8-len(gitdir)), false},
		{"leading-junk", "junk\ngitdir: " + gitdir + "\n", false},
		{"leading-space", " gitdir: " + gitdir + "\n", false},
		{"missing-space", "gitdir:" + gitdir + "\n", false},
		{"tab-separator", "gitdir:\t" + gitdir + "\n", false},
		{"newline-separator", "gitdir:\n" + gitdir + "\n", false},
		{"extra-space", "gitdir:  " + gitdir + "\n", false},
		{"extra-line", "gitdir: " + gitdir + "\njunk\n", false},
		{"trailing-space", "gitdir: " + gitdir + " \n", false},
		{"empty-path", "gitdir: \r\n", false},
		{"empty-file", "", false},
	} {
		for _, explicit := range []bool{false, true} {
			route := "discovered"
			if explicit {
				route = "explicit"
			}
			t.Run(tc.name+"/"+route, func(t *testing.T) {
				nested := mkdir(t, filepath.Join(repo, "nested", "tree"))
				sub := mkdir(t, filepath.Join(nested, "sub"))
				pointer := filepath.Join(nested, ".git")
				if err := os.WriteFile(pointer, []byte(tc.content), 0600); err != nil {
					t.Fatal(err)
				}
				// Without the literal .git file, discovery would find the parent repo.
				env := append(testgit.Env(), "GIT_CEILING_DIRECTORIES="+base)
				t.Setenv("GIT_CEILING_DIRECTORIES", base)
				if explicit {
					t.Setenv("GIT_DIR", pointer)
					env = append(env, "GIT_DIR="+pointer)
				}
				cmd := exec.Command(git, "rev-parse", "--show-cdup")
				cmd.Dir, cmd.Env = sub, env
				out, probeErr := cmd.Output()
				if (probeErr == nil) != tc.valid {
					t.Fatalf("Git validity = %v, output %q; valid = %v", probeErr, out, tc.valid)
				}
				want := ""
				if probeErr == nil {
					cdup := strings.Trim(string(out), "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff")
					if filepath.IsAbs(cdup) {
						want = filepath.Clean(cdup)
					} else {
						want = filepath.Join(sub, cdup)
					}
				}
				got, err := GitRoot(sub)
				if err != nil || got != want {
					t.Fatalf("GitRoot = %q, %v; Git = %q (%v)", got, err, want, probeErr)
				}
				resolved, err := Resolve(sub, false, false, Options{MountGitRoot: true, Platform: "linux"})
				source := want
				if source == "" {
					source = sub
				}
				if err != nil || resolved.WorkspaceMount.Source != source {
					t.Fatalf("mount = %+v, %v; want source %q", resolved, err, source)
				}
			})
		}
	}
}
