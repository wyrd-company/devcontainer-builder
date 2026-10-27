// relationships:
//   references: [git, git_config_include, git_global_config, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

// includeFixture is the layout that an include case writes into. common is
// the repository's common folder, which holds its config, and admin is the
// repository folder, which holds config.worktree. prefix is the prefix of
// Git's build that a gitPrefix case reads.
type includeFixture struct {
	base, folder, common, admin, prefix string
}

// includeCase writes config files that include others and returns the
// variables that select Git's system and global files; a nil value unsets
// the variable. ok is true when Git follows every include without an error,
// so the Git root is the one that the layout gives without the case.
// unknownAccount is true when the case names an account that no lookup finds:
// a build whose lookup reads /etc/passwd only skips that include, the
// approved difference, so native discovery gives the root where Git does not.
// gitPrefix is true when the case includes a file through %(prefix)/, which
// native discovery reads with Options.GitPrefix; assertGitPrefixCase asserts
// it with and without the prefix.
type includeCase struct {
	name           string
	setup          func(t *testing.T, f includeFixture) map[string]*string
	ok             bool
	unprivileged   bool
	unknownAccount bool
	gitPrefix      bool
}

// nativeOK reports whether native discovery follows every include of c
// without an error in this build.
func (c includeCase) nativeOK() bool {
	return c.ok || c.unknownAccount && gitAccountLookupPasswdOnly
}

// includeLine is a config file with one include.path entry.
func includeLine(path string) string { return "[include]\n\tpath = " + path + "\n" }

// includeGlobal writes content as GIT_CONFIG_GLOBAL in base/global, then
// each file named relative to base, and selects no other config file.
func includeGlobal(content string, files map[string]string) func(t *testing.T, f includeFixture) map[string]*string {
	return func(t *testing.T, f includeFixture) map[string]*string {
		global := filepath.Join(f.base, "global", "global.cfg")
		writeFile(t, global, content)
		for name, body := range files {
			writeFile(t, filepath.Join(f.base, name), body)
		}
		home := mkdir(t, filepath.Join(f.base, "home"))
		return map[string]*string{"GIT_CONFIG_GLOBAL": &global, "HOME": &home, "XDG_CONFIG_HOME": nil}
	}
}

// includeHome writes .gitconfig in base/home, then each file named relative
// to base, and selects HOME's default files.
func includeHome(content string, files map[string]string) func(t *testing.T, f includeFixture) map[string]*string {
	return func(t *testing.T, f includeFixture) map[string]*string {
		home := mkdir(t, filepath.Join(f.base, "home"))
		writeFile(t, filepath.Join(home, ".gitconfig"), content)
		for name, body := range files {
			writeFile(t, filepath.Join(f.base, name), body)
		}
		return map[string]*string{"GIT_CONFIG_GLOBAL": nil, "HOME": &home, "XDG_CONFIG_HOME": nil}
	}
}

// includeRepository appends content to the repository config, then writes
// each file named relative to the common folder.
func includeRepository(content string, files map[string]string) func(t *testing.T, f includeFixture) map[string]*string {
	return func(t *testing.T, f includeFixture) map[string]*string {
		config := filepath.Join(f.common, "config")
		current, err := os.ReadFile(config)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, config, string(current)+content)
		for name, body := range files {
			writeFile(t, filepath.Join(f.common, name), body)
		}
		return nil
	}
}

// includeChain writes base/global/d1.cfg to dN.cfg, each including the next.
// last, when it is not empty, is the path that dN.cfg includes. A file that
// global.cfg includes as d1.cfg is read at include depth 1, so dN.cfg is read
// at depth N.
func includeChain(n int, last string) map[string]string {
	files := map[string]string{}
	for i := 1; i <= n; i++ {
		next := fmt.Sprintf("d%d.cfg", i+1)
		if i == n {
			next = last
		}
		content := globalConfigValid
		if next != "" {
			content = includeLine(next)
		}
		files[filepath.Join("global", fmt.Sprintf("d%d.cfg", i))] = content
	}
	return files
}

