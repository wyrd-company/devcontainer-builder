// relationships:
//   references: [git, git_global_config, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

const (
	globalConfigBroken = "[broken\n"
	globalConfigValid  = "[core]\n\tpager = less\n"
)

// globalConfigCase sets the variables that select Git's global config files.
// setup writes files under base and folder and returns the variables; a nil
// value unsets the variable. ok is true when Git admits every file, so the Git
// root is the one that the layout gives without the case.
type globalConfigCase struct {
	name         string
	setup        func(t *testing.T, base, folder string) map[string]*string
	ok           bool
	unprivileged bool
}

func globalValue(value string) *string { return &value }

// globalHome writes home under base with the given .gitconfig and
// .config/git/config contents; an empty content writes no file.
func globalHome(t *testing.T, base, gitconfig, xdgDefault string) string {
	t.Helper()
	home := mkdir(t, filepath.Join(base, "home"))
	if gitconfig != "" {
		writeFile(t, filepath.Join(home, ".gitconfig"), gitconfig)
	}
	if xdgDefault != "" {
		writeFile(t, filepath.Join(home, ".config", "git", "config"), xdgDefault)
	}
	return home
}

// globalOnly names one file as GIT_CONFIG_GLOBAL, with a broken home that Git
// must not read.
func globalOnly(path func(t *testing.T, base, folder string) string) func(t *testing.T, base, folder string) map[string]*string {
	return func(t *testing.T, base, folder string) map[string]*string {
		home := globalHome(t, base, globalConfigBroken, globalConfigBroken)
		return map[string]*string{"GIT_CONFIG_GLOBAL": globalValue(path(t, base, folder)), "HOME": &home, "XDG_CONFIG_HOME": nil}
	}
}

// globalFile writes content to a file under base and names it.
func globalFile(content string) func(t *testing.T, base, folder string) string {
	return func(t *testing.T, base, _ string) string {
		file := filepath.Join(base, "global.cfg")
		writeFile(t, file, content)
		return file
	}
}

// homeOnly selects the default files from HOME and XDG_CONFIG_HOME.
func homeOnly(gitconfig, xdgDefault string, xdg *string) func(t *testing.T, base, folder string) map[string]*string {
	return func(t *testing.T, base, _ string) map[string]*string {
		home := globalHome(t, base, gitconfig, xdgDefault)
		return map[string]*string{"GIT_CONFIG_GLOBAL": nil, "HOME": &home, "XDG_CONFIG_HOME": xdg}
	}
}

// xdgFolder writes git/config under base/xdg and selects it.
func xdgFolder(content, gitconfig, xdgDefault string) func(t *testing.T, base, folder string) map[string]*string {
	return func(t *testing.T, base, _ string) map[string]*string {
		home := globalHome(t, base, gitconfig, xdgDefault)
		xdg := filepath.Join(base, "xdg")
		writeFile(t, filepath.Join(xdg, "git", "config"), content)
		return map[string]*string{"GIT_CONFIG_GLOBAL": nil, "HOME": &home, "XDG_CONFIG_HOME": &xdg}
	}
}

