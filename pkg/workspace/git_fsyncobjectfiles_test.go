// relationships:
//   references: [git_config_full, git_config_include_test, git_command_config_test]

package workspace

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

// fsyncobjectfilesSource selects the same key through each full-read source. All
// sources run through the native contract and eight real-Git discovery routes,
// including Resolve's source, target and workspace folder assertions.
func fsyncobjectfilesSource(source, body, value string) func(*testing.T, includeFixture) map[string]*string {
	return func(t *testing.T, f includeFixture) map[string]*string {
		content := body
		switch source {
		case "global":
			return includeGlobal(body, nil)(t, f)
		case "home":
			return includeHome(body, nil)(t, f)
		case "xdg":
			home := mkdir(t, filepath.Join(f.base, "home"))
			xdg := filepath.Join(f.base, "xdg")
			writeFile(t, filepath.Join(xdg, "git", "config"), body)
			return map[string]*string{"GIT_CONFIG_GLOBAL": nil, "HOME": &home, "XDG_CONFIG_HOME": &xdg}
		case "system", "system-include":
			file := filepath.Join(f.base, "system.cfg")
			if source == "system-include" {
				writeFile(t, filepath.Join(f.base, "inc.cfg"), body)
				content = includeLine("inc.cfg")
			}
			writeFile(t, file, content)
			return map[string]*string{"GIT_CONFIG_NOSYSTEM": nil, "GIT_CONFIG_SYSTEM": &file}
		case "repository":
			return includeRepository(body, nil)(t, f)
		case "repository-include":
			return includeRepository(includeLine("inc.cfg"), map[string]string{"inc.cfg": body})(t, f)
		case "worktree", "worktree-include", "worktree-disabled":
			if source != "worktree-disabled" {
				includeRepository("[core]\nrepositoryformatversion=1\n[extensions]\nworktreeConfig=true\n", nil)(t, f)
			}
			if source == "worktree-include" {
				writeFile(t, filepath.Join(f.admin, "inc.cfg"), body)
				content = includeLine("inc.cfg")
			}
			writeFile(t, filepath.Join(f.admin, "config.worktree"), content)
			return nil
		case "count":
			return commandPairs("CORE.FSYNCOBJECTFILES", value)(t, f)
		case "parameters":
			return commandParameters("'CoRe.FsYnCoBjEcTfIlEs'="+commandQuote(value))(t, f)
		case "command-include":
			return commandInclude(body, false)(t, f)
		case "parameters-include":
			return commandInclude(body, true)(t, f)
		default:
			return includeGlobal(includeLine("one.cfg"), map[string]string{"global/one.cfg": includeLine("two.cfg"), "global/two.cfg": body})(t, f)
		}
	}
}

