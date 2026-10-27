// relationships:
//   references: [git_command_config, git_config_include_test, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

func commandPairs(values ...string) func(*testing.T, includeFixture) map[string]*string {
	return func(_ *testing.T, _ includeFixture) map[string]*string {
		env := map[string]*string{"GIT_CONFIG_COUNT": globalValue(fmt.Sprint(len(values) / 2))}
		for i := 0; i < len(values); i += 2 {
			env[fmt.Sprintf("GIT_CONFIG_KEY_%d", i/2)] = globalValue(values[i])
			env[fmt.Sprintf("GIT_CONFIG_VALUE_%d", i/2)] = globalValue(values[i+1])
		}
		return env
	}
}

func commandParameters(raw string) func(*testing.T, includeFixture) map[string]*string {
	return func(_ *testing.T, _ includeFixture) map[string]*string {
		return map[string]*string{"GIT_CONFIG_PARAMETERS": &raw}
	}
}

// commandQuote uses Git's single-quote encoding, including the two characters
// that sq_quote_buf escapes outside a quoted segment.
func commandQuote(s string) string {
	s = strings.ReplaceAll(s, "'", "'\\''")
	s = strings.ReplaceAll(s, "!", "'\\!'")
	return "'" + s + "'"
}

func commandInclude(body string, parameters bool) func(*testing.T, includeFixture) map[string]*string {
	return func(t *testing.T, f includeFixture) map[string]*string {
		target := filepath.Join(f.base, "command.cfg")
		writeFile(t, target, body)
		if parameters {
			return commandParameters("'include.path'="+commandQuote(target))(t, f)
		}
		return commandPairs("include.path", target)(t, f)
	}
}

func assertCommandConfigGitExit(t *testing.T, git, folder string, env []string, ok bool) {
	t.Helper()
	cmd := exec.Command(git, "rev-parse", "--show-cdup")
	cmd.Dir, cmd.Env = folder, env
	out, err := cmd.CombinedOutput()
	want := 128
	if ok {
		want = 0
	}
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != want {
		t.Fatalf("Git exit = %v, %v: %s; want %d", cmd.ProcessState, err, out, want)
	}
}

