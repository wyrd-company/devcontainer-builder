// relationships:
//   references: [git, git_system_config, git_global_config_test, ../../reference/cli/src/spec-common/git]

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

// systemOnly reads GIT_CONFIG_SYSTEM from path, with GIT_CONFIG_NOSYSTEM unset
// and no global config file.
func systemOnly(path func(t *testing.T, base, folder string) string) func(t *testing.T, base, folder string) map[string]*string {
	return systemWith(nil, path)
}

// systemWith sets GIT_CONFIG_NOSYSTEM to nosystem, or unsets it when nil, and
// names path as GIT_CONFIG_SYSTEM, or unsets it when path is nil.
func systemWith(nosystem *string, path func(t *testing.T, base, folder string) string) func(t *testing.T, base, folder string) map[string]*string {
	return func(t *testing.T, base, folder string) map[string]*string {
		vars := map[string]*string{"GIT_CONFIG_NOSYSTEM": nosystem, "GIT_CONFIG_SYSTEM": nil, "GIT_CONFIG_GLOBAL": globalValue(os.DevNull)}
		if path != nil {
			vars["GIT_CONFIG_SYSTEM"] = globalValue(path(t, base, folder))
		}
		return vars
	}
}

// systemFile writes content to a file under base and names it.
func systemFile(content string) func(t *testing.T, base, folder string) string {
	return func(t *testing.T, base, _ string) string {
		file := filepath.Join(base, "system.cfg")
		writeFile(t, file, content)
		return file
	}
}

// systemLink links folder/link to a folder under base, so that link/.. names
// that folder's parent when the kernel resolves it.
func systemLink(t *testing.T, base, folder string) {
	t.Helper()
	symlink(t, mkdir(t, filepath.Join(base, "other", "sub")), filepath.Join(folder, "link"))
}

