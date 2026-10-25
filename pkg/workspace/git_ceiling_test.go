// relationships:
//
//	references: [git, ../../reference/cli/src/spec-common/git]
package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

func TestGitRootCeilingsMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	base := t.TempDir()
	repo := mkdir(t, filepath.Join(base, "repo"))
	makeRepo(t, repo)
	app := mkdir(t, filepath.Join(repo, "app"))
	sub := mkdir(t, filepath.Join(app, "src"))
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(app, alias); err != nil {
		t.Fatal(err)
	}
	sep := string(os.PathListSeparator)
	for _, tc := range []struct{ name, folder, ceiling string }{
		{"ancestor", sub, app},
		{"repository", sub, repo},
		{"start directory", repo, repo},
		{"start below repository", app, app},
		{"above repository", sub, base},
		{"list nearest", sub, base + sep + app},
		{"relative ignored", sub, "repo/app"},
		{"missing ignored", sub, filepath.Join(base, "missing")},
		{"symlink resolved", sub, alias},
		{"empty disables symlink resolution", sub, sep + alias},
		{"empty retains literal ancestor", sub, sep + app},
		{"trailing slash", sub, app + string(os.PathSeparator)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GIT_CEILING_DIRECTORIES", tc.ceiling)
			cmd := exec.Command(git, "rev-parse", "--show-cdup")
			cmd.Dir = tc.folder
			cmd.Env = append(testgit.Env(), "GIT_CEILING_DIRECTORIES="+tc.ceiling)
			out, probeErr := cmd.Output()
			want := ""
			if probeErr == nil {
				want = filepath.Clean(filepath.Join(tc.folder, strings.TrimSpace(string(out))))
			}
			got, err := GitRoot(tc.folder)
			if err != nil || got != want {
				t.Fatalf("GitRoot=%q, %v; git=%q (probe error %v)", got, err, want, probeErr)
			}
		})
	}
}

func TestResolveGitCeiling(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	makeRepo(t, repo)
	app := mkdir(t, filepath.Join(repo, "app"))
	sub := mkdir(t, filepath.Join(app, "src"))
	t.Setenv("GIT_CEILING_DIRECTORIES", app)
	got, err := Resolve(sub, false, false, Options{MountGitRoot: true, Platform: "linux"})
	if err != nil || got.WorkspaceMount == nil || got.WorkspaceMount.Source != sub || got.WorkspaceFolder != "/workspaces/src" {
		t.Fatalf("Resolve = %+v, mount %+v, %v", got, got.WorkspaceMount, err)
	}
}