var includeCases = append(append(append([]includeCase{
	{name: "global-valid", setup: includeGlobal(includeLine("inc.cfg"), map[string]string{"global/inc.cfg": globalConfigValid}), ok: true},
	{name: "global-syntax", setup: includeGlobal(includeLine("inc.cfg"), map[string]string{"global/inc.cfg": globalConfigBroken})},
	{name: "global-absolute-syntax", setup: func(t *testing.T, f includeFixture) map[string]*string {
		return includeGlobal(includeLine(filepath.Join(f.base, "inc.cfg")), map[string]string{"inc.cfg": globalConfigBroken})(t, f)
	}},
	{name: "global-anchor", setup: func(t *testing.T, f includeFixture) map[string]*string {
		// A relative path starts at the including file's folder, not at
		// the probe or working folder.
		writeFile(t, filepath.Join(f.folder, "inc.cfg"), globalConfigBroken)
		return includeGlobal(includeLine("inc.cfg"), map[string]string{"global/inc.cfg": globalConfigValid, "inc.cfg": globalConfigBroken})(t, f)
	}, ok: true},
	{name: "global-missing", setup: includeGlobal(includeLine("missing.cfg"), nil), ok: true},
	{name: "global-not-a-directory", setup: includeGlobal(includeLine("file/inc.cfg"), map[string]string{"global/file": globalConfigBroken}), ok: true},
	{name: "global-dangling-symlink", setup: func(t *testing.T, f includeFixture) map[string]*string {
		env := includeGlobal(includeLine("link.cfg"), nil)(t, f)
		symlink(t, filepath.Join(f.base, "missing.cfg"), filepath.Join(f.base, "global", "link.cfg"))
		return env
	}, ok: true},
	{name: "global-dev-null", setup: includeGlobal(includeLine(os.DevNull), nil), ok: true},
	{name: "global-valueless", setup: includeGlobal("[include]\n\tpath\n", nil)},
	{name: "global-empty", setup: includeGlobal("[include]\n\tpath =\n", nil)},
	{name: "global-directory", setup: func(t *testing.T, f includeFixture) map[string]*string {
		mkdir(t, filepath.Join(f.base, "global", "inc.d"))
		return includeGlobal(includeLine("inc.d"), nil)(t, f)
	}},
	{name: "global-unreadable", unprivileged: true, setup: func(t *testing.T, f includeFixture) map[string]*string {
		env := includeGlobal(includeLine("inc.cfg"), map[string]string{"global/inc.cfg": globalConfigValid})(t, f)
		chmod(t, filepath.Join(f.base, "global", "inc.cfg"), 0)
		return env
	}},
	{name: "global-unsearchable-folder", unprivileged: true, setup: func(t *testing.T, f includeFixture) map[string]*string {
		env := includeGlobal(includeLine("locked/inc.cfg"), map[string]string{"global/locked/inc.cfg": globalConfigValid})(t, f)
		chmod(t, filepath.Join(f.base, "global", "locked"), 0o600)
		return env
	}},
	{name: "global-symlink-loop", setup: func(t *testing.T, f includeFixture) map[string]*string {
		env := includeGlobal(includeLine("loop.cfg"), nil)(t, f)
		loop := filepath.Join(f.base, "global", "loop.cfg")
		symlink(t, loop, loop)
		return env
	}},
	{name: "global-name-too-long", setup: includeGlobal(includeLine(strings.Repeat("n", 256)), nil)},
	{name: "global-socket", setup: func(t *testing.T, f includeFixture) map[string]*string {
		// A short folder keeps the socket path within the host's limit.
		folder, err := os.MkdirTemp("", "gis")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(folder) })
		socket := filepath.Join(folder, "inc.sock")
		listener, err := net.Listen("unix", socket)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { listener.Close() })
		return includeGlobal(includeLine(socket), nil)(t, f)
	}},
	{name: "global-nested-syntax", setup: includeGlobal(includeLine("sub/inc1.cfg"), map[string]string{
		"global/sub/inc1.cfg": includeLine("inc2.cfg"), "global/sub/inc2.cfg": globalConfigBroken, "global/inc2.cfg": globalConfigValid,
	})},
	{name: "global-nested-anchor", setup: includeGlobal(includeLine("sub/inc1.cfg"), map[string]string{
		// A nested relative path starts at the included file's folder.
		"global/sub/inc1.cfg": includeLine("inc2.cfg"), "global/sub/inc2.cfg": globalConfigValid, "global/inc2.cfg": globalConfigBroken,
	}), ok: true},
	{name: "global-later-syntax", setup: includeGlobal(includeLine("inc.cfg")+globalConfigBroken, map[string]string{"global/inc.cfg": globalConfigValid})},
	{name: "global-depth-10", setup: includeGlobal(includeLine("d1.cfg"), includeChain(10, "")), ok: true},
	{name: "global-depth-11", setup: includeGlobal(includeLine("d1.cfg"), includeChain(11, ""))},
	{name: "global-depth-11-missing", setup: includeGlobal(includeLine("d1.cfg"), includeChain(10, "missing.cfg")), ok: true},
	{name: "global-cycle", setup: includeGlobal(includeLine("global.cfg"), nil)},
	{name: "global-sequential", setup: includeGlobal(strings.Repeat(includeLine("inc.cfg"), 12), map[string]string{"global/inc.cfg": globalConfigValid}), ok: true},
	{name: "global-case", setup: includeGlobal("[INCLUDE]\n\tPATH = inc.cfg\n", map[string]string{"global/inc.cfg": globalConfigBroken})},
	{name: "global-quoted", setup: includeGlobal("[include]\n\tpath = \"inc file.cfg\" ; comment\n", map[string]string{"global/inc file.cfg": globalConfigBroken})},
	{name: "global-subsection", setup: includeGlobal("[include \"x\"]\n\tpath = inc.cfg\n", map[string]string{"global/inc.cfg": globalConfigBroken}), ok: true},
	{name: "global-includeif-false", setup: includeGlobal("[includeIf \"gitdir:/nonexistent/\"]\n\tpath = inc.cfg\n", map[string]string{"global/inc.cfg": globalConfigBroken}), ok: true},
	{name: "global-tilde", setup: includeGlobal(includeLine("~/inc.cfg"), map[string]string{"home/inc.cfg": globalConfigBroken})},
	{name: "global-tilde-valid", setup: includeGlobal(includeLine("~/inc.cfg"), map[string]string{"home/inc.cfg": globalConfigValid, "global/~/inc.cfg": globalConfigBroken}), ok: true},
	{name: "global-tilde-alone", setup: includeGlobal(includeLine("~"), nil)},
	{name: "global-tilde-home-unset", setup: func(t *testing.T, f includeFixture) map[string]*string {
		env := includeGlobal(includeLine("~/inc.cfg"), nil)(t, f)
		env["HOME"] = nil
		return env
	}},
	{name: "global-tilde-home-relative", setup: func(t *testing.T, f includeFixture) map[string]*string {
		// An expanded path that is still relative starts at the including
		// file's folder.
		env := includeGlobal(includeLine("~/inc.cfg"), map[string]string{"global/rel/inc.cfg": globalConfigBroken})(t, f)
		env["HOME"] = globalValue("rel")
		return env
	}},
	{name: "global-tilde-unknown-user", setup: includeGlobal(includeLine("~no-such-user-for-dcb-tests/inc.cfg"), nil), unknownAccount: true},
	{name: "global-tilde-user", setup: func(t *testing.T, f includeFixture) map[string]*string {
		account, err := user.Current()
		if err != nil {
			t.Skipf("no account entry: %v", err)
		}
		return includeGlobal(includeLine("~"+account.Username+"/no-such-folder-for-dcb-tests/inc.cfg"), nil)(t, f)
	}, ok: true},
	{name: "global-prefix", setup: includeGlobal(includeLine("%(prefix)/no-such-folder-for-dcb-tests/inc.cfg"), map[string]string{
		// Git reads the path below its build prefix, not below the
		// including file's folder.
		"global/%(prefix)/no-such-folder-for-dcb-tests/inc.cfg": globalConfigBroken,
	}), ok: true},
	{name: "global-prefix-relative-anchor", setup: includeGlobal(includeLine("%(prefix)/no-such-folder-for-dcb-tests/inc.cfg"), map[string]string{
		// The rest of a %(prefix)/ path does not start at the including
		// file's folder either.
		"global/no-such-folder-for-dcb-tests/inc.cfg": globalConfigBroken,
	}), ok: true},
	{name: "global-prefix-absolute-valid", setup: func(t *testing.T, f includeFixture) map[string]*string {
		// Git keeps an absolute rest as it is, whatever its build prefix.
		return includeGlobal(includeLine("%(prefix)/"+filepath.Join(f.base, "inc.cfg")), map[string]string{"inc.cfg": globalConfigValid})(t, f)
	}, ok: true},
	{name: "global-prefix-absolute-syntax", setup: func(t *testing.T, f includeFixture) map[string]*string {
		return includeGlobal(includeLine("%(prefix)/"+filepath.Join(f.base, "inc.cfg")), map[string]string{"inc.cfg": globalConfigBroken})(t, f)
	}},
	{name: "global-prefix-absolute-missing", setup: func(t *testing.T, f includeFixture) map[string]*string {
		return includeGlobal(includeLine("%(prefix)/"+filepath.Join(f.base, "missing.cfg")), nil)(t, f)
	}, ok: true},
	{name: "global-prefix-absolute-nested-syntax", setup: func(t *testing.T, f includeFixture) map[string]*string {
		// A relative include in the file starts at that file's folder.
		return includeGlobal(includeLine("%(prefix)/"+filepath.Join(f.base, "sub", "inc1.cfg")), map[string]string{
			"sub/inc1.cfg": includeLine("inc2.cfg"), "sub/inc2.cfg": globalConfigBroken, "global/inc2.cfg": globalConfigValid,
		})(t, f)
	}},
	{name: "repository-prefix-absolute-syntax", setup: func(t *testing.T, f includeFixture) map[string]*string {
		writeFile(t, filepath.Join(f.base, "inc.cfg"), globalConfigBroken)
		return includeRepository(includeLine("%(prefix)/"+filepath.Join(f.base, "inc.cfg")), nil)(t, f)
	}},
	{name: "system-syntax", setup: func(t *testing.T, f includeFixture) map[string]*string {
		system := filepath.Join(f.base, "system", "system.cfg")
		writeFile(t, system, includeLine("inc.cfg"))
		writeFile(t, filepath.Join(f.base, "system", "inc.cfg"), globalConfigBroken)
		return map[string]*string{"GIT_CONFIG_NOSYSTEM": nil, "GIT_CONFIG_SYSTEM": &system}
	}},
	{name: "xdg-syntax", setup: func(t *testing.T, f includeFixture) map[string]*string {
		xdg := filepath.Join(f.base, "xdg")
		writeFile(t, filepath.Join(xdg, "git", "config"), includeLine("inc.cfg"))
		writeFile(t, filepath.Join(xdg, "git", "inc.cfg"), globalConfigBroken)
		home := mkdir(t, filepath.Join(f.base, "home"))
		return map[string]*string{"GIT_CONFIG_GLOBAL": nil, "HOME": &home, "XDG_CONFIG_HOME": &xdg}
	}},
	{name: "home-syntax", setup: includeHome(includeLine("inc.cfg"), map[string]string{"home/inc.cfg": globalConfigBroken})},
	{name: "home-valid", setup: includeHome(includeLine("inc.cfg"), map[string]string{"home/inc.cfg": globalConfigValid}), ok: true},
	{name: "repository-valid", setup: includeRepository(includeLine("inc.cfg"), map[string]string{"inc.cfg": globalConfigValid}), ok: true},
	{name: "repository-syntax", setup: includeRepository(includeLine("inc.cfg"), map[string]string{"inc.cfg": globalConfigBroken})},
	{name: "repository-anchor", setup: func(t *testing.T, f includeFixture) map[string]*string {
		writeFile(t, filepath.Join(f.folder, "inc.cfg"), globalConfigBroken)
		return includeRepository(includeLine("inc.cfg"), map[string]string{"inc.cfg": globalConfigValid})(t, f)
	}, ok: true},
	{name: "repository-parent-syntax", setup: func(t *testing.T, f includeFixture) map[string]*string {
		writeFile(t, filepath.Join(filepath.Dir(f.common), "repository-parent.cfg"), globalConfigBroken)
		return includeRepository(includeLine("../repository-parent.cfg"), nil)(t, f)
	}},
	{name: "repository-missing", setup: includeRepository(includeLine("missing.cfg"), nil), ok: true},
	{name: "repository-cycle", setup: includeRepository(includeLine("config"), nil)},
	{name: "repository-unreadable", unprivileged: true, setup: func(t *testing.T, f includeFixture) map[string]*string {
		env := includeRepository(includeLine("inc.cfg"), map[string]string{"inc.cfg": globalConfigValid})(t, f)
		chmod(t, filepath.Join(f.common, "inc.cfg"), 0)
		return env
	}},
	{name: "repository-unversioned-syntax", setup: func(t *testing.T, f includeFixture) map[string]*string {
		// Git's full read reads the repository config without a format
		// version too.
		writeFile(t, filepath.Join(f.common, "config"), includeLine("inc.cfg"))
		writeFile(t, filepath.Join(f.common, "inc.cfg"), globalConfigBroken)
		return nil
	}},
	{name: "worktree-config-syntax", setup: func(t *testing.T, f includeFixture) map[string]*string {
		writeFile(t, filepath.Join(f.admin, "config.worktree"), includeLine("inc.cfg"))
		writeFile(t, filepath.Join(f.admin, "inc.cfg"), globalConfigBroken)
		return includeRepository("[extensions]\n\tworktreeConfig = true\n", nil)(t, f)
	}},
	{name: "worktree-config-valid", setup: func(t *testing.T, f includeFixture) map[string]*string {
		writeFile(t, filepath.Join(f.admin, "config.worktree"), includeLine("inc.cfg"))
		writeFile(t, filepath.Join(f.admin, "inc.cfg"), globalConfigValid)
		return includeRepository("[extensions]\n\tworktreeConfig = true\n", nil)(t, f)
	}, ok: true},
	{name: "worktree-config-without-extension", setup: func(t *testing.T, f includeFixture) map[string]*string {
		writeFile(t, filepath.Join(f.admin, "config.worktree"), includeLine("inc.cfg"))
		writeFile(t, filepath.Join(f.admin, "inc.cfg"), globalConfigBroken)
		return nil
	}, ok: true},
	{name: "worktree-config-unversioned", setup: func(t *testing.T, f includeFixture) map[string]*string {
		// Without a format version, Git ignores extensions.worktreeConfig.
		writeFile(t, filepath.Join(f.common, "config"), "[extensions]\n\tworktreeConfig = true\n")
		writeFile(t, filepath.Join(f.admin, "config.worktree"), includeLine("inc.cfg"))
		writeFile(t, filepath.Join(f.admin, "inc.cfg"), globalConfigBroken)
		return nil
	}, ok: true},
}, prefixIncludeCases...), commandConfigCases...), append(append(append(ignoreCaseCases, fileModeCases...), symlinksCases...), append(trustctimeCases, append(quotepathCases, append(ignorestatCases, append(sparsecheckoutCases, append(sparsecheckoutconeCases, append(protecthfsCases, append(protectntfsCases, append(precomposeunicodeCases, fsyncobjectfilesCases...)...)...)...)...)...)...)...)...)...)

