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

// Git's repository format check reads the common config before it selects a
// working tree. core.bare leaves no working tree, so upstream's empty cdup
// selects the caller folder, and config that Git rejects makes the probe fail.
func TestGitRootRepositoryFormatMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	const v0 = "[core]\n\trepositoryformatversion = 0\n"
	const v1 = "[core]\n\trepositoryformatversion = 1\n"
	const worktreeConfig = "[extensions]\n\tworktreeConfig\n"
	cases := []struct{ name, config, worktree string }{
		{"plain", v0, ""},
		{"bare", v0 + "\tbare = true\n", ""},
		{"bare-false", v0 + "\tbare = false\n", ""},
		{"bare-unversioned", "[core]\n\tbare = true\n", ""},
		{"bare-before-version", "[core]\n\tbare = true\n\trepositoryformatversion = 0\n", ""},
		{"bare-negative-version", "[core]\n\trepositoryformatversion = -1\n\tbare = true\n", ""},
		{"bare-version-1", v1 + "\tbare = true\n", ""},
		{"bare-no-value", v0 + "\tbare\n", ""},
		{"bare-empty-value", v0 + "\tbare =\n", ""},
		{"bare-word-case", v0 + "\tbare = YeS\n", ""},
		{"bare-integer", v0 + "\tbare = -1\n", ""},
		{"bare-unit", v0 + "\tbare = 1k\n", ""},
		{"bare-unit-m", v0 + "\tbare = 2m\n", ""},
		{"bare-unit-g", v0 + "\tbare = 1G\n", ""},
		{"bare-zero", v0 + "\tbare = 0x0\n", ""},
		{"bare-binary", v0 + "\tbare = 0b1\n", ""},
		{"bare-binary-signed", v0 + "\tbare = -0B1\n", ""},
		{"bare-binary-zero", v0 + "\tbare = 0b0\n", ""},
		{"bare-binary-max", v0 + "\tbare = 0b" + strings.Repeat("1", 31) + "\n", ""},
		{"bare-quoted", v0 + "\tbare = \"true\"\n", ""},
		{"bare-comment", v0 + "\tbare = true ; note\n", ""},
		{"bare-trailing-space", v0 + "\tbare = off \t\n", ""},
		{"bare-continuation", v0 + "\tbare = tr\\\nue\n", ""},
		{"bare-crlf", "[core]\r\n\trepositoryformatversion = 0\r\n\tbare\r\n", ""},
		{"bare-bom", "\ufeff" + v0 + "\tbare = true\n", ""},
		{"bare-nul", v0 + "\tbare = true\x00x\n", ""},
		{"bare-upper-names", "[CORE]\n\tRepositoryFormatVersion = 0\n\tBARE = true\n", ""},
		{"bare-inline", "[core] repositoryformatversion = 0\n[core] bare = true\n", ""},
		{"bare-last-wins", v0 + "\tbare = true\n\tbare = false\n", ""},
		{"bare-split-section", v0 + "[other]\n\tkey = value\n[core]\n\tbare = true\n", ""},
		{"bare-subsection", v0 + "[core \"x\"]\n\tbare = true\n", ""},
		{"bare-dotted-section", v0 + "[core.x]\n\tbare = true\n", ""},
		{"bare-no-section", "bare = true\n" + v0, ""},
		{"bare-include", v0 + "[include]\n\tpath = extra\n", ""},
		{"bare-with-worktree", v0 + "\tbare = true\n\tworktree = /nonexistent\n", ""},
		{"bare-work-tree-env", v0 + "\tbare = true\n", ""},
		{"version-padded", "[core]\n\trepositoryformatversion = \\t+1\n\tbare = true\n", ""},
		{"version-hex", "[core]\n\trepositoryformatversion = 0x1\n\tbare = true\n", ""},
		{"version-binary", "[core]\n\trepositoryformatversion = 0b1\n", ""},
		{"version-binary-unit", "[core]\n\trepositoryformatversion = 0b1k\n", ""},
		{"version-2", "[core]\n\trepositoryformatversion = 2\n", ""},
		{"version-octal", "[core]\n\trepositoryformatversion = 010\n", ""},
		{"version-unit", "[core]\n\trepositoryformatversion = 1k\n", ""},
		{"version-bad-octal", "[core]\n\trepositoryformatversion = 08\n", ""},
		{"version-range", "[core]\n\trepositoryformatversion = 2147483648\n", ""},
		{"version-empty", "[core]\n\trepositoryformatversion =\n", ""},
		{"version-no-value", "[core]\n\trepositoryformatversion\n", ""},
		{"version-subsection", "[core \"x\"]\n\trepositoryformatversion = 2\n", ""},
		{"bad-bare", v0 + "\tbare = maybe\n", ""},
		{"bad-bare-unversioned", "[core]\n\tbare = maybe\n", ""},
		{"bad-bare-quoted-space", v0 + "\tbare = \" true\"\n", ""},
		{"bad-bare-folded", v0 + "\tbare = ye\u017f\n", ""},
		{"bad-bare-range", v0 + "\tbare = 2147483648\n", ""},
		{"bad-bare-octal", v0 + "\tbare = 08\n", ""},
		{"bad-bare-sign", v0 + "\tbare = +\n", ""},
		{"bad-bare-binary-prefix", v0 + "\tbare = 0b\n", ""},
		{"bad-bare-binary-digit", v0 + "\tbare = 0b2\n", ""},
		{"bad-bare-binary-range", v0 + "\tbare = 0b1" + strings.Repeat("0", 31) + "\n", ""},
		{"worktree-no-value", v0 + "\tworktree\n", ""},
		{"extension-worktree-config", v0 + "[extensions]\n\tworktreeConfig = maybe\n", ""},
		{"extension-precious-objects", v0 + "[extensions]\n\tpreciousObjects = maybe\n", ""},
		{"extension-precious-objects-binary", v0 + "\tbare = true\n[extensions]\n\tpreciousObjects = 0B1\n", ""},
		{"extension-partial-clone", v0 + "[extensions]\n\tpartialClone\n", ""},
		{"extension-noop", v0 + "\tbare = true\n[extensions]\n\tnoop = maybe\n", ""},
		{"extension-unknown", v0 + "\tbare = true\n[extensions]\n\texample = 1\n", ""},
		{"syntax-section", "[core\n\trepositoryformatversion = 0\n", ""},
		{"syntax-empty-section", "[]\n" + v0, ""},
		{"syntax-subsection-newline", "[core \"x\n\"]\n" + v0, ""},
		{"syntax-key", v0 + "\t1bare = true\n", ""},
		{"syntax-assignment", v0 + "\tbare : true\n", ""},
		{"syntax-quote", v0 + "\tkey = \"value\n", ""},
		{"syntax-escape", v0 + "\tkey = a\\qb\n", ""},
		{"syntax-partial-bom", "\xef\xbb" + v0, ""},
		{"syntax-unversioned", "[core\n", ""},
		{"config-missing", "", ""},
		{"config-directory", "", ""},
		{"config-symlink", v0 + "\tbare = true\n", ""},
		{"config-dangling", "", ""},
		{"config-unreadable", v0, ""},
		{"worktree-config-bare", v1 + worktreeConfig, "[core]\n\tbare\n"},
		{"worktree-config-false", v0 + "\tbare = true\n" + worktreeConfig, "[core]\n\tbare = false\n"},
		{"worktree-config-version", v0 + worktreeConfig, "[core]\n\trepositoryformatversion = 5\n\tbare\n"},
		{"worktree-config-binary", v1 + "[extensions]\n\tworktreeConfig = 0b1\n", "[core]\n\tbare\n"},
		{"worktree-config-off", v1 + "[extensions]\n\tworktreeConfig = false\n", "[core]\n\tbare\n"},
		{"worktree-config-unset", v1 + "\tbare\n", "[core]\n\tbare = false\n"},
		{"worktree-config-unversioned", worktreeConfig, "[core]\n\tbare\n"},
		{"worktree-config-syntax", v1 + worktreeConfig, "[core\n"},
		{"worktree-config-bad-bare", v1 + worktreeConfig, "[core]\n\tbare = maybe\n"},
		{"worktree-config-directory", v1 + worktreeConfig, ""},
	}
	binary := gitReadsBinaryIntegers(git)
	for _, route := range []string{"discovered", "gitfile", "explicit", "self", "linked", "common-env"} {
		for _, c := range cases {
			t.Run(c.name+"/"+route, func(t *testing.T) {
				if strings.Contains(c.name, "binary") && !binary {
					t.Skip("host Git reads no 0b integers; TestGitRootReadsBinaryIntegers covers them")
				}
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
				admin := filepath.Join(tree, ".git")
				worktreeAdmin := admin
				set := func(key, value string) {
					env = append(env, key+"="+value)
					t.Setenv(key, value)
				}
				probe := filepath.Join(tree, "app", "deep")
				switch route {
				case "gitfile":
					// A separate repository folder has no commondir, so its core.bare applies.
					moved := filepath.Join(base, "admin")
					if e := os.Rename(admin, moved); e != nil {
						t.Fatal(e)
					}
					writeFile(t, admin, "gitdir: "+moved+"\n")
					admin, worktreeAdmin = moved, moved
				case "explicit":
					set("GIT_DIR", admin)
				case "self":
					probe = admin
				case "linked":
					run(tree, "commit", "-q", "--allow-empty", "-m", "initial")
					run(tree, "worktree", "add", "-q", filepath.Join(base, "linked"))
					probe = filepath.Join(base, "linked", "app")
					worktreeAdmin = filepath.Join(admin, "worktrees", "linked")
				case "common-env":
					set("GIT_COMMON_DIR", admin)
				}
				mkdir(t, probe)
				config := filepath.Join(admin, "config")
				writeFile(t, config, c.config)
				if c.worktree != "" {
					writeFile(t, filepath.Join(worktreeAdmin, "config.worktree"), c.worktree)
				}
				replace := func(make func()) {
					t.Helper()
					if e := os.Remove(config); e != nil {
						t.Fatal(e)
					}
					make()
				}
				switch c.name {
				case "bare-include":
					writeFile(t, filepath.Join(admin, "extra"), "[core]\n\tbare = true\n")
				case "bare-work-tree-env":
					set("GIT_WORK_TREE", tree)
				case "config-missing":
					replace(func() {})
				case "config-directory":
					replace(func() { mkdir(t, config) })
				case "config-symlink":
					real := filepath.Join(base, "real-config")
					writeFile(t, real, c.config)
					replace(func() {
						if e := os.Symlink(real, config); e != nil {
							t.Fatal(e)
						}
					})
				case "config-dangling":
					replace(func() {
						if e := os.Symlink(filepath.Join(base, "absent"), config); e != nil {
							t.Fatal(e)
						}
					})
				case "config-unreadable":
					if e := os.Chmod(config, 0); e != nil {
						t.Fatal(e)
					}
				case "worktree-config-directory":
					mkdir(t, filepath.Join(worktreeAdmin, "config.worktree"))
				}
				set("GIT_CEILING_DIRECTORIES", base)
				cmd := exec.Command(git, "rev-parse", "--show-cdup")
				cmd.Dir = probe
				cmd.Env = env
				out, e := cmd.Output()
				want := ""
				if e == nil {
					// Upstream resolves the trimmed cdup from the workspace folder.
					cdup := strings.TrimSpace(string(out))
					want = filepath.Join(probe, cdup)
					if filepath.IsAbs(cdup) {
						want = filepath.Clean(cdup)
					}
				}
				got, e := GitRoot(probe)
				if e != nil || got != want {
					t.Fatalf("GitRoot = %q, %v; git = %q", got, e, want)
				}
				resolved, e := Resolve(probe, false, false, Options{MountGitRoot: true, Platform: "linux"})
				if e != nil {
					t.Fatal(e)
				}
				mount := want
				if mount == "" {
					mount = probe
				}
				if resolved.WorkspaceMount.Source != mount {
					t.Fatalf("mount = %q; want %q", resolved.WorkspaceMount.Source, mount)
				}
			})
		}
	}
}