// Every row runs as a native contract and against real Git through the eight
// discovery routes in TestGitRootConfigIncludeMatchesGit, with Resolve's bind
// source, target and workspace folder asserted by the same test.
var commandConfigCases = func() []includeCase {
	cases := []includeCase{
		{name: "command-count-valid", setup: commandPairs("sample.value", "1"), ok: true},
		{name: "command-count-empty-value", setup: commandPairs("sample.value", ""), ok: true},
		{name: "command-count-second-key", setup: commandPairs("sample.value", "1", "nodot", "1")},
		{name: "command-count-empty-key", setup: commandPairs("", "1")},
		{name: "command-count-no-section", setup: commandPairs("nodot", "1")},
		{name: "command-count-no-variable", setup: commandPairs("sample.", "1")},
		{name: "command-count-variable-digit", setup: commandPairs("sample.1value", "1")},
		{name: "command-count-bad-section", setup: commandPairs("bad_name.value", "1")},
		{name: "command-count-newline-subsection", setup: commandPairs("sample.a\nb.value", "1")},
		{name: "command-count-subsection", setup: commandPairs("Sample.A = '!.Value", "1"), ok: true},
		{name: "command-count-leading-dot-subsection", setup: commandPairs(".sub.value", "1"), ok: true},
		{name: "command-count-key-missing", setup: func(_ *testing.T, _ includeFixture) map[string]*string {
			return map[string]*string{"GIT_CONFIG_COUNT": globalValue("1")}
		}},
		{name: "command-count-value-missing", setup: func(_ *testing.T, _ includeFixture) map[string]*string {
			return map[string]*string{"GIT_CONFIG_COUNT": globalValue("1"), "GIT_CONFIG_KEY_0": globalValue("sample.value")}
		}},
		{name: "command-count-second-value-missing", setup: func(t *testing.T, f includeFixture) map[string]*string {
			e := commandPairs("sample.one", "1", "sample.two", "2")(t, f)
			e["GIT_CONFIG_VALUE_1"] = nil
			return e
		}},
		{name: "command-include-valid", setup: commandInclude(globalConfigValid, false), ok: true},
		{name: "command-include-syntax", setup: commandInclude(globalConfigBroken, false)},
		{name: "command-parameters-include-valid", setup: commandInclude(globalConfigValid, true), ok: true},
		{name: "command-parameters-include-syntax", setup: commandInclude(globalConfigBroken, true)},
		{name: "command-include-relative", setup: commandPairs("include.path", "relative.cfg")},
		{name: "command-include-empty", setup: commandPairs("include.path", "")},
		{name: "command-include-missing", setup: func(t *testing.T, f includeFixture) map[string]*string {
			return commandPairs("include.path", filepath.Join(f.base, "missing.cfg"))(t, f)
		}, ok: true},
		{name: "command-include-directory", setup: func(t *testing.T, f includeFixture) map[string]*string {
			return commandPairs("include.path", mkdir(t, filepath.Join(f.base, "directory")))(t, f)
		}},
		{name: "command-include-upper-case", setup: func(t *testing.T, f includeFixture) map[string]*string {
			e := commandInclude(globalConfigBroken, false)(t, f)
			e["GIT_CONFIG_KEY_0"] = globalValue("INCLUDE.PATH")
			return e
		}},
		{name: "command-include-subsection-ignored", setup: commandPairs("include.sub.path", "relative.cfg"), ok: true},
		{name: "command-include-nested-relative", setup: func(t *testing.T, f includeFixture) map[string]*string {
			writeFile(t, filepath.Join(f.base, "nested.cfg"), globalConfigBroken)
			return commandInclude(includeLine("nested.cfg"), false)(t, f)
		}},
		{name: "command-include-home-syntax", setup: func(t *testing.T, f includeFixture) map[string]*string {
			writeFile(t, filepath.Join(f.base, "home", "inc.cfg"), globalConfigBroken)
			e := commandPairs("include.path", "~/inc.cfg")(t, f)
			e["HOME"] = globalValue(filepath.Join(f.base, "home"))
			return e
		}},
		{name: "command-include-no-home", setup: func(t *testing.T, f includeFixture) map[string]*string {
			e := commandPairs("include.path", "~/inc.cfg")(t, f)
			e["HOME"] = nil
			return e
		}},
		{name: "command-include-unknown-account", setup: commandPairs("include.path", "~no-such-user-for-dcb-tests/inc.cfg"), unknownAccount: true},
		{name: "command-prefix-relative-syntax", gitPrefix: true, setup: func(t *testing.T, f includeFixture) map[string]*string {
			return commandPairs("include.path", "%(prefix)/"+prefixRest(f.prefix, prefixTarget(t, f, globalConfigBroken)))(t, f)
		}},
		{name: "command-prefix-relative-valid", gitPrefix: true, ok: true, setup: func(t *testing.T, f includeFixture) map[string]*string {
			return commandPairs("include.path", "%(prefix)/"+prefixRest(f.prefix, prefixTarget(t, f, globalConfigValid)))(t, f)
		}},
		{name: "command-prefix-absolute-syntax", setup: func(t *testing.T, f includeFixture) map[string]*string {
			return commandPairs("include.path", "%(prefix)/"+prefixTarget(t, f, globalConfigBroken))(t, f)
		}},
		{name: "command-parameters-include-escaped-path", setup: func(t *testing.T, f includeFixture) map[string]*string {
			target := filepath.Join(f.base, "a'b!c.cfg")
			writeFile(t, target, globalConfigBroken)
			return commandParameters("'include.path'="+commandQuote(target))(t, f)
		}},
		{name: "command-parameters-old-include-syntax", setup: func(t *testing.T, f includeFixture) map[string]*string {
			target := filepath.Join(f.base, "old.cfg")
			writeFile(t, target, globalConfigBroken)
			return commandParameters(commandQuote("include.path="+target))(t, f)
		}},
		{name: "command-count-before-parameters", setup: func(t *testing.T, f includeFixture) map[string]*string {
			e := commandInclude(globalConfigBroken, false)(t, f)
			e["GIT_CONFIG_PARAMETERS"] = globalValue("'include.path'=" + commandQuote(os.DevNull))
			return e
		}},
		{name: "command-parameters-after-count", setup: func(t *testing.T, f includeFixture) map[string]*string {
			e := commandPairs("sample.value", "1")(t, f)
			e["GIT_CONFIG_PARAMETERS"] = globalValue("unquoted")
			return e
		}},
	}
	for _, c := range []struct {
		name, raw string
		ok        bool
	}{
		{"empty", "", true}, {"zero", "0", true}, {"plus-zero", "+0", true}, {"negative-zero", "-0", true},
		{"space-one", " \t\v\f\r\n+01", true}, {"negative-wrap", "-18446744073709551615", true},
		{"bogus", "bogus", false}, {"negative", "-1", false}, {"maximum", "2147483647", false},
		{"over-maximum", "2147483648", false}, {"overflow", "18446744073709551616", false},
		{"negative-overflow", "-18446744073709551616", false}, {"space-only", " ", false},
		{"sign-only", "+", false}, {"trailing-space", "1 ", false}, {"hex", "0x1", false},
	} {
		cases = append(cases, includeCase{name: "command-count-" + c.name, ok: c.ok, setup: func(t *testing.T, f includeFixture) map[string]*string {
			e := commandPairs("sample.value", "1")(t, f)
			e["GIT_CONFIG_COUNT"] = &c.raw
			if c.name == "maximum" {
				delete(e, "GIT_CONFIG_KEY_0")
			}
			return e
		}})
	}
	for _, c := range []struct {
		name, raw string
		ok        bool
	}{
		{"empty", "", true}, {"old", "'sample.value=1'", true}, {"new", "'sample.value'='1'", true},
		{"implicit-old", "'sample.value'", true}, {"implicit-new", "'sample.value'=", true},
		{"empty-value", "'sample.value'=''", true}, {"mixed", "'sample.one=1' \t'sample.two'='a=b'\r\n'sample.three'= ", true},
		{"escaped", "'sample.sub'\\''section'\\!'.value'='a'\\''b'\\!'c'", true},
		{"no-quotes", "sample.value=1", false}, {"leading-space", " 'sample.value=1'", false},
		{"space-only", " ", false}, {"bad-key", "'nodot'", false}, {"empty-key", "''='1'", false},
		{"unclosed-key", "'sample.value", false}, {"unclosed-value", "'sample.value'='1", false},
		{"unquoted-value", "'sample.value'=1", false}, {"adjacent", "'sample.one'='1''sample.two'='2'", false},
		{"unknown-separator", "'sample.value': '1'", false}, {"invalid-escape", "'sample.value'\\x'='1'", false},
		{"formfeed-separator", "'sample.one=1'\f'sample.two=2'", false},
		{"valueless-include-old", "'include.path'", false}, {"valueless-include-new", "'include.path'=", false},
		{"empty-include", "'include.path'=''", false}, {"relative-include", "'include.path=relative.cfg'", false},
	} {
		cases = append(cases, includeCase{name: "command-parameters-" + c.name, setup: commandParameters(c.raw), ok: c.ok})
	}
	for _, depth := range []int{10, 11} {
		cases = append(cases, includeCase{name: fmt.Sprintf("command-include-depth-%d", depth), ok: depth == 10, setup: func(t *testing.T, f includeFixture) map[string]*string {
			for i := 1; i <= depth; i++ {
				body := globalConfigValid
				if i < depth {
					body = includeLine(fmt.Sprintf("d%d.cfg", i+1))
				}
				writeFile(t, filepath.Join(f.base, fmt.Sprintf("d%d.cfg", i)), body)
			}
			return commandPairs("include.path", filepath.Join(f.base, "d1.cfg"))(t, f)
		}})
	}
	return cases
}()