// includeCaseVars selects no system or global file, so that only the case
// selects one.
func includeCaseVars(base string, vars map[string]*string) map[string]*string {
	all := map[string]*string{
		"GIT_CONFIG_NOSYSTEM": globalValue("1"), "GIT_CONFIG_SYSTEM": nil,
		"GIT_CONFIG_GLOBAL": globalValue(os.DevNull), "GIT_CEILING_DIRECTORIES": &base,
		"GIT_CONFIG_COUNT": nil, "GIT_CONFIG_PARAMETERS": nil,
	}
	for i := range 4 {
		all[fmt.Sprintf("GIT_CONFIG_KEY_%d", i)] = nil
		all[fmt.Sprintf("GIT_CONFIG_VALUE_%d", i)] = nil
	}
	for name, value := range vars {
		all[name] = value
	}
	return all
}

// assertIncludeResolve asserts that Resolve mounts want from folder, or folder
// when want is "".
func assertIncludeResolve(t *testing.T, folder, want string) {
	t.Helper()
	assertIncludeResolveOptions(t, folder, want, Options{})
}

// assertIncludeResolveOptions asserts that Resolve with opts' GitPrefix
// mounts want from folder, or folder when want is "".
func assertIncludeResolveOptions(t *testing.T, folder, want string, opts Options) {
	t.Helper()
	source := want
	if source == "" {
		source = folder
	}
	rel, err := filepath.Rel(source, folder)
	if err != nil {
		t.Fatal(err)
	}
	target := path.Join("/workspaces", filepath.Base(source))
	resolved, err := Resolve(folder, false, false, Options{MountGitRoot: true, Platform: "linux", GitPrefix: opts.GitPrefix})
	if err != nil || resolved.WorkspaceMount == nil || resolved.WorkspaceMount.Source != source ||
		resolved.WorkspaceMount.Target != target || resolved.WorkspaceFolder != path.Join(target, filepath.ToSlash(rel)) {
		t.Fatalf("Resolve = %+v %+v, %v; want source %q target %q", resolved, resolved.WorkspaceMount, err, source, target)
	}
}

