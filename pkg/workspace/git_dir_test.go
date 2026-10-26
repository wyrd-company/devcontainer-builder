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

func TestInvalidGitDirMatchesGit(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	base := t.TempDir()
	repo := mkdir(t, filepath.Join(base, "repo"))
	sub := mkdir(t, filepath.Join(repo, "app", "src"))
	cmd := exec.Command(gitBin, "init", "-q", repo)
	cmd.Env = testgit.Env()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %s: %v", out, err)
	}
	broken := filepath.Join(base, "broken")
	valid := filepath.Join(base, "valid")
	linked := filepath.Join(base, "linked")
	if err := os.Symlink(filepath.Join(repo, ".git"), filepath.Join(base, "admin")); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{broken: "gitdir: missing\n", valid: "gitdir: repo/.git\n", linked: "gitdir: admin/../.git\n"} {
		if err := os.WriteFile(name, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	pointDir := mkdir(t, filepath.Join(base, "pointers"))
	point := filepath.Join(pointDir, "file")
	if err := os.WriteFile(point, []byte("gitdir: .git\n"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(repo, "pointer")
	if err := os.Symlink(point, alias); err != nil {
		t.Fatal(err)
	}
	badAlias := filepath.Join(sub, "pointer")
	if err := os.Symlink(valid, badAlias); err != nil {
		t.Fatal(err)
	}
	// The lexical parent of link is app; its physical parent is base.
	if err := os.Symlink(filepath.Join(repo, ".git"), filepath.Join(sub, "link")); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, folder, value string
		workTree            bool
		valid               bool
	}{
		{"empty", sub, "", false, false},
		{"empty-in-admin-folder", filepath.Join(repo, ".git"), "", false, false},
		{"missing-absolute", sub, filepath.Join(base, "missing"), false, false},
		{"missing-relative", sub, "missing", false, false},
		{"working-directory-is-not-git-dir", sub, repo, false, false},
		{"regular-file", sub, filepath.Join(repo, ".git", "HEAD"), false, false},
		{"file-before-parent", sub, filepath.Join(repo, ".git", "HEAD") + "/..", false, false},
		{"symlinked-gitfile-invalid-relative-target", sub, badAlias, false, false},
		{"broken-gitfile", sub, broken, false, false},
		{"cwd-relative-not-process-relative", sub, ".git", false, false},
		{"invalid-dir-before-work-tree", sub, "missing", true, false},
		{"valid-absolute", repo, filepath.Join(repo, ".git"), false, true},
		{"valid-relative", repo, ".git", false, true},
		{"valid-gitfile", repo, valid, false, true},
		{"valid-symlinked-gitfile", repo, alias, false, true},
		{"valid-gitfile-physical-target", repo, linked, false, true},
		{"valid-relative-with-work-tree", sub, "../../.git", true, true},
		{"valid-symlink-before-parent", sub, "link/../.git", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("GIT_DIR", tc.value)
			t.Setenv("GIT_CEILING_DIRECTORIES", base)
			env := append(testgit.Env(), "GIT_DIR="+tc.value, "GIT_CEILING_DIRECTORIES="+base)
			if tc.workTree {
				t.Setenv("GIT_WORK_TREE", repo)
				env = append(env, "GIT_WORK_TREE="+repo)
			}
			cmd := exec.Command(gitBin, "rev-parse", "--show-cdup")
			cmd.Dir = tc.folder
			cmd.Env = env
			out, probeErr := cmd.Output()
			if (probeErr == nil) != tc.valid {
				t.Fatalf("unexpected Git validity: %v, %q", probeErr, out)
			}
			want := ""
			if probeErr == nil {
				want = filepath.Join(tc.folder, strings.TrimSpace(string(out)))
			}
			got, err := GitRoot(tc.folder)
			if err != nil || got != want {
				t.Fatalf("GitRoot = %q, %v; Git says %q", got, err, want)
			}
			if !tc.valid {
				resolved, err := Resolve(tc.folder, false, false, Options{MountGitRoot: true, Platform: "linux"})
				if err != nil || resolved.WorkspaceMount.Source != tc.folder || resolved.WorkspaceFolder != "/workspaces/"+filepath.Base(tc.folder) {
					t.Fatalf("invalid GIT_DIR mount = %+v, %v", resolved, err)
				}
			}
		})
	}
}
