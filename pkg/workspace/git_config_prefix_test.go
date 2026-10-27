// relationships:
//   references: [git, git_config_include, workspace, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

func TestGitIncludePathPrefix(t *testing.T) {
	for _, c := range []struct {
		name, value, prefix, path string
		follow                    bool
	}{
		{name: "unset", value: "%(prefix)/etc/inc.cfg"},
		{name: "joined", value: "%(prefix)/etc/inc.cfg", prefix: "/opt/git", path: "/opt/git/etc/inc.cfg", follow: true},
		// Git joins with a separator and does not clean the result.
		{name: "trailing-separator", value: "%(prefix)/etc/inc.cfg", prefix: "/opt/git/", path: "/opt/git//etc/inc.cfg", follow: true},
		{name: "parent-components", value: "%(prefix)/../etc/./inc.cfg", prefix: "/opt/git", path: "/opt/git/../etc/./inc.cfg", follow: true},
		{name: "root", value: "%(prefix)/etc/inc.cfg", prefix: "/", path: "//etc/inc.cfg", follow: true},
		{name: "relative-prefix", value: "%(prefix)/etc/inc.cfg", prefix: "git", path: "git/etc/inc.cfg", follow: true},
		{name: "absolute-rest-unset", value: "%(prefix)//etc/inc.cfg", path: "/etc/inc.cfg", follow: true},
		{name: "absolute-rest-set", value: "%(prefix)//etc/inc.cfg", prefix: "/opt/git", path: "/etc/inc.cfg", follow: true},
		// Only the exact %(prefix)/ form names the prefix.
		{name: "no-separator", value: "%(prefix)etc/inc.cfg", prefix: "/opt/git", path: "%(prefix)etc/inc.cfg", follow: true},
	} {
		t.Run(c.name, func(t *testing.T) {
			path, follow, ok := gitIncludePath(c.value, c.prefix)
			if path != c.path || follow != c.follow || !ok {
				t.Fatalf("gitIncludePath(%q, %q) = %q, %v, %v; want %q, %v, true", c.value, c.prefix, path, follow, ok, c.path, c.follow)
			}
		})
	}
}

// prefixRest is the rest of a %(prefix)/ include path that names target, an
// absolute path, below prefix: one .. for each component of prefix, then
// target. Only the prefix with that number of components names target, so a
// case with this rest checks the prefix that it is read with.
func prefixRest(prefix, target string) string {
	depth := len(strings.FieldsFunc(prefix, func(r rune) bool { return r == '/' }))
	return strings.Repeat("../", depth) + strings.TrimPrefix(target, "/")
}

// prefixLine is a config file that includes target through f.prefix.
func prefixLine(f includeFixture, target string) string {
	return includeLine("%(prefix)/" + prefixRest(f.prefix, target))
}

// prefixTarget writes body as base/prefixed/inc.cfg, and broken.cfg beside
// it, and returns the path of inc.cfg.
func prefixTarget(t *testing.T, f includeFixture, body string) string {
	t.Helper()
	target := filepath.Join(f.base, "prefixed", "inc.cfg")
	if body != "" {
		writeFile(t, target, body)
	}
	writeFile(t, filepath.Join(f.base, "prefixed", "broken.cfg"), globalConfigBroken)
	return target
}

// prefixGlobal includes the prefixed target from GIT_CONFIG_GLOBAL.
func prefixGlobal(body string) func(t *testing.T, f includeFixture) map[string]*string {
	return func(t *testing.T, f includeFixture) map[string]*string {
		return includeGlobal(prefixLine(f, prefixTarget(t, f, body)), nil)(t, f)
	}
}