func TestGitCommandConfigCount(t *testing.T) {
	for _, c := range []struct {
		raw  string
		want uint64
		ok   bool
	}{
		{"", 0, true}, {"2147483647", 2147483647, true}, {"2147483648", 2147483648, false},
		{"-18446744073709551615", 1, true}, {"-18446744073709551614", 2, true},
		{"-18446744073709551616", 0, false}, {"-0", 0, true}, {" \v\f01", 1, true}, {" \v\f", 0, false},
	} {
		t.Run(fmt.Sprintf("%q", c.raw), func(t *testing.T) {
			got, ok := gitCommandConfigCount(c.raw)
			if ok != c.ok || ok && got != c.want {
				t.Fatalf("count = %d, %v; want %d, %v", got, ok, c.want, c.ok)
			}
		})
	}
}

func TestGitCommandConfigParameters(t *testing.T) {
	var entries []gitConfigEntry
	raw := "'Sample.MixedCase.Value'='a'\\''b'\\!'c' 'sample.flag'= 'sample.empty'='' 'sample.old=a=b'"
	if !gitCommandConfigParameters(raw, func(key string, value *string) bool {
		name, ok := gitCommandConfigKey(key)
		entries = append(entries, gitConfigEntry{name, value})
		return ok
	}) {
		t.Fatal("parameters rejected")
	}
	if len(entries) != 4 || entries[0].name != "sample.MixedCase.value" || entries[0].value == nil || *entries[0].value != "a'b!c" || entries[1].value != nil || entries[2].value == nil || *entries[2].value != "" || entries[3].value == nil || *entries[3].value != "a=b" {
		t.Fatalf("entries = %+v", entries)
	}
	if gitCommandConfigParameters("'sample.one=1' 'sample.two=2'", func(string, *string) bool { return false }) {
		t.Fatal("callback failure ignored")
	}
}