// TestGitRootConfigIncludeContract asserts the include rules without running
// Git, from a caller folder inside a discovered repository.
func TestGitRootConfigIncludeContract(t *testing.T) {
	for _, c := range includeCases {
		t.Run(c.name, func(t *testing.T) {
			if c.unprivileged && os.Geteuid() == 0 {
				t.Skip("root bypasses the permission the case denies")
			}
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			tree := filepath.Join(base, "tree")
			makeRepo(t, tree)
			folder := mkdir(t, filepath.Join(tree, "app"))
			admin := filepath.Join(tree, ".git")
			// makeRepo writes no config; Git's own repositories have one.
			writeFile(t, filepath.Join(admin, "config"), "[core]\n\trepositoryformatversion = 0\n")
			f := includeFixture{base: base, folder: folder, common: admin, admin: admin, prefix: mkdir(t, filepath.Join(base, "prefix", "git"))}
			applyGlobalEnv(t, nil, includeCaseVars(base, c.setup(t, f)))
			want := ""
			if c.nativeOK() {
				want = tree
			}
			if c.gitPrefix {
				assertGitPrefixCase(t, c, f, folder, tree, want)
				return
			}
			if got, err := GitRoot(folder); err != nil || got != want {
				t.Fatalf("GitRoot = %q, %v; want %q", got, err, want)
			}
			assertIncludeResolve(t, folder, want)
		})
	}
}