var globalConfigCases = []globalConfigCase{
	{name: "global-valid", setup: globalOnly(globalFile(globalConfigValid)), ok: true},
	{name: "global-syntax", setup: globalOnly(globalFile(globalConfigBroken))},
	{name: "global-bom", setup: globalOnly(globalFile("\xef\xbb\xbf" + globalConfigValid)), ok: true},
	{name: "global-partial-bom", setup: globalOnly(globalFile("\xef\xbb" + globalConfigValid))},
	{name: "global-crlf", setup: globalOnly(globalFile("[core]\r\n\tpager = less\r\n")), ok: true},
	{name: "global-missing-include", setup: globalOnly(func(t *testing.T, base, folder string) string {
		return globalFile("[include]\n\tpath = "+filepath.Join(base, "missing.cfg")+"\n")(t, base, folder)
	}), ok: true},
	{name: "global-missing", setup: globalOnly(func(_ *testing.T, base, _ string) string { return filepath.Join(base, "missing.cfg") }), ok: true},
	{name: "global-empty", setup: globalOnly(func(*testing.T, string, string) string { return "" }), ok: true},
	{name: "global-dev-null", setup: globalOnly(func(*testing.T, string, string) string { return os.DevNull }), ok: true},
	{name: "global-dev-zero", setup: globalOnly(func(*testing.T, string, string) string { return "/dev/zero" })},
	{name: "global-directory", setup: globalOnly(func(t *testing.T, base, _ string) string { return mkdir(t, filepath.Join(base, "global.d")) })},
	{name: "global-socket", setup: globalOnly(func(t *testing.T, _, _ string) string {
		// A short folder keeps the socket path within the host's limit.
		folder, err := os.MkdirTemp("", "gfr")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(folder) })
		socket := filepath.Join(folder, "global.sock")
		listener, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { listener.Close() })
		return socket
	})},
	{name: "global-large-regular", setup: globalOnly(globalFile(strings.Repeat("# "+strings.Repeat("x", 97)+"\n", 11000) + globalConfigValid)), ok: true},
	{name: "global-dangling-symlink", setup: globalOnly(func(t *testing.T, base, _ string) string {
		link := filepath.Join(base, "global.cfg")
		symlink(t, filepath.Join(base, "missing.cfg"), link)
		return link
	}), ok: true},
	{name: "global-symlink-loop", setup: globalOnly(func(t *testing.T, base, _ string) string {
		link := filepath.Join(base, "global.cfg")
		symlink(t, link, link)
		return link
	})},
	{name: "global-not-a-directory", setup: globalOnly(func(t *testing.T, base, _ string) string {
		writeFile(t, filepath.Join(base, "file"), globalConfigBroken)
		return filepath.Join(base, "file", "global.cfg")
	}), ok: true},
	{name: "global-name-too-long", setup: globalOnly(func(_ *testing.T, base, _ string) string {
		return filepath.Join(base, strings.Repeat("n", 256))
	})},
	{name: "global-unreadable", unprivileged: true, setup: globalOnly(func(t *testing.T, base, folder string) string {
		file := globalFile(globalConfigBroken)(t, base, folder)
		chmod(t, file, 0)
		return file
	}), ok: true},
	{name: "global-unsearchable-folder", unprivileged: true, setup: globalOnly(func(t *testing.T, base, _ string) string {
		locked := mkdir(t, filepath.Join(base, "locked"))
		writeFile(t, filepath.Join(locked, "global.cfg"), globalConfigBroken)
		chmod(t, locked, 0o600)
		return filepath.Join(locked, "global.cfg")
	}), ok: true},
	{name: "global-relative-syntax", setup: globalOnly(func(t *testing.T, _ string, folder string) string {
		writeFile(t, filepath.Join(folder, "global.cfg"), globalConfigBroken)
		return "global.cfg"
	})},
	{name: "global-overrides-xdg", setup: func(t *testing.T, base, folder string) map[string]*string {
		env := xdgFolder(globalConfigBroken, globalConfigBroken, globalConfigBroken)(t, base, folder)
		env["GIT_CONFIG_GLOBAL"] = globalValue(globalFile(globalConfigValid)(t, base, folder))
		return env
	}, ok: true},
	{name: "home-valid", setup: homeOnly(globalConfigValid, globalConfigValid, nil), ok: true},
	{name: "home-gitconfig-syntax", setup: homeOnly(globalConfigBroken, "", nil)},
	{name: "home-xdg-default-syntax", setup: homeOnly("", globalConfigBroken, nil)},
	{name: "home-xdg-empty-default-syntax", setup: homeOnly("", globalConfigBroken, globalValue(""))},
	{name: "home-relative-syntax", setup: func(t *testing.T, _ string, folder string) map[string]*string {
		writeFile(t, filepath.Join(folder, "home", ".gitconfig"), globalConfigBroken)
		return map[string]*string{"GIT_CONFIG_GLOBAL": nil, "HOME": globalValue("home"), "XDG_CONFIG_HOME": nil}
	}},
	{name: "home-unset", setup: func(*testing.T, string, string) map[string]*string {
		return map[string]*string{"GIT_CONFIG_GLOBAL": nil, "HOME": nil, "XDG_CONFIG_HOME": nil}
	}, ok: true},
	{name: "home-unset-xdg-syntax", setup: func(t *testing.T, base, folder string) map[string]*string {
		env := xdgFolder(globalConfigBroken, "", "")(t, base, folder)
		env["HOME"] = nil
		return env
	}},
	{name: "xdg-syntax", setup: xdgFolder(globalConfigBroken, globalConfigValid, "")},
	{name: "xdg-replaces-home-default", setup: xdgFolder(globalConfigValid, globalConfigValid, globalConfigBroken), ok: true},
	{name: "xdg-valid-gitconfig-syntax", setup: xdgFolder(globalConfigValid, globalConfigBroken, "")},
}

// applyGlobalEnv sets the case variables in the process for native discovery
// and returns them applied to env for Git.
func applyGlobalEnv(t *testing.T, env []string, vars map[string]*string) []string {
	t.Helper()
	var out []string
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		if _, replaced := vars[name]; !replaced {
			out = append(out, entry)
		}
	}
	for name, value := range vars {
		// Setenv records the value that the test restores.
		t.Setenv(name, "")
		if value == nil {
			os.Unsetenv(name)
			continue
		}
		os.Setenv(name, *value)
		out = append(out, name+"="+*value)
	}
	return out
}

func skipPrivileged(t *testing.T, c globalConfigCase) {
	t.Helper()
	if c.unprivileged && os.Geteuid() == 0 {
		t.Skip("root bypasses the permission the case denies")
	}
}

// TestGitRootGlobalConfigContract asserts the global config rules without
// running Git.
func TestGitRootGlobalConfigContract(t *testing.T) {
	for _, c := range globalConfigCases {
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

// Git reads its global config files when it starts, before discovery. A file
// that it rejects stops the probe, and upstream finds no Git root. Native
// discovery and the host Git must both give each case's result on every
// discovery route.
func TestGitRootGlobalConfigMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	for _, route := range []string{"discovered", "gitfile", "explicit", "self", "linked", "symlinked-folder"} {
		for _, c := range globalConfigCases {
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
				if c.ok {
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

// Git blocks reading a FIFO named as a global config file. Native discovery
// rejects it without opening it, so it does not block.
func TestGitRootGlobalConfigFIFO(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	tree := filepath.Join(base, "tree")
	makeRepo(t, tree)
	folder := mkdir(t, filepath.Join(tree, "app"))
	t.Setenv("GIT_CEILING_DIRECTORIES", base)
	fifo := filepath.Join(base, "global.cfg")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", fifo)
	if got, err := GitRoot(folder); err != nil || got != "" {
		t.Fatalf("GitRoot = %q, %v; want no root", got, err)
	}
}
