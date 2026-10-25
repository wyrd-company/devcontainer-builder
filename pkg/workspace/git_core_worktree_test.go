// relationships:
//   references: [git, git_config, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

// core.worktree selects the working tree after Git's repository format check.
// An absolute value is resolved as a path, a relative value by changing to the
// repository folder and then to the value. Upstream resolves the probe's cdup,
// which is relative inside that working tree and absolute outside it.
func TestGitRootCoreWorktreeMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	const v0 = "[core]\n\trepositoryformatversion = 0\n"
	const worktreeConfig = "[extensions]\n\tworktreeConfig\n"
	worktree := func(value string) string { return v0 + "\tworktree = " + value + "\n" }
	cases := []struct{ name, config, worktree string }{
		{"absolute-other", worktree("$BASE/other"), ""},
		{"absolute-inside", worktree("$BASE/tree/app"), ""},
		{"absolute-tree", worktree("$BASE/tree"), ""},
		{"absolute-missing", worktree("$BASE/missing"), ""},
		{"absolute-missing-parent", worktree("$BASE/missing/sub"), ""},
		{"absolute-file", worktree("$BASE/file"), ""},
		{"absolute-symlink", worktree("$BASE/link"), ""},
		{"absolute-symlink-inside", worktree("$BASE/tree/app-link"), ""},
		{"absolute-trailing-separator", worktree("$BASE/other/"), ""},
		{"absolute-dot-components", worktree("$BASE/tree/../other/./"), ""},
		{"absolute-file-dot-dot", worktree("$BASE/file/../other"), ""},
		{"absolute-quoted", worktree("\"$BASE/other\""), ""},
		{"absolute-comment", worktree("$BASE/other ; note"), ""},
		{"relative-parent", worktree(".."), ""},
		{"relative-dot", worktree("."), ""},
		{"relative-inside", worktree("../app"), ""},
		{"relative-other", worktree("../../other"), ""},
		{"relative-missing", worktree("../missing"), ""},
		{"relative-file", worktree("../../file"), ""},
		{"relative-file-dot-dot", worktree("../../file/.."), ""},
		{"relative-symlink-dot-dot", worktree("../../link/.."), ""},
		{"relative-symlink-inside", worktree("../app-link"), ""},
		{"empty", worktree(""), ""},
		{"last-wins", worktree("$BASE/missing/sub") + "\tworktree = $BASE/other\n", ""},
		{"unversioned", "[core]\n\tworktree = $BASE/other\n", ""},
		{"negative-version", "[core]\n\trepositoryformatversion = -1\n\tworktree = $BASE/other\n", ""},
		{"subsection", v0 + "[core \"x\"]\n\tworktree = $BASE/other\n", ""},
		{"include", v0 + "[include]\n\tpath = extra\n", ""},
		{"bare", worktree("$BASE/other") + "\tbare = true\n", ""},
		{"bare-false", worktree("$BASE/other") + "\tbare = false\n", ""},
		{"bare-relative-missing", worktree("../missing") + "\tbare = true\n", ""},
		{"work-tree-env", worktree("../missing"), ""},
		{"worktree-config", v0 + worktreeConfig, "[core]\n\tworktree = $BASE/other\n"},
		{"worktree-config-common", worktree("$BASE/other") + worktreeConfig, "[core]\n\tbare = false\n"},
		{"worktree-config-override", worktree("$BASE/tree/app") + worktreeConfig, "[core]\n\tworktree = $BASE/other\n"},
		{"worktree-config-relative", v0 + worktreeConfig, "[core]\n\tworktree = ..\n"},
		{"worktree-config-bare", worktree("$BASE/other") + worktreeConfig, "[core]\n\tbare\n"},
		{"worktree-config-off", v0 + "[extensions]\n\tworktreeConfig = false\n", "[core]\n\tworktree = $BASE/other\n"},
	}
	for _, route := range []string{"discovered", "gitfile", "explicit", "self", "self-nested", "linked", "common-env"} {
		for _, c := range cases {
			t.Run(c.name+"/"+route, func(t *testing.T) {
				base := t.TempDir()
				tree := filepath.Join(base, "tree")
				env := testgit.Env()
				run := func(dir string, args ...string) {
					t.Helper()
					cmd := exec.Command(git, args...)
					cmd.Dir = dir
					cmd.Env = env
					if out, e := cmd.CombinedOutput(); e != nil {
						t.Fatalf("git %v: %v %s", args, e, out)
					}
				}
				run(base, "init", "-q", "-b", "main", tree)
				mkdir(t, filepath.Join(base, "other", "sub"))
				writeFile(t, filepath.Join(base, "file"), "")
				for link, target := range map[string]string{
					filepath.Join(base, "link"):     filepath.Join(base, "other", "sub"),
					filepath.Join(tree, "app-link"): filepath.Join(tree, "app"),
				} {
					if e := os.Symlink(target, link); e != nil {
						t.Fatal(e)
					}
				}
				admin := filepath.Join(tree, ".git")
				worktreeAdmin := admin
				set := func(key, value string) {
					env = append(env, key+"="+value)
					t.Setenv(key, value)
				}
				folders := []string{tree, filepath.Join(tree, "app", "deep")}
				switch route {
				case "gitfile":
					// A separate repository folder has no commondir, so its config applies.
					moved := filepath.Join(base, "admin")
					if e := os.Rename(admin, moved); e != nil {
						t.Fatal(e)
					}
					writeFile(t, admin, "gitdir: "+moved+"\n")
					admin, worktreeAdmin = moved, moved
				case "explicit":
					set("GIT_DIR", admin)
				case "self":
					folders = []string{admin}
				case "self-nested":
					folders = []string{filepath.Join(admin, "refs")}
				case "linked":
					run(tree, "commit", "-q", "--allow-empty", "-m", "initial")
					run(tree, "worktree", "add", "-q", filepath.Join(base, "linked"))
					folders = []string{filepath.Join(base, "linked"), filepath.Join(base, "linked", "app")}
					worktreeAdmin = filepath.Join(admin, "worktrees", "linked")
				case "common-env":
					set("GIT_COMMON_DIR", admin)
				}
				for _, folder := range folders {
					mkdir(t, folder)
				}
				expand := func(s string) string { return strings.ReplaceAll(s, "$BASE", base) }
				writeFile(t, filepath.Join(admin, "config"), expand(c.config))
				if c.worktree != "" {
					writeFile(t, filepath.Join(worktreeAdmin, "config.worktree"), expand(c.worktree))
				}
				switch c.name {
				case "include":
					writeFile(t, filepath.Join(admin, "extra"), expand("[core]\n\tworktree = $BASE/other\n"))
				case "work-tree-env":
					set("GIT_WORK_TREE", tree)
				}
				set("GIT_CEILING_DIRECTORIES", base)
				for _, folder := range folders {
					cmd := exec.Command(git, "rev-parse", "--show-cdup")
					cmd.Dir = folder
					cmd.Env = env
					out, e := cmd.Output()
					want := ""
					if e == nil {
						// Upstream resolves the trimmed cdup from the workspace folder.
						cdup := strings.TrimSpace(string(out))
						want = filepath.Join(folder, cdup)
						if filepath.IsAbs(cdup) {
							want = filepath.Clean(cdup)
						}
					}
					got, e := GitRoot(folder)
					if e != nil || got != want {
						t.Fatalf("GitRoot(%s) = %q, %v; git = %q", folder, got, e, want)
					}
					resolved, e := Resolve(folder, false, false, Options{MountGitRoot: true, Platform: "linux"})
					if e != nil {
						t.Fatal(e)
					}
					mount := want
					if mount == "" {
						mount = folder
					}
					if resolved.WorkspaceMount.Source != mount {
						t.Fatalf("mount(%s) = %q; want %q", folder, resolved.WorkspaceMount.Source, mount)
					}
				}
			})
		}
	}
}