// prefixIncludeCases include a file through %(prefix)/ with a relative rest.
// Git reads it below its build prefix, which the cases read as f.prefix.
var prefixIncludeCases = []includeCase{
	{name: "prefix-relative-syntax", setup: prefixGlobal(globalConfigBroken), gitPrefix: true},
	{name: "prefix-relative-valid", setup: prefixGlobal(globalConfigValid), ok: true, gitPrefix: true},
	{name: "prefix-relative-missing", setup: prefixGlobal(""), ok: true, gitPrefix: true},
	// A relative include in the prefixed file starts at its folder.
	{name: "prefix-relative-nested-syntax", setup: prefixGlobal(includeLine("broken.cfg")), gitPrefix: true},
	{name: "prefix-relative-home-syntax", setup: func(t *testing.T, f includeFixture) map[string]*string {
		return includeHome(prefixLine(f, prefixTarget(t, f, globalConfigBroken)), nil)(t, f)
	}, gitPrefix: true},
	{name: "prefix-relative-system-syntax", setup: func(t *testing.T, f includeFixture) map[string]*string {
		system := filepath.Join(f.base, "system", "system.cfg")
		writeFile(t, system, prefixLine(f, prefixTarget(t, f, globalConfigBroken)))
		return map[string]*string{"GIT_CONFIG_NOSYSTEM": nil, "GIT_CONFIG_SYSTEM": &system}
	}, gitPrefix: true},
	{name: "prefix-relative-included-syntax", setup: func(t *testing.T, f includeFixture) map[string]*string {
		// The prefixed include is in a file that the global file includes.
		line := prefixLine(f, prefixTarget(t, f, globalConfigBroken))
		return includeGlobal(includeLine("next.cfg"), map[string]string{"global/next.cfg": line})(t, f)
	}, gitPrefix: true},
	{name: "prefix-relative-repository-syntax", setup: func(t *testing.T, f includeFixture) map[string]*string {
		return includeRepository(prefixLine(f, prefixTarget(t, f, globalConfigBroken)), nil)(t, f)
	}, gitPrefix: true},
	{name: "prefix-relative-repository-valid", setup: func(t *testing.T, f includeFixture) map[string]*string {
		return includeRepository(prefixLine(f, prefixTarget(t, f, globalConfigValid)), nil)(t, f)
	}, ok: true, gitPrefix: true},
	{name: "prefix-relative-worktree-config-syntax", setup: func(t *testing.T, f includeFixture) map[string]*string {
		writeFile(t, filepath.Join(f.admin, "config.worktree"), prefixLine(f, prefixTarget(t, f, globalConfigBroken)))
		return includeRepository("[extensions]\n\tworktreeConfig = true\n", nil)(t, f)
	}, gitPrefix: true},
	{name: "prefix-absolute-syntax-with-prefix", setup: func(t *testing.T, f includeFixture) map[string]*string {
		// An absolute rest does not use the prefix.
		target := prefixTarget(t, f, globalConfigBroken)
		return includeGlobal(includeLine("%(prefix)/"+target), nil)(t, f)
	}, gitPrefix: true},
}

// assertGitPrefixCase asserts the Git root and the Resolve mount for a
// gitPrefix case, from folder, with f.prefix and without a prefix. control
// is the root that the layout gives without the case. Read with f.prefix,
// the case gives want. Without a prefix, a relative %(prefix)/ include is
// not read: this is the approved default, which gives control. A prefix of
// more components names another file, which these cases do not write.
func assertGitPrefixCase(t *testing.T, c includeCase, f includeFixture, folder, control, want string) {
	t.Helper()
	if got, err := (Options{GitPrefix: f.prefix}).GitRoot(folder); err != nil || got != want {
		t.Fatalf("Options{GitPrefix: %q}.GitRoot = %q, %v; want %q", f.prefix, got, err, want)
	}
	assertIncludeResolveOptions(t, folder, want, Options{GitPrefix: f.prefix})
	unset := control
	if strings.HasPrefix(c.name, "prefix-absolute-") {
		unset = want
	}
	if got, err := GitRoot(folder); err != nil || got != unset {
		t.Fatalf("GitRoot without a prefix = %q, %v; want %q", got, err, unset)
	}
	if got, err := (Options{}).GitRoot(folder); err != nil || got != unset {
		t.Fatalf("Options{}.GitRoot = %q, %v; want %q", got, err, unset)
	}
	assertIncludeResolveOptions(t, folder, unset, Options{})
	deeper := mkdir(t, filepath.Join(f.base, "deeper", "a", "b", "c", "d", "e", "f", "g", "h"))
	if got, err := (Options{GitPrefix: deeper}).GitRoot(folder); err != nil || got != unset {
		t.Fatalf("Options{GitPrefix: %q}.GitRoot = %q, %v; want %q", deeper, got, err, unset)
	}
}