// Git follows include.path entries in each config file that it reads, when it
// starts and in its full read after setup, which also reads the repository
// config files. An include that it rejects stops the probe, and upstream finds
// no Git root. Native discovery and the host Git must both give each case's
// result on every discovery route, except where an unknown account is the
// approved difference of a build whose lookup reads /etc/passwd only.
func TestGitRootConfigIncludeMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	routes := []string{"discovered", "gitfile", "explicit", "explicit-relative", "self", "linked", "common-relative", "symlinked-folder"}
	prefix := hostGitPrefix(t, git)
	for _, route := range routes {
		for _, c := range includeCases {
			t.Run(c.name+"/"+route, func(t *testing.T) {
				if c.unprivileged && os.Geteuid() == 0 {
					t.Skip("root bypasses the permission the case denies")
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
				common := admin
				var vars map[string]*string
				set := func(key, value string) {
					if vars == nil {
						vars = map[string]*string{}
					}
					vars[key] = &value
				}
				folder := filepath.Join(tree, "app", "deep")
				switch route {
				case "gitfile":
					moved := filepath.Join(base, "admin")
					if err := os.Rename(admin, moved); err != nil {
						t.Fatal(err)
					}
					writeFile(t, admin, "gitdir: "+moved+"\n")
					admin, common = moved, moved
				case "explicit":
					set("GIT_DIR", admin)
				case "explicit-relative":
					set("GIT_DIR", "../../.git")
				case "self":
					folder = admin
				case "linked":
					run(tree, "commit", "-q", "--allow-empty", "-m", "initial")
					run(tree, "worktree", "add", "-q", filepath.Join(base, "linked"))
					admin = filepath.Join(common, "worktrees", "linked")
					folder = filepath.Join(base, "linked", "app")
				case "common-relative":
					// Git's discovery starts a relative GIT_COMMON_DIR at the
					// probe cwd, where w/c names the common folder. Its format
					// check and full read start it at the folder where Git
					// found the gitfile, where it names c, which holds the
					// config that Git reads. The other config is broken.
					run(tree, "commit", "-q", "--allow-empty", "-m", "initial")
					run(tree, "worktree", "add", "-q", filepath.Join(base, "w", "linked"))
					admin = filepath.Join(common, "worktrees", "linked")
					folder = filepath.Join(base, "w", "linked", "app")
					symlink(t, common, filepath.Join(base, "w", "c"))
					config, err := os.ReadFile(filepath.Join(common, "config"))
					if err != nil {
						t.Fatal(err)
					}
					writeFile(t, filepath.Join(base, "c", "config"), string(config))
					writeFile(t, filepath.Join(common, "config"), string(config)+globalConfigBroken)
					common = filepath.Join(base, "c")
					set("GIT_COMMON_DIR", "../../c")
				case "symlinked-folder":
					mkdir(t, folder)
					symlink(t, tree, filepath.Join(base, "alias"))
					folder = filepath.Join(base, "alias", "app", "deep")
				}
				mkdir(t, folder)
				env = applyGlobalEnv(t, env, includeCaseVars(base, vars))
				control := globalSetupGit(t, git, folder, env)
				if control == "" {
					t.Fatal("Git finds no root before the case")
				}
				caseVars := map[string]*string{}
				f := includeFixture{base: base, folder: folder, common: common, admin: admin, prefix: prefix}
				for name, value := range c.setup(t, f) {
					caseVars[name] = value
				}
				for name, value := range vars {
					caseVars[name] = value
				}
				env = applyGlobalEnv(t, env, includeCaseVars(base, caseVars))
				if strings.HasPrefix(c.name, "command-") {
					assertCommandConfigGitExit(t, git, folder, env, c.ok)
				}
				want := ""
				if c.ok {
					want = control
				}
				if measured := globalSetupGit(t, git, folder, env); measured != want {
					t.Fatalf("Git root = %q; want %q", measured, want)
				}
				if c.gitPrefix {
					assertGitPrefixCase(t, c, f, folder, control, want)
					return
				}
				if c.nativeOK() {
					want = control
				}
				if got, err := GitRoot(folder); err != nil || got != want {
					t.Fatalf("GitRoot = %q, %v; want %q", got, err, want)
				}
				assertIncludeResolve(t, folder, want)
			})
		}
	}
}

