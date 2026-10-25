// relationships:
//   references: [git, git_differential_test, workspace]

package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

func TestGitWorkTreeMatchesGit(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	base := t.TempDir()
	repo := mkdir(t, filepath.Join(base, "repo"))
	sub := mkdir(t, filepath.Join(repo, "app", "src"))
	other := mkdir(t, filepath.Join(base, "other"))
	plain := mkdir(t, filepath.Join(base, "plain"))
	for _, suffix := range []string{" ", "\t", "\n", "\ufeff", "\u0085", "\u2003"} {
		mkdir(t, other+suffix)
	}
	cmd := exec.Command(gitBin, "init", "-q", repo)
	cmd.Env = testgit.Env()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %s: %v", out, err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(repo, alias); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(sub, "link")
	if err := os.Symlink(other, link); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, folder, value string }{
		{"absolute-ancestor", sub, repo},
		{"relative-parent", sub, ".."},
		{"relative-current", sub, "."},
		{"relative-outside", sub, "../../.."},
		{"absolute-outside", sub, other},
		{"empty", sub, ""},
		{"outside-space", sub, other + " "},
		{"outside-tab", sub, other + "\t"},
		{"outside-newline", sub, other + "\n"},
		{"outside-bom", sub, other + "\ufeff"},
		{"outside-nel", sub, other + "\u0085"},
		{"outside-zs", sub, other + "\u2003"},
		{"missing-final", sub, "missing"},
		{"missing-intermediate", sub, "missing/child"},
		{"missing-before-parent", sub, "missing/.."},
		{"file", sub, filepath.Join(repo, ".git", "HEAD")},
		{"file-before-parent", sub, filepath.Join(repo, ".git", "HEAD") + string(os.PathSeparator) + ".."},
		{"symlink", sub, "link"},
		{"symlink-before-parent", sub, "link/.."},
		{"symlink-ancestor", sub, alias},
		{"lexical-start", filepath.Join(alias, "app", "src"), repo},
		{"no-repository", plain, other},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GIT_WORK_TREE", tc.value)
			t.Setenv("GIT_CEILING_DIRECTORIES", base)
			cmd := exec.Command(gitBin, "rev-parse", "--show-cdup")
			cmd.Dir = tc.folder
			cmd.Env = append(testgit.Env(), "GIT_WORK_TREE="+tc.value, "GIT_CEILING_DIRECTORIES="+base)
			out, probeErr := cmd.Output()
			want := ""
			if probeErr == nil {
				// JavaScript String.trim(), as used by the upstream CLI.
				// The explicit set keeps this oracle independent of the Unicode
				// category predicate in the native implementation.
				cdup := strings.Trim(string(out), "\t\n\v\f\r \u00a0\u1680\u2000\u2001\u2002\u2003\u2004\u2005\u2006\u2007\u2008\u2009\u200a\u2028\u2029\u202f\u205f\u3000\ufeff")
				if filepath.IsAbs(cdup) {
					want = filepath.Clean(cdup)
				} else {
					want = filepath.Join(tc.folder, cdup)
				}
			}
			got, err := GitRoot(tc.folder)
			if err != nil || got != want {
				t.Fatalf("GitRoot = %q, %v; git says %q (probe %v)", got, err, want, probeErr)
			}
		})
	}
	t.Run("ceiling-still-stops-discovery", func(t *testing.T) {
		t.Setenv("GIT_WORK_TREE", repo)
		t.Setenv("GIT_CEILING_DIRECTORIES", filepath.Dir(sub))
		got, err := GitRoot(sub)
		if got != "" || err != nil {
			t.Fatalf("GitRoot = %q, %v", got, err)
		}
	})
	t.Run("resolve-selected-root", func(t *testing.T) {
		t.Setenv("GIT_WORK_TREE", "..")
		got, err := Resolve(sub, false, false, Options{MountGitRoot: true, Platform: "linux"})
		if err != nil || got.WorkspaceMount.Source != filepath.Dir(sub) || got.WorkspaceMount.Target != "/workspaces/app" || got.WorkspaceFolder != "/workspaces/app/src" {
			t.Fatalf("Resolve = %+v, %v", got, err)
		}
	})
}