// gitReadsBinaryIntegers reports whether the host Git reads 0b integers. Git
// parses integers with the C library's strtoimax, which reads them in C23
// mode, as in glibc 2.38 and later.
func gitReadsBinaryIntegers(git string) bool {
	cmd := exec.Command(git, "-c", "probe.value=0b1", "config", "--type=bool", "probe.value")
	cmd.Env = testgit.Env()
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

// The format check reads integers as Git with a C23 C library does, whatever
// the host Git reads.
func TestGitRootReadsBinaryIntegers(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git unavailable")
	}
	cases := []struct{ name, config, root string }{
		{"version", "[core]\n\trepositoryformatversion = 0b1\n", "tree"},
		{"version-above-one", "[core]\n\trepositoryformatversion = 0b10\n", ""},
		{"bare", "[core]\n\trepositoryformatversion = 0\n\tbare = -0B1\n", "folder"},
		{"bare-false", "[core]\n\trepositoryformatversion = 0\n\tbare = 0b0\n", "tree"},
		{"bare-max", "[core]\n\trepositoryformatversion = 0\n\tbare = 0b" + strings.Repeat("1", 31) + "\n", "folder"},
		{"bare-no-digits", "[core]\n\trepositoryformatversion = 0\n\tbare = 0b2\n", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := t.TempDir()
			tree := filepath.Join(base, "tree")
			cmd := exec.Command(git, "init", "-q", "-b", "main", tree)
			cmd.Env = testgit.Env()
			if out, e := cmd.CombinedOutput(); e != nil {
				t.Fatalf("git init: %v %s", e, out)
			}
			writeFile(t, filepath.Join(tree, ".git", "config"), c.config)
			folder := mkdir(t, filepath.Join(tree, "app"))
			want := map[string]string{"tree": tree, "folder": folder}[c.root]
			got, e := GitRoot(folder)
			if e != nil || got != want {
				t.Fatalf("GitRoot = %q, %v; want %q", got, e, want)
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
				t.Fatalf("mount = %q; want %q", resolved.WorkspaceMount.Source, mount)
			}
		})
	}
}
