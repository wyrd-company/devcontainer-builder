package workspace

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

// TestGitRootMatchesGit compares GitRoot with upstream's own probe,
// `git rev-parse --show-cdup`, over real repositories and worktrees.
func TestGitRootMatchesGit(t *testing.T) {
	gitBin, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	base := t.TempDir()
	git := func(dir string, args ...string) (string, error) {
		cmd := exec.Command(gitBin, args...)
		cmd.Dir = dir
		cmd.Env = append(testgit.Env(), "GIT_CEILING_DIRECTORIES="+base)
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
	must := func(dir string, args ...string) {
		t.Helper()
		if _, err := git(dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	repo := mkdir(t, filepath.Join(base, "repo"))
	must(repo, "init", "-q", "-b", "main")
	must(repo, "commit", "-q", "--allow-empty", "-m", "initial")
	nested := mkdir(t, filepath.Join(repo, "vendor", "nested"))
	must(nested, "init", "-q")
	must(repo, "worktree", "add", "-q", "--relative-paths", filepath.Join(base, "trees", "topic"), "-b", "topic")
	must(repo, "worktree", "add", "-q", filepath.Join(base, "trees", "absolute"), "-b", "absolute")

	for _, folder := range []string{
		repo,
		mkdir(t, filepath.Join(repo, "app", "src")),
		nested,
		mkdir(t, filepath.Join(nested, "deep")),
		filepath.Join(base, "trees", "topic"),
		mkdir(t, filepath.Join(base, "trees", "topic", "sub")),
		filepath.Join(base, "trees", "absolute"),
		mkdir(t, filepath.Join(base, "plain")),
	} {
		want := ""
		if cdup, err := git(folder, "rev-parse", "--show-cdup"); err == nil {
			want = filepath.Clean(filepath.Join(folder, cdup))
		}
		got, err := GitRoot(folder)
		if err != nil || got != want {
			t.Errorf("GitRoot(%s) = %q, %v; git says %q", folder, got, err, want)
		}
	}

	// The common-folder mount is the folder Git itself uses for the worktree.
	topic := filepath.Join(base, "trees", "topic")
	common, err := git(topic, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Resolve(topic, false, false, Options{MountGitRoot: true, MountGitWorktreeCommonDir: true, Platform: "linux"})
	if err != nil || got.AdditionalMount == nil || got.AdditionalMount.Source != common || got.AdditionalMount.Target != "/workspaces/repo/.git" || got.WorkspaceFolder != "/workspaces/trees/topic" {
		t.Fatalf("worktree Resolve = %+v %+v %v; git common dir %s", got, got.AdditionalMount, err, common)
	}
}