var fsyncobjectfilesCases = func() []includeCase {
	var cases []includeCase
	for _, source := range []string{"global", "home", "xdg", "system", "system-include", "repository", "repository-include", "worktree", "worktree-include", "worktree-disabled", "count", "parameters", "command-include", "parameters-include", "global-nested"} {
		for _, value := range []struct {
			name, value string
			ok          bool
		}{
			{"invalid", "invalid", false}, {"true", "true", true}, {"false", "OFF", true}, {"empty", "", true}, {"integer", "-0x1", true}, {"overflow", "2147483648", false},
		} {
			cases = append(cases, includeCase{name: "fsyncobjectfiles-" + source + "-" + value.name, setup: fsyncobjectfilesSource(source, "[core]\nfsyncobjectfiles="+value.value+"\n", value.value), ok: value.ok || source == "worktree-disabled"})
		}
	}
	for _, c := range []struct {
		name, body string
		ok         bool
	}{
		{"valueless", "[core]\nfsyncobjectfiles\n", true},
		{"subsection", "[core \"other\"]\nfsyncobjectfiles=invalid\n", true},
		{"old-subsection", "[core.other]\nfsyncobjectfiles=invalid\n", true},
		{"unrelated-key", "[sample]\nfsyncobjectfiles=invalid\n", true},
		{"upper-key", "[CORE]\nFSYNCOBJECTFILES=invalid\n", false},
		{"invalid-before-valid", "[core]\nfsyncobjectfiles=invalid\nfsyncobjectfiles=true\n", false},
		{"valid-before-invalid", "[core]\nfsyncobjectfiles=true\nfsyncobjectfiles=invalid\n", false},
		{"invalid-before-valid-symlinks", "[core]\nfsyncobjectfiles=invalid\nsymlinks=true\n", false},
		{"invalid-symlinks-before-valid", "[core]\nsymlinks=invalid\nfsyncobjectfiles=true\n", false},
		{"invalid-before-valid-trustctime", "[core]\nfsyncobjectfiles=invalid\ntrustctime=true\n", false},
		{"invalid-trustctime-before-valid", "[core]\ntrustctime=invalid\nfsyncobjectfiles=true\n", false},
		{"invalid-before-valid-quotepath", "[core]\nfsyncobjectfiles=invalid\nquotepath=true\n", false},
		{"invalid-quotepath-before-valid", "[core]\nquotepath=invalid\nfsyncobjectfiles=true\n", false},
		{"invalid-before-valid-ignorestat", "[core]\nfsyncobjectfiles=invalid\nignorestat=true\n", false},
		{"invalid-ignorestat-before-valid", "[core]\nignorestat=invalid\nfsyncobjectfiles=true\n", false},
		{"invalid-before-valid-sparsecheckout", "[core]\nfsyncobjectfiles=invalid\nsparsecheckout=true\n", false},
		{"invalid-sparsecheckout-before-valid", "[core]\nsparsecheckout=invalid\nfsyncobjectfiles=true\n", false},
		{"invalid-before-valid-sparsecheckoutcone", "[core]\nfsyncobjectfiles=invalid\nsparsecheckoutcone=true\n", false},
		{"invalid-sparsecheckoutcone-before-valid", "[core]\nsparsecheckoutcone=invalid\nfsyncobjectfiles=true\n", false},
		{"invalid-before-valid-protecthfs", "[core]\nfsyncobjectfiles=invalid\nprotecthfs=true\n", false},
		{"invalid-protecthfs-before-valid", "[core]\nprotecthfs=invalid\nfsyncobjectfiles=true\n", false},
		{"invalid-before-valid-protectntfs", "[core]\nfsyncobjectfiles=invalid\nprotectntfs=true\n", false},
		{"invalid-protectntfs-before-valid", "[core]\nprotectntfs=invalid\nfsyncobjectfiles=true\n", false},
		{"invalid-before-valid-precomposeunicode", "[core]\nfsyncobjectfiles=invalid\nprecomposeunicode=true\n", false},
		{"invalid-precomposeunicode-before-valid", "[core]\nprecomposeunicode=invalid\nfsyncobjectfiles=true\n", false},
		{"valid-duplicates", "[core]\nfsyncobjectfiles=true\nfsyncobjectfiles=false\n", true},
		{"invalid-before-valid-ignorecase", "[core]\nfsyncobjectfiles=invalid\nignorecase=true\n", false},
		{"invalid-ignorecase-before-valid", "[core]\nignorecase=invalid\nfsyncobjectfiles=true\n", false},
		{"invalid-before-valid-filemode", "[core]\nfsyncobjectfiles=invalid\nfilemode=true\n", false},
		{"invalid-filemode-before-valid", "[core]\nfilemode=invalid\nfsyncobjectfiles=true\n", false},
	} {
		cases = append(cases, includeCase{name: "fsyncobjectfiles-" + c.name, setup: includeGlobal(c.body, nil), ok: c.ok})
	}
	for _, value := range []string{"yes", "No", "ON", "0", "1", "-1", "01", "0x1", "1k", "2m", "1g", "-2147483648", "2147483647", "-2147483649", "2g", "1.0", "truth", "1 ", "0b10"} {
		// A trailing space survives quoting so Git's integer parser must reject it.
		ok := value != "-2147483649" && value != "2g" && value != "1.0" && value != "truth" && value != "1 "
		cases = append(cases, includeCase{name: "fsyncobjectfiles-value-" + strings.ReplaceAll(value, " ", "space"), setup: commandPairs("core.fsyncobjectfiles", value), ok: ok})
	}
	cases = append(cases,
		includeCase{name: "fsyncobjectfiles-prefix-invalid", setup: prefixGlobal("[core]\nfsyncobjectfiles=invalid\n"), gitPrefix: true},
		includeCase{name: "fsyncobjectfiles-prefix-valid", setup: prefixGlobal("[core]\nfsyncobjectfiles=true\n"), gitPrefix: true, ok: true},
		includeCase{name: "fsyncobjectfiles-count-prefix-invalid", gitPrefix: true, setup: func(t *testing.T, f includeFixture) map[string]*string {
			target := prefixTarget(t, f, "[core]\nfsyncobjectfiles=invalid\n")
			return commandPairs("include.path", "%(prefix)/"+prefixRest(f.prefix, target))(t, f)
		}},
		includeCase{name: "fsyncobjectfiles-parameters-prefix-invalid", gitPrefix: true, setup: func(t *testing.T, f includeFixture) map[string]*string {
			target := prefixTarget(t, f, "[core]\nfsyncobjectfiles=invalid\n")
			return commandParameters("'include.path'="+commandQuote("%(prefix)/"+prefixRest(f.prefix, target)))(t, f)
		}},
		includeCase{name: "fsyncobjectfiles-command-valueless", setup: commandParameters("'core.fsyncobjectfiles'"), ok: true},
		includeCase{name: "fsyncobjectfiles-command-invalid-before-valid", setup: commandParameters("'core.fsyncobjectfiles=invalid' 'core.fsyncobjectfiles=true'")},
		includeCase{name: "fsyncobjectfiles-count-before-parameters", setup: func(t *testing.T, f includeFixture) map[string]*string {
			e := commandPairs("core.fsyncobjectfiles", "invalid")(t, f)
			e["GIT_CONFIG_PARAMETERS"] = globalValue("'core.fsyncobjectfiles=true'")
			return e
		}},
		includeCase{name: "fsyncobjectfiles-global-before-command", setup: func(t *testing.T, f includeFixture) map[string]*string {
			e := includeGlobal("[core]\nfsyncobjectfiles=invalid\n", nil)(t, f)
			for k, v := range commandPairs("core.fsyncobjectfiles", "true")(t, f) {
				e[k] = v
			}
			return e
		}},
	)
	return cases
}()