// hostGitPrefix measures the prefix of git's build: Git names the path of a
// config file that it cannot parse, which for a %(prefix)/ include is the
// prefix, a separator and the rest.
func hostGitPrefix(t *testing.T, git string) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(base, "broken.cfg")
	writeFile(t, broken, globalConfigBroken)
	// 64 .. components reach the root from any prefix.
	rest := strings.Repeat("../", 64) + strings.TrimPrefix(broken, "/")
	config := filepath.Join(base, "probe.cfg")
	writeFile(t, config, includeLine("%(prefix)/"+rest))
	cmd := exec.Command(git, "config", "--file", config, "--includes", "--list")
	cmd.Env = append(testgit.Env(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := cmd.CombinedOutput()
	end := strings.Index(string(out), "/"+rest)
	if err == nil || end < 0 {
		t.Fatalf("git names no prefix: %v %s", err, out)
	}
	prefix := string(out[strings.LastIndex(string(out[:end]), " ")+1 : end])
	if prefix == "" {
		// An empty or root prefix gives //rest.
		prefix = "/"
	}
	if info, err := os.Stat(prefix); err != nil || !info.IsDir() {
		t.Fatalf("measured prefix %q: %v", prefix, err)
	}
	t.Logf("git build prefix %q", prefix)
	return prefix
}

// Git reads the global config files when it starts, from the probe cwd, and
// again after setup, from its working folder. A relative GIT_CONFIG_GLOBAL
// names a file that only one of these reads finds, so each read must use the
// prefix.
func TestGitRootGitPrefixReads(t *testing.T) {
	git, _ := exec.LookPath("git")
	for _, c := range []struct {
		name  string
		start bool
	}{
		{name: "start", start: true},
		{name: "setup"},
	} {
		t.Run(c.name, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			tree := filepath.Join(base, "tree")
			makeRepo(t, tree)
			writeFile(t, filepath.Join(tree, ".git", "config"), "[core]\n\trepositoryformatversion = 0\n")
			folder := mkdir(t, filepath.Join(tree, "app"))
			f := includeFixture{base: base, folder: folder, prefix: mkdir(t, filepath.Join(base, "prefix"))}
			if git != "" {
				f.prefix = hostGitPrefix(t, git)
			}
			reader := tree
			if c.start {
				reader = folder
			}
			writeFile(t, filepath.Join(reader, "global.cfg"), prefixLine(f, prefixTarget(t, f, globalConfigBroken)))
			env := applyGlobalEnv(t, testgit.Env(), includeCaseVars(base, map[string]*string{"GIT_CONFIG_GLOBAL": globalValue("global.cfg")}))
			if git != "" {
				if measured := globalSetupGit(t, git, folder, env); measured != "" {
					t.Fatalf("Git root = %q; want none", measured)
				}
			}
			assertGitPrefixCase(t, includeCase{name: "prefix-relative-" + c.name}, f, folder, tree, "")
		})
	}
}

// A relative prefix gives a relative path, which starts at the including
// file's folder, as in a Git build with a relative prefix.
func TestGitRootGitPrefixRelative(t *testing.T) {
	for _, c := range []struct{ name, prefix string }{
		{name: "including-folder", prefix: "relative"},
		{name: "parent", prefix: "../outside"},
		{name: "other", prefix: "missing"},
	} {
		t.Run(c.name, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			tree := filepath.Join(base, "tree")
			makeRepo(t, tree)
			writeFile(t, filepath.Join(tree, ".git", "config"), "[core]\n\trepositoryformatversion = 0\n")
			folder := mkdir(t, filepath.Join(tree, "app"))
			global := filepath.Join(base, "global", "global.cfg")
			writeFile(t, global, includeLine("%(prefix)/etc/inc.cfg"))
			writeFile(t, filepath.Join(base, "global", "relative", "etc", "inc.cfg"), globalConfigBroken)
			writeFile(t, filepath.Join(base, "outside", "etc", "inc.cfg"), globalConfigBroken)
			// Not the probe cwd or Git's working folder.
			writeFile(t, filepath.Join(folder, "missing", "etc", "inc.cfg"), globalConfigBroken)
			writeFile(t, filepath.Join(tree, "missing", "etc", "inc.cfg"), globalConfigBroken)
			applyGlobalEnv(t, nil, includeCaseVars(base, map[string]*string{"GIT_CONFIG_GLOBAL": &global}))
			want := ""
			if c.name == "other" {
				want = tree
			}
			if got, err := (Options{GitPrefix: c.prefix}).GitRoot(folder); err != nil || got != want {
				t.Fatalf("Options{GitPrefix: %q}.GitRoot = %q, %v; want %q", c.prefix, got, err, want)
			}
			assertIncludeResolveOptions(t, folder, want, Options{GitPrefix: c.prefix})
		})
	}
}
