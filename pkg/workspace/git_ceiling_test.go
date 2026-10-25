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
	deepAlias := filepath.Join(base, "deep-alias")
	if err := os.Symlink(sub, deepAlias); err != nil {
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
		{"symlink parent", sub, deepAlias + "/.."},
		{"missing parent", sub, app + "/missing/.."},
		{"literal dot", sub, sep + app + "/."},
		{"literal parent", sub, sep + app + "/.."},
		{"literal double slash", sub, sep + app + "//"},
		{"literal trailing slash", sub, sep + app + "/"},
		{"root ceiling", sub, string(os.PathSeparator)},
		{"literal root ceiling", sub, sep + string(os.PathSeparator)},
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

func TestResolveGitCeilingPathComponents(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	makeRepo(t, repo)
	app := mkdir(t, filepath.Join(repo, "app"))
	sub := mkdir(t, filepath.Join(app, "src"))
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(sub, alias); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, ceiling, source string }{
		{"symlink parent", alias + "/..", sub},
		{"missing parent", app + "/missing/..", repo},
		{"literal dot", string(os.PathListSeparator) + app + "/.", repo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GIT_CEILING_DIRECTORIES", tc.ceiling)
			got, err := Resolve(sub, false, false, Options{MountGitRoot: true, Platform: "linux"})
			if err != nil || got.WorkspaceMount == nil || got.WorkspaceMount.Source != tc.source {
				t.Fatalf("Resolve=%+v mount=%+v err=%v; source want %q", got, got.WorkspaceMount, err, tc.source)
			}
		})
	}
}