// Options.GitRoot must apply the same full-read admission with an explicit
// prefix, including nested and command-line includes. Expected results come
// from real Git before any native API runs.
func TestGitRootFsyncObjectFilesOptionsMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	prefix := hostGitPrefix(t, git)
	for _, c := range fsyncobjectfilesCases {
		t.Run(c.name, func(t *testing.T) {
			base := t.TempDir()
			tree := filepath.Join(base, "tree")
			makeRepo(t, tree)
			folder := mkdir(t, filepath.Join(tree, "app"))
			admin := filepath.Join(tree, ".git")
			writeFile(t, filepath.Join(admin, "config"), "[core]\nrepositoryformatversion=0\n")
			f := includeFixture{base: base, folder: folder, common: admin, admin: admin, prefix: prefix}
			env := applyGlobalEnv(t, testgit.Env(), includeCaseVars(base, c.setup(t, f)))
			want := ""
			if c.ok {
				want = tree
			}
			if got := globalSetupGit(t, git, folder, env); got != want {
				t.Fatalf("Git root = %q; want %q", got, want)
			}
			opts := Options{GitPrefix: prefix}
			t.Run("Options.GitRoot", func(t *testing.T) {
				if got, err := opts.GitRoot(folder); err != nil || got != want {
					t.Fatalf("Options.GitRoot = %q, %v; want %q", got, err, want)
				}
			})
			t.Run("Resolve", func(t *testing.T) {
				assertIncludeResolveOptions(t, folder, want, opts)
			})
		})
	}
}
