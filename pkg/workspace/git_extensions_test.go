// relationships:
//   references: [git, git_extensions, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

const (
	extensionsV0          = "[core]\n\trepositoryformatversion = 0\n[extensions]\n"
	extensionsV1          = "[core]\n\trepositoryformatversion = 1\n[extensions]\n"
	extensionsUnversioned = "[extensions]\n"
)

// extensionCase is one repository config. ok is the result of Git 2.55 built
// without Rust, which native discovery follows: true when the Git root is the
// one that the same layout gives without the config. The other fields declare
// how other Git builds differ. since is the Git 2.x minor release that first
// knows the case's extension form; older Git treats it as an unknown
// extension. rust marks a case that Git 2.45 to 2.53, and Git built with Rust,
// accept.
type extensionCase struct {
	name, config string
	version      int
	ok           bool
	since        int
	rust         bool
}

var extensionCases = []extensionCase{
	{"v0-unknown", extensionsV0 + "\texample = 1\n", 0, true, 0, false},
	{"v0-noop", extensionsV0 + "\tnoop\n", 0, true, 0, false},
	{"v0-objectformat", extensionsV0 + "\tobjectFormat = sha1\n", 0, false, 0, false},
	{"v0-noop-v1", extensionsV0 + "\tnoop-v1\n", 0, false, 0, false},
	{"v0-refstorage", extensionsV0 + "\trefStorage = files\n", 0, false, 44, false},
	{"v0-relative-worktrees", extensionsV0 + "\trelativeWorktrees = true\n", 0, false, 48, false},
	{"v0-submodule-path-config", extensionsV0 + "\tsubmodulePathConfig = true\n", 0, false, 54, false},
	{"v0-objectformat-invalid", extensionsV0 + "\tobjectFormat = md5\n", 0, false, 0, false},
	{"v1-unknown", extensionsV1 + "\texample = 1\n", 1, false, 0, false},
	{"v1-unknown-upper", extensionsV1 + "\tExample = 1\n", 1, false, 0, false},
	{"v1-unknown-subsection", "[core]\n\trepositoryformatversion = 1\n[extensions \"noop\"]\n\tkey = 1\n", 1, false, 0, false},
	{"v1-unknown-dotted", "[core]\n\trepositoryformatversion = 1\n[extensions.noop]\n\tkey = 1\n", 1, false, 0, false},
	{"v1-v0-extensions", extensionsV1 + "\tNOOP = maybe\n\tpreciousObjects\n\tpartialClone = origin\n\tworktreeConfig = false\n", 1, true, 0, false},
	{"v1-noop-v1", extensionsV1 + "\tnoop-v1 = anything\n", 1, true, 0, false},
	{"v1-objectformat-sha1", extensionsV1 + "\tobjectFormat = sha1\n", 1, true, 0, false},
	{"v1-objectformat-sha256", extensionsV1 + "\tobjectFormat = sha256\n", 1, true, 0, false},
	{"v1-objectformat-case", extensionsV1 + "\tobjectFormat = SHA1\n", 1, false, 0, false},
	{"v1-objectformat-invalid", extensionsV1 + "\tobjectFormat = md5\n", 1, false, 0, false},
	{"v1-objectformat-empty", extensionsV1 + "\tobjectFormat =\n", 1, false, 0, false},
	{"v1-objectformat-no-value", extensionsV1 + "\tobjectFormat\n", 1, false, 0, false},
	{"v1-refstorage-files", extensionsV1 + "\trefStorage = files\n", 1, true, 44, false},
	{"v1-refstorage-reftable", extensionsV1 + "\trefStorage = reftable\n", 1, true, 44, false},
	{"v1-refstorage-uri", extensionsV1 + "\trefStorage = files://payload\n", 1, true, 54, false},
	{"v1-refstorage-case", extensionsV1 + "\trefStorage = Files\n", 1, false, 44, false},
	{"v1-refstorage-invalid", extensionsV1 + "\trefStorage = example\n", 1, false, 44, false},
	{"v1-refstorage-empty-format", extensionsV1 + "\trefStorage = ://files\n", 1, false, 44, false},
	{"v1-refstorage-no-value", extensionsV1 + "\trefStorage\n", 1, false, 44, false},
	{"v1-relative-worktrees", extensionsV1 + "\trelativeWorktrees = yes\n", 1, true, 48, false},
	{"v1-relative-worktrees-no-value", extensionsV1 + "\trelativeWorktrees\n", 1, true, 48, false},
	{"v1-relative-worktrees-invalid", extensionsV1 + "\trelativeWorktrees = maybe\n", 1, false, 48, false},
	{"v1-submodule-path-config", extensionsV1 + "\tsubmodulePathConfig = 0\n", 1, true, 54, false},
	{"v1-submodule-path-config-invalid", extensionsV1 + "\tsubmodulePathConfig = maybe\n", 1, false, 54, false},
	{"v1-compat-invalid", extensionsV1 + "\tcompatObjectFormat = md5\n", 1, false, 45, false},
	{"v1-compat-no-value", extensionsV1 + "\tcompatObjectFormat\n", 1, false, 45, false},
	{"v1-compat-twice", extensionsV1 + "\tobjectFormat = sha1\n\tcompatObjectFormat = sha256\n\tcompatObjectFormat = sha256\n", 1, false, 45, false},
	{"v1-compat-default-format", extensionsV1 + "\tcompatObjectFormat = sha1\n", 1, false, 45, false},
	{"v1-compat-same-sha256", extensionsV1 + "\tobjectFormat = sha256\n\tcompatObjectFormat = sha256\n", 1, false, 45, false},
	{"v1-compat-last-format", extensionsV1 + "\tobjectFormat = sha1\n\tcompatObjectFormat = sha256\n\tobjectFormat = sha256\n", 1, false, 45, false},
	{"v1-compat-other-format", extensionsV1 + "\tcompatObjectFormat = sha256\n", 1, false, 45, true},
	{"v1-compat-sha256-to-sha1", extensionsV1 + "\tobjectFormat = sha256\n\tcompatObjectFormat = sha1\n", 1, false, 45, true},
	{"unversioned-unknown", extensionsUnversioned + "\texample = 1\n", -1, true, 0, false},
	{"unversioned-v1-only", extensionsUnversioned + "\tobjectFormat = sha256\n\tcompatObjectFormat = sha256\n", -1, true, 45, false},
	{"unversioned-compat-twice", extensionsUnversioned + "\tcompatObjectFormat = sha256\n\tcompatObjectFormat = sha256\n", -1, false, 45, false},
	{"unversioned-compat-invalid", extensionsUnversioned + "\tcompatObjectFormat = md5\n", -1, false, 45, false},
	{"unversioned-objectformat-invalid", extensionsUnversioned + "\tobjectFormat = md5\n", -1, false, 0, false},
	{"unversioned-relative-worktrees-invalid", extensionsUnversioned + "\trelativeWorktrees = maybe\n", -1, false, 48, false},
	{"negative-version-unknown", "[core]\n\trepositoryformatversion = -1\n[extensions]\n\texample = 1\n", -1, true, 0, false},
}

