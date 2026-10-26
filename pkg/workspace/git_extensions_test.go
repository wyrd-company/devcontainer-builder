// relationships:
//   references: [git, git_extensions, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

// Git checks each extensions.* value as it reads the repository config, then
// rejects an extension it does not know in a version 1 repository and a
// version 1 extension in a version 0 repository. Its probe then fails, so
// upstream finds no Git root. ok is Git 2.55's result: true when the Git root
// is the one that the same layout gives without the case's config.
func TestGitRootRepositoryExtensionsMatchGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	const v0 = "[core]\n\trepositoryformatversion = 0\n[extensions]\n"
	const v1 = "[core]\n\trepositoryformatversion = 1\n[extensions]\n"
	const unversioned = "[extensions]\n"
	cases := []struct {
		name, config string
		ok           bool
		// requires names an extension that the host Git must know.
		requires string
		// rust marks a result that differs in Git built without Rust.
		rust bool
	}{
		{"v0-unknown", v0 + "\texample = 1\n", true, "", false},
		{"v0-noop", v0 + "\tnoop\n", true, "", false},
		{"v0-objectformat", v0 + "\tobjectFormat = sha1\n", false, "objectformat", false},
		{"v0-noop-v1", v0 + "\tnoop-v1\n", false, "noop-v1", false},
		{"v0-refstorage", v0 + "\trefStorage = files\n", false, "refstorage", false},
		{"v0-relative-worktrees", v0 + "\trelativeWorktrees = true\n", false, "relativeworktrees", false},
		{"v0-submodule-path-config", v0 + "\tsubmodulePathConfig = true\n", false, "submodulepathconfig", false},
		{"v0-objectformat-invalid", v0 + "\tobjectFormat = md5\n", false, "objectformat", false},
		{"v1-unknown", v1 + "\texample = 1\n", false, "", false},
		{"v1-unknown-upper", v1 + "\tExample = 1\n", false, "", false},
		{"v1-unknown-subsection", "[core]\n\trepositoryformatversion = 1\n[extensions \"noop\"]\n\tkey = 1\n", false, "", false},
		{"v1-unknown-dotted", "[core]\n\trepositoryformatversion = 1\n[extensions.noop]\n\tkey = 1\n", false, "", false},
		{"v1-v0-extensions", v1 + "\tNOOP = maybe\n\tpreciousObjects\n\tpartialClone = origin\n\tworktreeConfig = false\n", true, "", false},
		{"v1-noop-v1", v1 + "\tnoop-v1 = anything\n", true, "noop-v1", false},
		{"v1-objectformat-sha1", v1 + "\tobjectFormat = sha1\n", true, "objectformat", false},
		{"v1-objectformat-sha256", v1 + "\tobjectFormat = sha256\n", true, "objectformat", false},
		{"v1-objectformat-case", v1 + "\tobjectFormat = SHA1\n", false, "objectformat", false},
		{"v1-objectformat-invalid", v1 + "\tobjectFormat = md5\n", false, "objectformat", false},
		{"v1-objectformat-empty", v1 + "\tobjectFormat =\n", false, "objectformat", false},
		{"v1-objectformat-no-value", v1 + "\tobjectFormat\n", false, "objectformat", false},
		{"v1-refstorage-files", v1 + "\trefStorage = files\n", true, "refstorage", false},
		{"v1-refstorage-reftable", v1 + "\trefStorage = reftable\n", true, "refstorage", false},
		{"v1-refstorage-uri", v1 + "\trefStorage = files://payload\n", true, "refstorage", false},
		{"v1-refstorage-case", v1 + "\trefStorage = Files\n", false, "refstorage", false},
		{"v1-refstorage-invalid", v1 + "\trefStorage = example\n", false, "refstorage", false},
		{"v1-refstorage-empty-format", v1 + "\trefStorage = ://files\n", false, "refstorage", false},
		{"v1-refstorage-no-value", v1 + "\trefStorage\n", false, "refstorage", false},
		{"v1-relative-worktrees", v1 + "\trelativeWorktrees = yes\n", true, "relativeworktrees", false},
		{"v1-relative-worktrees-no-value", v1 + "\trelativeWorktrees\n", true, "relativeworktrees", false},
		{"v1-relative-worktrees-invalid", v1 + "\trelativeWorktrees = maybe\n", false, "relativeworktrees", false},
		{"v1-submodule-path-config", v1 + "\tsubmodulePathConfig = 0\n", true, "submodulepathconfig", false},
		{"v1-submodule-path-config-invalid", v1 + "\tsubmodulePathConfig = maybe\n", false, "submodulepathconfig", false},
		{"v1-compat-invalid", v1 + "\tcompatObjectFormat = md5\n", false, "objectformat", false},
		{"v1-compat-no-value", v1 + "\tcompatObjectFormat\n", false, "objectformat", false},
		{"v1-compat-twice", v1 + "\tobjectFormat = sha1\n\tcompatObjectFormat = sha256\n\tcompatObjectFormat = sha256\n", false, "objectformat", false},
		{"v1-compat-default-format", v1 + "\tcompatObjectFormat = sha1\n", false, "objectformat", false},
		{"v1-compat-last-format", v1 + "\tobjectFormat = sha1\n\tcompatObjectFormat = sha256\n\tobjectFormat = sha256\n", false, "objectformat", false},
		{"v1-compat-other-format", v1 + "\tcompatObjectFormat = sha256\n", true, "objectformat", true},
		{"unversioned-unknown", unversioned + "\texample = 1\n", true, "", false},
		{"unversioned-v1-only", unversioned + "\tobjectFormat = sha256\n\tcompatObjectFormat = sha256\n", true, "objectformat", false},
		{"unversioned-objectformat-invalid", unversioned + "\tobjectFormat = md5\n", false, "objectformat", false},
		{"unversioned-relative-worktrees-invalid", unversioned + "\trelativeWorktrees = maybe\n", false, "relativeworktrees", false},
		{"negative-version-unknown", "[core]\n\trepositoryformatversion = -1\n[extensions]\n\texample = 1\n", true, "", false},
	}
	known := map[string]bool{}
	for _, route := range []string{"discovered", "gitfile", "explicit", "self", "linked", "symlinked-folder"} {
		for _, c := range cases {
			t.Run(c.name+"/"+route, func(t *testing.T) {
				if c.requires != "" {
					if _, seen := known[c.requires]; !seen {
						known[c.requires] = gitKnowsExtension(t, git, c.requires)
					}
					if !known[c.requires] {
						t.Skipf("host Git does not know extensions.%s", c.requires)
					}
				}
				base, err := filepath.EvalSymlinks(t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				tree := filepath.Join(base, "tree")
				env := testgit.Env()
				run := func(dir string, args ...string) {
					t.Helper()
					cmd := exec.Command(git, args...)
					cmd.Dir, cmd.Env = dir, env
					if out, err := cmd.CombinedOutput(); err != nil {
						t.Fatalf("git %v: %v %s", args, err, out)
					}
				}
				run(base, "init", "-q", "-b", "main", tree)
				admin := filepath.Join(tree, ".git")
				set := func(key, value string) {
					env = append(env, key+"="+value)
					t.Setenv(key, value)
				}
				folder := filepath.Join(tree, "app", "deep")
				switch route {
				case "gitfile":
					moved := filepath.Join(base, "admin")
					if err := os.Rename(admin, moved); err != nil {
						t.Fatal(err)
					}
					writeFile(t, admin, "gitdir: "+moved+"\n")
					admin = moved
				case "explicit":
					set("GIT_DIR", admin)
				case "self":
					folder = admin
				case "linked":
					run(tree, "commit", "-q", "--allow-empty", "-m", "initial")
					run(tree, "worktree", "add", "-q", filepath.Join(base, "linked"))
					folder = filepath.Join(base, "linked", "app")
				case "symlinked-folder":
					mkdir(t, folder)
					symlink(t, tree, filepath.Join(base, "alias"))
					folder = filepath.Join(base, "alias", "app", "deep")
				}
				mkdir(t, folder)
				set("GIT_CEILING_DIRECTORIES", base)
				probe := func() (string, string) {
					t.Helper()
					cmd := exec.Command(git, "rev-parse", "--show-cdup")
					cmd.Dir, cmd.Env = folder, env
					var stderr strings.Builder
					cmd.Stderr = &stderr
					out, err := cmd.Output()
					if err != nil {
						return "", stderr.String()
					}
					return filepath.Join(folder, strings.TrimSpace(string(out))), ""
				}
				control, stderr := probe()
				if control == "" {
					t.Fatalf("Git finds no root before the case config: %s", stderr)
				}
				writeFile(t, filepath.Join(admin, "config"), c.config)
				want, stderr := probe()
				expected := ""
				if c.ok {
					expected = control
				}
				if c.rust && want == "" && strings.Contains(stderr, "requires Rust") {
					// Git built without Rust rejects every compatibility hash.
					t.Logf("host Git without Rust: %s", strings.TrimSpace(stderr))
					want = expected
				}
				if want != expected {
					t.Fatalf("Git root = %q (%s); want %q", want, strings.TrimSpace(stderr), expected)
				}
				got, err := GitRoot(folder)
				if err != nil || got != want {
					t.Fatalf("GitRoot = %q, %v; Git = %q", got, err, want)
				}
				// The upstream CLI mounts the Git root, or the workspace
				// folder when Git's probe fails.
				source := want
				if source == "" {
					source = folder
				}
				rel, err := filepath.Rel(source, folder)
				if err != nil {
					t.Fatal(err)
				}
				target := path.Join("/workspaces", filepath.Base(source))
				resolved, err := Resolve(folder, false, false, Options{MountGitRoot: true, Platform: "linux"})
				if err != nil || resolved.WorkspaceMount == nil || resolved.WorkspaceMount.Source != source ||
					resolved.WorkspaceMount.Target != target || resolved.WorkspaceFolder != path.Join(target, filepath.ToSlash(rel)) {
					t.Fatalf("Resolve = %+v %+v, %v; want source %q target %q", resolved, resolved.WorkspaceMount, err, source, target)
				}
			})
		}
	}
}

// gitKnowsExtension reports whether the host Git accepts a valid value of a
// version 1 extension. Older Git rejects extensions added after it.
func gitKnowsExtension(t *testing.T, git, name string) bool {
	t.Helper()
	value := map[string]string{"objectformat": "sha1", "refstorage": "files"}[name]
	if value == "" {
		value = "true"
	}
	repo := filepath.Join(t.TempDir(), "known")
	cmd := exec.Command(git, "init", "-q", repo)
	cmd.Env = testgit.Env()
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("init: %s: %v", out, err)
	}
	writeFile(t, filepath.Join(repo, ".git", "config"), "[core]\n\trepositoryformatversion = 1\n[extensions]\n\t"+name+" = "+value+"\n")
	cmd = exec.Command(git, "rev-parse", "--git-dir")
	cmd.Dir, cmd.Env = repo, testgit.Env()
	return cmd.Run() == nil
}