var systemConfigCases = []globalConfigCase{
	{name: "system-valid", setup: systemOnly(systemFile(globalConfigValid)), ok: true},
	{name: "system-syntax", setup: systemOnly(systemFile(globalConfigBroken))},
	{name: "system-missing", setup: systemOnly(func(_ *testing.T, base, _ string) string { return filepath.Join(base, "missing.cfg") }), ok: true},
	{name: "system-empty", setup: systemOnly(func(*testing.T, string, string) string { return "" }), ok: true},
	{name: "system-dev-null", setup: systemOnly(func(*testing.T, string, string) string { return os.DevNull }), ok: true},
	{name: "system-directory", setup: systemOnly(func(t *testing.T, base, _ string) string { return mkdir(t, filepath.Join(base, "system.d")) })},
	{name: "system-unreadable", unprivileged: true, setup: systemOnly(func(t *testing.T, base, folder string) string {
		// The first read skips the denied file; the second read stops on it.
		file := systemFile(globalConfigValid)(t, base, folder)
		chmod(t, file, 0)
		return file
	})},
	{name: "system-unsearchable-folder", unprivileged: true, setup: systemOnly(func(t *testing.T, base, _ string) string {
		locked := mkdir(t, filepath.Join(base, "locked"))
		writeFile(t, filepath.Join(locked, "system.cfg"), globalConfigValid)
		chmod(t, locked, 0o600)
		return filepath.Join(locked, "system.cfg")
	})},
	{name: "system-dangling-symlink", setup: systemOnly(func(t *testing.T, base, _ string) string {
		link := filepath.Join(base, "system.cfg")
		symlink(t, filepath.Join(base, "missing.cfg"), link)
		return link
	}), ok: true},
	{name: "system-symlink-loop", setup: systemOnly(func(t *testing.T, base, _ string) string {
		link := filepath.Join(base, "system.cfg")
		symlink(t, link, link)
		return link
	})},
	{name: "system-not-a-directory", setup: systemOnly(func(t *testing.T, base, _ string) string {
		writeFile(t, filepath.Join(base, "file"), globalConfigBroken)
		return filepath.Join(base, "file", "system.cfg")
	}), ok: true},
	{name: "system-name-too-long", setup: systemOnly(func(_ *testing.T, base, _ string) string {
		return filepath.Join(base, strings.Repeat("n", 256))
	})},
	{name: "system-relative-syntax", setup: systemOnly(func(t *testing.T, _ string, folder string) string {
		writeFile(t, filepath.Join(folder, "system.cfg"), globalConfigBroken)
		return "system.cfg"
	})},
	{name: "system-relative-directory", setup: systemOnly(func(t *testing.T, _ string, folder string) string {
		mkdir(t, filepath.Join(folder, "system.d"))
		return "system.d"
	}), ok: true, secondRead: map[string]bool{"explicit": true, "self": true}},
	{name: "system-relative-unreadable", unprivileged: true, setup: systemOnly(func(t *testing.T, _ string, folder string) string {
		file := filepath.Join(folder, "system.cfg")
		writeFile(t, file, globalConfigValid)
		chmod(t, file, 0)
		return "system.cfg"
	}), ok: true, secondRead: map[string]bool{"explicit": true, "self": true}},
	{name: "system-normalized-symlink-parent", setup: systemOnly(func(t *testing.T, base, folder string) string {
		// Git removes link/.. without reading the link, so the path names
		// a file in folder.
		systemLink(t, base, folder)
		writeFile(t, filepath.Join(folder, "system.cfg"), globalConfigBroken)
		return "link/../system.cfg"
	})},
	{name: "system-normalized-skips-link-target", setup: systemOnly(func(t *testing.T, base, folder string) string {
		systemLink(t, base, folder)
		writeFile(t, filepath.Join(base, "other", "system.cfg"), globalConfigBroken)
		return "link/../system.cfg"
	}), ok: true},
	{name: "system-normalized-missing-parent", setup: systemOnly(func(t *testing.T, _ string, folder string) string {
		writeFile(t, filepath.Join(folder, "system.cfg"), globalConfigBroken)
		return "missing/.//../system.cfg"
	})},
	{name: "system-normalized-absolute", setup: systemOnly(func(t *testing.T, base, _ string) string {
		writeFile(t, filepath.Join(base, "system.cfg"), globalConfigBroken)
		return base + "//missing/./../system.cfg"
	})},
	{name: "system-normalized-empty", setup: systemOnly(func(t *testing.T, _ string, folder string) string {
		// sub/.. normalizes to an empty path, which Git skips; read
		// without normalization, it names folder, which Git cannot open.
		mkdir(t, filepath.Join(folder, "sub"))
		return "sub/.."
	}), ok: true},
	{name: "system-normalize-failure-in-place", setup: systemOnly(func(t *testing.T, _ string, folder string) string {
		// The .. past the start stops normalization after a/./x/ was
		// rewritten to a/x/x/, so Git reads folder/a/x/x/../../../s.
		mkdir(t, filepath.Join(folder, "a", "x", "x"))
		writeFile(t, filepath.Join(folder, "s"), globalConfigBroken)
		return "a/./x/../../../s"
	})},
	{name: "system-normalize-failure-skips-original", setup: systemOnly(func(t *testing.T, _ string, folder string) string {
		// Without folder/a/x/x, the rewritten path is missing; the original
		// path names a broken file beside folder.
		mkdir(t, filepath.Join(folder, "a", "x"))
		writeFile(t, filepath.Join(filepath.Dir(folder), "s"), globalConfigBroken)
		return "a/./x/../../../s"
	}), ok: true},
	{name: "system-normalize-failure-at-root", setup: systemOnly(func(t *testing.T, base, _ string) string {
		writeFile(t, filepath.Join(base, "system.cfg"), globalConfigBroken)
		return "/.." + base + "/system.cfg"
	})},
	{name: "system-valid-global-syntax", setup: func(t *testing.T, base, folder string) map[string]*string {
		vars := systemOnly(systemFile(globalConfigValid))(t, base, folder)
		vars["GIT_CONFIG_GLOBAL"] = globalValue(globalFile(globalConfigBroken)(t, base, folder))
		return vars
	}},
	{name: "nosystem-true", setup: systemWith(globalValue("true"), systemFile(globalConfigBroken)), ok: true},
	{name: "nosystem-number", setup: systemWith(globalValue(" 2"), systemFile(globalConfigBroken)), ok: true},
	{name: "nosystem-true-default", setup: systemWith(globalValue("yes"), nil), ok: true},
	{name: "nosystem-false", setup: systemWith(globalValue("off"), systemFile(globalConfigBroken))},
	{name: "nosystem-empty", setup: systemWith(globalValue(""), systemFile(globalConfigBroken))},
	{name: "nosystem-invalid", setup: systemWith(globalValue("maybe"), systemFile(globalConfigValid))},
	{name: "nosystem-out-of-range", setup: systemWith(globalValue("4294967296"), systemFile(globalConfigValid))},
}