// core.worktree selection with fixed expectations, independent of the host Git.
func TestGitRootCoreWorktreeSelection(t *testing.T) {
	const v0 = "[core]\n\trepositoryformatversion = 0\n"
	cases := []struct{ name, config, env, root string }{
		{"absolute-outside", v0 + "\tworktree = $BASE/other\n", "", "$BASE/other"},
		{"absolute-missing-parent", v0 + "\tworktree = $BASE/missing/sub\n", "", ""},
		{"relative-inside", v0 + "\tworktree = ../app\n", "", "$BASE/tree/app"},
		{"relative-physical", v0 + "\tworktree = ../../link/..\n", "", "$BASE/other"},
		{"relative-missing", v0 + "\tworktree = ../missing\n", "", ""},
		{"relative-file", v0 + "\tworktree = ../../file\n", "", ""},
		{"empty", v0 + "\tworktree =\n", "", ""},
		{"unversioned", "[core]\n\tworktree = $BASE/other\n", "", "$BASE/tree"},
		{"bare", v0 + "\tworktree = $BASE/other\n\tbare = true\n", "", "$BASE/tree/app/deep"},
		{"work-tree-env", v0 + "\tworktree = ../missing\n", "GIT_WORK_TREE", "$BASE/tree"},
		{"common-env", v0 + "\tworktree = $BASE/other\n", "GIT_COMMON_DIR", "$BASE/tree"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			tree := filepath.Join(base, "tree")
			makeRepo(t, tree)
			mkdir(t, filepath.Join(base, "other", "sub"))
			writeFile(t, filepath.Join(base, "file"), "")
			if err := os.Symlink(filepath.Join(base, "other", "sub"), filepath.Join(base, "link")); err != nil {
				t.Fatal(err)
			}
			expand := func(s string) string { return strings.ReplaceAll(s, "$BASE", base) }
			writeFile(t, filepath.Join(tree, ".git", "config"), expand(c.config))
			t.Setenv("GIT_CEILING_DIRECTORIES", base)
			switch c.env {
			case "GIT_WORK_TREE":
				t.Setenv(c.env, tree)
			case "GIT_COMMON_DIR":
				t.Setenv(c.env, filepath.Join(tree, ".git"))
			}
			folder := mkdir(t, filepath.Join(tree, "app", "deep"))
			want := expand(c.root)
			if got, err := GitRoot(folder); err != nil || got != want {
				t.Fatalf("GitRoot = %q, %v; want %q", got, err, want)
			}
		})
	}
}