// hostOK reports the result that the host Git gives, by the differences the
// case declares.
func (c extensionCase) hostOK(host hostGit) bool {
	switch {
	case host.minor < c.since:
		// An unknown extension fails a version 1 repository only.
		return c.version < 1
	case c.rust && (host.rust || host.minor < 54):
		return true
	}
	return c.ok
}

// hostGit is the host Git's 2.x minor release and whether it is built with
// Rust.
type hostGit struct {
	version string
	minor   int
	rust    bool
}

// readHostGit reads the version and build of the host Git.
func readHostGit(t *testing.T, git string) hostGit {
	t.Helper()
	cmd := exec.Command(git, "version", "--build-options")
	cmd.Env = testgit.Env()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git version: %v", err)
	}
	lines := strings.Split(string(out), "\n")
	host := hostGit{version: strings.TrimPrefix(lines[0], "git version ")}
	fields := strings.SplitN(host.version, ".", 3)
	if len(fields) < 2 || fields[0] != "2" {
		t.Fatalf("host Git version %q is not 2.x", host.version)
	}
	if host.minor, err = strconv.Atoi(fields[1]); err != nil {
		t.Fatalf("host Git version %q: %v", host.version, err)
	}
	for _, line := range lines[1:] {
		host.rust = host.rust || strings.TrimSpace(line) == "rust: enabled"
	}
	return host
}

// TestGitRootRepositoryExtensionsContract asserts the Git 2.55 contract
// without running Git.
func TestGitRootRepositoryExtensionsContract(t *testing.T) {
	for _, c := range extensionCases {
		t.Run(c.name, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			tree := filepath.Join(base, "tree")
			makeRepo(t, tree)
			writeFile(t, filepath.Join(tree, ".git", "config"), c.config)
			folder := mkdir(t, filepath.Join(tree, "app"))
			t.Setenv("GIT_CEILING_DIRECTORIES", base)
			source, target, workspaceFolder := folder, "/workspaces/app", "/workspaces/app"
			if c.ok {
				source, target, workspaceFolder = tree, "/workspaces/tree", "/workspaces/tree/app"
			}
			got, err := GitRoot(folder)
			if err != nil || (got != "") != c.ok || c.ok && got != tree {
				t.Fatalf("GitRoot = %q, %v; ok %v", got, err, c.ok)
			}
			resolved, err := Resolve(folder, false, false, Options{MountGitRoot: true, Platform: "linux"})
			if err != nil || resolved.WorkspaceMount == nil || resolved.WorkspaceMount.Source != source ||
				resolved.WorkspaceMount.Target != target || resolved.WorkspaceFolder != workspaceFolder {
				t.Fatalf("Resolve = %+v %+v, %v; want source %q target %q folder %q", resolved, resolved.WorkspaceMount, err, source, target, workspaceFolder)
			}
		})
	}
}

// Git checks each extensions.* value as it reads the repository config, then
// rejects an extension it does not know in a version 1 repository and a
// version 1 extension in a version 0 repository. Git built without Rust also
// stops on a compatibility hash. Its probe then fails, so upstream finds no
// Git root. Native discovery must give each case's ok result, and the host Git
// the result that its version and build declare: on Git 2.55 built without
// Rust, both are the same.
func TestGitRootRepositoryExtensionsMatchGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	host := readHostGit(t, git)
	for _, route := range []string{"discovered", "gitfile", "explicit", "self", "linked", "symlinked-folder"} {
		for _, c := range extensionCases {
			t.Run(c.name+"/"+route, func(t *testing.T) {
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
				measured, stderr := probe()
				want, hostWant := "", ""
				if c.ok {
					want = control
				}
				if c.hostOK(host) {
					hostWant = control
				}
				if measured != hostWant {
					t.Fatalf("Git %s (rust %v) root = %q (%s); want %q", host.version, host.rust, measured, strings.TrimSpace(stderr), hostWant)
				}
				if hostWant != want {
					t.Logf("declared difference: Git %s (rust %v) root = %q; Git 2.55 without Rust gives %q", host.version, host.rust, measured, want)
				}
				got, err := GitRoot(folder)
				if err != nil || got != want {
					t.Fatalf("GitRoot = %q, %v; want %q", got, err, want)
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