// TestGitRootSystemConfigContract asserts the system config rules without
// running Git.
func TestGitRootSystemConfigContract(t *testing.T) {
	for _, c := range systemConfigCases {
		t.Run(c.name, func(t *testing.T) {
			skipPrivileged(t, c)
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			tree := filepath.Join(base, "tree")
			makeRepo(t, tree)
			folder := mkdir(t, filepath.Join(tree, "app"))
			t.Setenv("GIT_CEILING_DIRECTORIES", base)
			applyGlobalEnv(t, nil, c.setup(t, base, folder))
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

// Git reads the file that GIT_CONFIG_SYSTEM names, unless GIT_CONFIG_NOSYSTEM
// is true, when it starts and again from its working folder after setup. A
// file or value that it rejects stops the probe, and upstream finds no Git
// root. Native discovery and the host Git must both give each case's result on
// every discovery route.
func TestGitRootSystemConfigMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	for _, route := range []string{"discovered", "gitfile", "explicit", "self", "linked", "symlinked-folder"} {
		for _, c := range systemConfigCases {
			t.Run(c.name+"/"+route, func(t *testing.T) {
				skipPrivileged(t, c)
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
				probe := func(env []string) (string, string) {
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
				control, stderr := probe(env)
				if control == "" {
					t.Fatalf("Git finds no root before the case: %s", stderr)
				}
				env = applyGlobalEnv(t, env, c.setup(t, base, folder))
				want := ""
				if c.ok && !c.secondRead[route] {
					want = control
				}
				if measured, stderr := probe(env); measured != want {
					t.Fatalf("Git root = %q (%s); want %q", measured, strings.TrimSpace(stderr), want)
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

// TestGitNormalizePath pins Git's lexical normalization, including the partly
// rewritten path that Git keeps when a .. component has no component before
// it. The in-place result is the path that Git 2.55 names in its error for
// a/./x/../../../s.
func TestGitNormalizePath(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"/", "/"},
		{"//", "/"},
		{"a", "a"},
		{"a/", "a/"},
		{"a//b", "a/b"},
		{"./a", "a"},
		{"a/.", "a/"},
		{"a/./b", "a/b"},
		{"a/..", ""},
		{"a/../b", "b"},
		{"link/../system.cfg", "system.cfg"},
		{"/a/b/../c", "/a/c"},
		{"/a/b/..", "/a/"},
		{"..a/.b/...", "..a/.b/..."},
		{"..", ".."},
		{"../a", "../a"},
		{"/..", "/.."},
		{"/../a", "/../a"},
		{"a/../..", "a/../.."},
		{"a/./x/../../../s", "a/x/x/../../../s"},
		{"a//b/../../../c", "a/b//../../../c"},
	} {
		if got := gitNormalizePath(c.in); got != c.want {
			t.Errorf("gitNormalizePath(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