func TestGitRootCommandConfigRelativePrefix(t *testing.T) {
	base := t.TempDir()
	tree := filepath.Join(base, "tree")
	makeRepo(t, tree)
	folder := mkdir(t, filepath.Join(tree, "app"))
	writeFile(t, filepath.Join(tree, "config.cfg"), globalConfigValid)
	applyGlobalEnv(t, nil, includeCaseVars(base, map[string]*string{
		"GIT_CONFIG_COUNT": globalValue("1"), "GIT_CONFIG_KEY_0": globalValue("include.path"), "GIT_CONFIG_VALUE_0": globalValue("%(prefix)/config.cfg"),
	}))
	for _, c := range []struct{ prefix, want string }{{"", tree}, {tree, tree}, {".", ""}} {
		t.Run(fmt.Sprintf("prefix-%s", c.prefix), func(t *testing.T) {
			opts := Options{GitPrefix: c.prefix}
			if got, err := opts.GitRoot(folder); err != nil || got != c.want {
				t.Fatalf("GitRoot = %q, %v; want %q", got, err, c.want)
			}
			assertIncludeResolveOptions(t, folder, c.want, opts)
		})
	}
}

// Assert Resolve independently so a GitRoot assertion cannot prevent its
// mount assertions from executing in an external mutation control.
func TestResolveCommandConfigMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	prefix := hostGitPrefix(t, git)
	for _, c := range commandConfigCases {
		t.Run(c.name, func(t *testing.T) {
			base, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			tree := filepath.Join(base, "tree")
			cmd := exec.Command(git, "init", "-q", "-b", "main", tree)
			cmd.Env = testgit.Env()
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git init: %v %s", err, out)
			}
			folder := mkdir(t, filepath.Join(tree, "app"))
			admin := filepath.Join(tree, ".git")
			f := includeFixture{base: base, folder: folder, common: admin, admin: admin, prefix: prefix}
			env := applyGlobalEnv(t, testgit.Env(), includeCaseVars(base, c.setup(t, f)))
			want := ""
			if c.ok {
				want = tree
			}
			assertCommandConfigGitExit(t, git, folder, env, c.ok)
			if measured := globalSetupGit(t, git, folder, env); measured != want {
				t.Fatalf("Git root = %q; want %q", measured, want)
			}
			if c.nativeOK() {
				want = tree
			}
			opts := Options{}
			if c.gitPrefix {
				opts.GitPrefix = prefix
			}
			assertIncludeResolveOptions(t, folder, want, opts)
		})
	}
}
