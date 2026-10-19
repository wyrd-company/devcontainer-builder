package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

// makeRepo writes the minimum Git's discovery accepts as a repository.
func makeRepo(t *testing.T, dir string) {
	t.Helper()
	for _, sub := range []string{"objects", "refs"} {
		if err := os.MkdirAll(filepath.Join(dir, ".git", sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(dir, ".git", "HEAD"), "ref: refs/heads/main\n")
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func mkdir(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestGitRoot(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	makeRepo(t, repo)
	sub := mkdir(t, filepath.Join(repo, "app", "src"))
	// A .git folder that is not a repository is skipped, as Git skips it.
	mkdir(t, filepath.Join(repo, "app", ".git"))
	plain := mkdir(t, filepath.Join(base, "plain"))
	// A linked worktree's .git file points at an administrative folder.
	admin := filepath.Join(repo, ".git", "worktrees", "topic")
	writeFile(t, filepath.Join(admin, "HEAD"), "ref: refs/heads/topic\n")
	writeFile(t, filepath.Join(admin, "commondir"), "../..\n")
	worktree := filepath.Join(base, "trees", "topic")
	writeFile(t, filepath.Join(worktree, ".git"), "gitdir: ../../repo/.git/worktrees/topic\r\n")
	// A gitfile pointing nowhere ends discovery, even inside a repository.
	broken := filepath.Join(repo, "broken")
	writeFile(t, filepath.Join(broken, ".git"), "gitdir: missing\n")

	for _, tc := range []struct{ name, folder, want string }{
		{"root", repo, repo},
		{"subfolder", sub, repo},
		{"outside", plain, ""},
		{"worktree", filepath.Join(worktree), worktree},
		{"broken gitfile", broken, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := GitRoot(tc.folder)
			if err != nil || got != tc.want {
				t.Fatalf("GitRoot(%s) = %q, %v; want %q", tc.folder, got, err, tc.want)
			}
		})
	}
}

func TestResolve(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "repo")
	makeRepo(t, repo)
	sub := mkdir(t, filepath.Join(repo, "app"))
	plain := mkdir(t, filepath.Join(base, "plain"))
	linux := Options{MountGitRoot: true, Consistency: ConsistencyCached, Platform: "linux"}

	for _, tc := range []struct {
		name               string
		local              string
		folder, mount      bool
		opts               Options
		wantFolder         string
		wantMount          *Mount
		wantAdditionalNone bool
	}{
		{"git root", sub, false, false, linux, "/workspaces/repo/app", &Mount{Source: repo, Target: "/workspaces/repo"}, true},
		{"workspace only", sub, false, false, Options{Platform: "linux"}, "/workspaces/app", &Mount{Source: sub, Target: "/workspaces/app"}, true},
		{"outside a repository", plain, false, false, linux, "/workspaces/plain", &Mount{Source: plain, Target: "/workspaces/plain"}, true},
		{"desktop consistency", sub, false, false, Options{MountGitRoot: true, Consistency: ConsistencyDelegated, Platform: "darwin"}, "/workspaces/repo/app", &Mount{Source: repo, Target: "/workspaces/repo", Consistency: "delegated"}, true},
		{"desktop default", sub, false, false, Options{MountGitRoot: true, Platform: "windows"}, "/workspaces/repo/app", &Mount{Source: repo, Target: "/workspaces/repo", Consistency: "consistent"}, true},
		{"configured folder", sub, true, false, linux, "", &Mount{Source: repo, Target: "/workspaces/repo"}, true},
		{"configured mount", sub, false, true, linux, "/workspaces/repo/app", nil, true},
		{"both configured", sub, true, true, linux, "", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Resolve(tc.local, tc.folder, tc.mount, tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			if got.WorkspaceFolder != tc.wantFolder {
				t.Errorf("folder = %q, want %q", got.WorkspaceFolder, tc.wantFolder)
			}
			if (got.WorkspaceMount == nil) != (tc.wantMount == nil) || got.WorkspaceMount != nil && *got.WorkspaceMount != *tc.wantMount {
				t.Errorf("mount = %+v, want %+v", got.WorkspaceMount, tc.wantMount)
			}
			if got.AdditionalMount != nil {
				t.Errorf("additional mount = %+v", got.AdditionalMount)
			}
		})
	}
}

func TestResolveWorktreeCommonDir(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(base, "src", "repo")
	makeRepo(t, repo)
	admin := filepath.Join(repo, ".git", "worktrees", "topic")
	writeFile(t, filepath.Join(admin, "HEAD"), "ref: refs/heads/topic\n")
	writeFile(t, filepath.Join(admin, "commondir"), "../..\n")
	worktree := filepath.Join(base, "src", "trees", "topic")
	writeFile(t, filepath.Join(worktree, ".git"), "gitdir: ../../repo/.git/worktrees/topic\n")
	sub := mkdir(t, filepath.Join(worktree, "pkg"))
	opts := Options{MountGitRoot: true, MountGitWorktreeCommonDir: true, Platform: "linux"}

	got, err := Resolve(sub, false, false, opts)
	if err != nil {
		t.Fatal(err)
	}
	want := Configuration{
		WorkspaceFolder: "/workspaces/trees/topic/pkg",
		WorkspaceMount:  &Mount{Source: worktree, Target: "/workspaces/trees/topic"},
		AdditionalMount: &Mount{Source: filepath.Join(repo, ".git"), Target: "/workspaces/repo/.git"},
	}
	if got.WorkspaceFolder != want.WorkspaceFolder || *got.WorkspaceMount != *want.WorkspaceMount || got.AdditionalMount == nil || *got.AdditionalMount != *want.AdditionalMount {
		t.Fatalf("Resolve = %+v %+v %+v", got, got.WorkspaceMount, got.AdditionalMount)
	}

	// Without the flag, or without Git-root mounting, the worktree mounts alone.
	for _, o := range []Options{{MountGitRoot: true, Platform: "linux"}, {MountGitWorktreeCommonDir: true, Platform: "linux"}} {
		got, err := Resolve(worktree, false, false, o)
		if err != nil || got.AdditionalMount != nil || got.WorkspaceFolder != "/workspaces/topic" {
			t.Fatalf("options %+v: %+v %v", o, got, err)
		}
	}

	// An absolute gitdir is not handled, as upstream.
	absolute := filepath.Join(base, "abs")
	writeFile(t, filepath.Join(absolute, ".git"), "gitdir: "+admin+"\n")
	got, err = Resolve(absolute, false, false, opts)
	if err != nil || got.AdditionalMount != nil || got.WorkspaceFolder != "/workspaces/abs" {
		t.Fatalf("absolute gitdir: %+v %v", got, err)
	}
}

func TestMountString(t *testing.T) {
	for _, tc := range []struct {
		m    Mount
		want string
	}{
		{Mount{Source: "/src/a", Target: "/workspaces/a"}, "type=bind,source=/src/a,target=/workspaces/a"},
		{Mount{Source: "/src/a,b", Target: "/workspaces/a,b", Consistency: "cached"}, `type=bind,"source=/src/a,b","target=/workspaces/a,b",consistency=cached`},
	} {
		if got := tc.m.String(); got != tc.want {
			t.Errorf("String() = %s, want %s", got, tc.want)
		}
	}
}