// Git keeps a relative GIT_COMMON_DIR as given. Its format check starts it at
// the probe cwd for GIT_DIR, and its full read after setup at its working
// folder, which GIT_WORK_TREE selects. From the caller folder, w/c names the
// common folder; from the working tree, c names a folder whose config only the
// full read reads.
func TestGitRootRelativeCommonDirFullRead(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	for _, c := range []struct {
		name, config string
		ok           bool
	}{
		{name: "valid", config: includeLine("inc.cfg"), ok: true},
		{name: "syntax", config: includeLine("inc.cfg") + globalConfigBroken},
		{name: "include-broken", config: includeLine("broken.cfg")},
	} {
		t.Run(c.name, func(t *testing.T) {
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
			run(tree, "commit", "-q", "--allow-empty", "-m", "initial")
			linked := filepath.Join(base, "w", "linked")
			run(tree, "worktree", "add", "-q", linked)
			symlink(t, filepath.Join(tree, ".git"), filepath.Join(base, "w", "c"))
			writeFile(t, filepath.Join(base, "c", "config"), c.config)
			writeFile(t, filepath.Join(base, "c", "inc.cfg"), globalConfigValid)
			writeFile(t, filepath.Join(base, "c", "broken.cfg"), globalConfigBroken)
			folder := mkdir(t, filepath.Join(linked, "app"))
			env = applyGlobalEnv(t, env, includeCaseVars(base, map[string]*string{
				"GIT_DIR":        globalValue(filepath.Join(tree, ".git", "worktrees", "linked")),
				"GIT_WORK_TREE":  &linked,
				"GIT_COMMON_DIR": globalValue("../../c"),
			}))
			want := ""
			if c.ok {
				want = linked
			}
			if measured := globalSetupGit(t, git, folder, env); measured != want {
				t.Fatalf("Git root = %q; want %q", measured, want)
			}
			if got, err := GitRoot(folder); err != nil || got != want {
				t.Fatalf("GitRoot = %q, %v; want %q", got, err, want)
			}
			assertIncludeResolve(t, folder, want)
		})
	}
}
