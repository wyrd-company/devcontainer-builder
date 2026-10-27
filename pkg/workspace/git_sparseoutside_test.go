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

// sparseOutsideSource selects the same key through each full-read source. All
// sources run through the native contract and eight real-Git discovery routes,
// including Resolve's source, target and workspace folder assertions.
func sparseOutsideSource(source, body, value string) func(*testing.T, includeFixture) map[string]*string {
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
			return commandPairs("SPARSE.EXPECTFILESOUTSIDEOFPATTERNS", value)(t, f)
		case "parameters":
			return commandParameters("'SpArSe.ExPeCtFiLeSoUtSiDeOfPaTtErNs'="+commandQuote(value))(t, f)
		case "command-include":
			return commandInclude(body, false)(t, f)
		case "parameters-include":
			return commandInclude(body, true)(t, f)
		default:
			return includeGlobal(includeLine("one.cfg"), map[string]string{"global/one.cfg": includeLine("two.cfg"), "global/two.cfg": body})(t, f)
		}
	}
}

var sparseOutsideCases = func() []includeCase {
	var cases []includeCase
	for _, source := range []string{"global", "home", "xdg", "system", "system-include", "repository", "repository-include", "worktree", "worktree-include", "worktree-disabled", "count", "parameters", "command-include", "parameters-include", "global-nested"} {
		for _, value := range []struct {
			name, value string
			ok          bool
		}{
			{"invalid", "invalid", false}, {"warn", "warn", false}, {"input", "input", false}, {"true", "true", true}, {"false", "OFF", true}, {"empty", "", true}, {"integer", "-0x1", true}, {"overflow", "2147483648", false},
		} {
			cases = append(cases, includeCase{name: "sparseoutside-" + source + "-" + value.name, setup: sparseOutsideSource(source, "[sparse]\nexpectfilesoutsideofpatterns="+value.value+"\n", value.value), ok: value.ok || source == "worktree-disabled"})
		}
	}
	for _, c := range []struct {
		name, body string
		ok         bool
	}{
		{"valueless", "[sparse]\nexpectfilesoutsideofpatterns\n", true},
		{"subsection", "[sparse \"other\"]\nexpectfilesoutsideofpatterns=invalid\n", true},
		{"old-subsection", "[sparse.other]\nexpectfilesoutsideofpatterns=invalid\n", true},
		{"unrelated-key", "[sample]\nexpectfilesoutsideofpatterns=invalid\n", true},
		{"upper-key", "[SPARSE]\nEXPECTFILESOUTSIDEOFPATTERNS=invalid\n", false},
		{"invalid-before-valid", "[sparse]\nexpectfilesoutsideofpatterns=invalid\nexpectfilesoutsideofpatterns=true\n", false},
		{"valid-before-invalid", "[sparse]\nexpectfilesoutsideofpatterns=true\nexpectfilesoutsideofpatterns=invalid\n", false},
		{"invalid-before-valid-symlinks", "[sparse]\nexpectfilesoutsideofpatterns=invalid\n[core]\nsymlinks=true\n", false},
		{"invalid-symlinks-before-valid", "[core]\nsymlinks=invalid\n[sparse]\nexpectfilesoutsideofpatterns=true\n", false},
		{"invalid-before-valid-trustctime", "[sparse]\nexpectfilesoutsideofpatterns=invalid\n[core]\ntrustctime=true\n", false},
		{"invalid-trustctime-before-valid", "[core]\ntrustctime=invalid\n[sparse]\nexpectfilesoutsideofpatterns=true\n", false},
		{"invalid-before-valid-quotepath", "[sparse]\nexpectfilesoutsideofpatterns=invalid\n[core]\nquotepath=true\n", false},
		{"invalid-quotepath-before-valid", "[core]\nquotepath=invalid\n[sparse]\nexpectfilesoutsideofpatterns=true\n", false},
		{"invalid-before-valid-ignorestat", "[sparse]\nexpectfilesoutsideofpatterns=invalid\n[core]\nignorestat=true\n", false},
		{"invalid-ignorestat-before-valid", "[core]\nignorestat=invalid\n[sparse]\nexpectfilesoutsideofpatterns=true\n", false},
		{"invalid-before-valid-sparsecheckout", "[sparse]\nexpectfilesoutsideofpatterns=invalid\n[core]\nsparsecheckout=true\n", false},
		{"invalid-sparsecheckout-before-valid", "[core]\nsparsecheckout=invalid\n[sparse]\nexpectfilesoutsideofpatterns=true\n", false},
		{"invalid-before-valid-sparsecheckoutcone", "[sparse]\nexpectfilesoutsideofpatterns=invalid\n[core]\nsparsecheckoutcone=true\n", false},
		{"invalid-sparsecheckoutcone-before-valid", "[core]\nsparsecheckoutcone=invalid\n[sparse]\nexpectfilesoutsideofpatterns=true\n", false},
		{"invalid-before-valid-protecthfs", "[sparse]\nexpectfilesoutsideofpatterns=invalid\n[core]\nprotecthfs=true\n", false},
		{"invalid-protecthfs-before-valid", "[core]\nprotecthfs=invalid\n[sparse]\nexpectfilesoutsideofpatterns=true\n", false},
		{"invalid-before-valid-protectntfs", "[sparse]\nexpectfilesoutsideofpatterns=invalid\n[core]\nprotectntfs=true\n", false},
		{"invalid-protectntfs-before-valid", "[core]\nprotectntfs=invalid\n[sparse]\nexpectfilesoutsideofpatterns=true\n", false},
		{"invalid-before-valid-precomposeunicode", "[sparse]\nexpectfilesoutsideofpatterns=invalid\n[core]\nprecomposeunicode=true\n", false},
		{"invalid-precomposeunicode-before-valid", "[core]\nprecomposeunicode=invalid\n[sparse]\nexpectfilesoutsideofpatterns=true\n", false},
		{"invalid-before-valid-fsyncobjectfiles", "[sparse]\nexpectfilesoutsideofpatterns=invalid\n[core]\nfsyncobjectfiles=true\n", false},
		{"invalid-fsyncobjectfiles-before-valid", "[core]\nfsyncobjectfiles=invalid\n[sparse]\nexpectfilesoutsideofpatterns=true\n", false},
		{"invalid-before-safecrlf-warn", "[sparse]\nexpectfilesoutsideofpatterns=invalid\n[core]\nsafecrlf=warn\n", false},
		{"invalid-before-autocrlf-input", "[sparse]\nexpectfilesoutsideofpatterns=invalid\n[core]\nautocrlf=input\n", false},
		{"safecrlf-warn-autocrlf-input-control", "[sparse]\nexpectfilesoutsideofpatterns=true\n[core]\nsafecrlf=WaRn\nautocrlf=InPuT\n", true},
		{"safecrlf-input-remains-invalid", "[sparse]\nexpectfilesoutsideofpatterns=true\n[core]\nsafecrlf=input\n", false},
		{"autocrlf-warn-remains-invalid", "[sparse]\nexpectfilesoutsideofpatterns=true\n[core]\nautocrlf=warn\n", false},
		{"invalid-safecrlf-before-valid", "[core]\nsafecrlf=invalid\n[sparse]\nexpectfilesoutsideofpatterns=true\n", false},
		{"invalid-autocrlf-before-valid", "[core]\nautocrlf=invalid\n[sparse]\nexpectfilesoutsideofpatterns=true\n", false},
		{"valid-duplicates", "[sparse]\nexpectfilesoutsideofpatterns=true\nexpectfilesoutsideofpatterns=false\n", true},
		{"invalid-before-valid-ignorecase", "[sparse]\nexpectfilesoutsideofpatterns=invalid\n[core]\nignorecase=true\n", false},
		{"invalid-ignorecase-before-valid", "[core]\nignorecase=invalid\n[sparse]\nexpectfilesoutsideofpatterns=true\n", false},
		{"invalid-before-valid-filemode", "[sparse]\nexpectfilesoutsideofpatterns=invalid\n[core]\nfilemode=true\n", false},
		{"invalid-filemode-before-valid", "[core]\nfilemode=invalid\n[sparse]\nexpectfilesoutsideofpatterns=true\n", false},
	} {
		cases = append(cases, includeCase{name: "sparseoutside-" + c.name, setup: includeGlobal(c.body, nil), ok: c.ok})
	}
	for _, c := range []struct {
		name, body string
		ok         bool
	}{
		{"invalid-before-valid-lockfilepid", "[sparse]\nexpectfilesoutsideofpatterns=invalid\n[core]\nlockfilepid=true\n", false},
		{"invalid-lockfilepid-before-valid", "[core]\nlockfilepid=invalid\n[sparse]\nexpectfilesoutsideofpatterns=true\n", false},
		{"core-namespace-control", "[core]\nexpectfilesoutsideofpatterns=invalid\n", true},
	} {
		// Git before 2.54 ignores only the prior lockfilePid guard in this case.
		cases = append(cases, includeCase{name: "sparseoutside-" + c.name, setup: includeGlobal(c.body, nil), ok: c.ok, gitBefore54OK: c.name == "invalid-lockfilepid-before-valid"})
	}
	for _, value := range []string{"yes", "No", "ON", "0", "1", "-1", "01", "0x1", "1k", "2m", "1g", "-2147483648", "2147483647", "-2147483649", "2g", "1.0", "truth", "1 ", "0b10"} {
		// A trailing space survives quoting so Git's integer parser must reject it.
		ok := value != "-2147483649" && value != "2g" && value != "1.0" && value != "truth" && value != "1 "
		cases = append(cases, includeCase{name: "sparseoutside-value-" + strings.ReplaceAll(value, " ", "space"), setup: commandPairs("sparse.expectfilesoutsideofpatterns", value), ok: ok})
	}
	cases = append(cases,
		includeCase{name: "sparseoutside-prefix-invalid", setup: prefixGlobal("[sparse]\nexpectfilesoutsideofpatterns=invalid\n"), gitPrefix: true},
		includeCase{name: "sparseoutside-prefix-valid", setup: prefixGlobal("[sparse]\nexpectfilesoutsideofpatterns=true\n"), gitPrefix: true, ok: true},
		includeCase{name: "sparseoutside-count-prefix-invalid", gitPrefix: true, setup: func(t *testing.T, f includeFixture) map[string]*string {
			target := prefixTarget(t, f, "[sparse]\nexpectfilesoutsideofpatterns=invalid\n")
			return commandPairs("include.path", "%(prefix)/"+prefixRest(f.prefix, target))(t, f)
		}},
		includeCase{name: "sparseoutside-parameters-prefix-invalid", gitPrefix: true, setup: func(t *testing.T, f includeFixture) map[string]*string {
			target := prefixTarget(t, f, "[sparse]\nexpectfilesoutsideofpatterns=invalid\n")
			return commandParameters("'include.path'="+commandQuote("%(prefix)/"+prefixRest(f.prefix, target)))(t, f)
		}},
		includeCase{name: "sparseoutside-command-valueless", setup: commandParameters("'sparse.expectfilesoutsideofpatterns'"), ok: true},
		includeCase{name: "sparseoutside-command-invalid-before-valid", setup: commandParameters("'sparse.expectfilesoutsideofpatterns=invalid' 'sparse.expectfilesoutsideofpatterns=true'")},
		includeCase{name: "sparseoutside-count-before-parameters", setup: func(t *testing.T, f includeFixture) map[string]*string {
			e := commandPairs("sparse.expectfilesoutsideofpatterns", "invalid")(t, f)
			e["GIT_CONFIG_PARAMETERS"] = globalValue("'sparse.expectfilesoutsideofpatterns=true'")
			return e
		}},
		includeCase{name: "sparseoutside-global-before-command", setup: func(t *testing.T, f includeFixture) map[string]*string {
			e := includeGlobal("[sparse]\nexpectfilesoutsideofpatterns=invalid\n", nil)(t, f)
			for k, v := range commandPairs("sparse.expectfilesoutsideofpatterns", "true")(t, f) {
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
func TestGitRootSparseOutsideOptionsMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	host := readHostGit(t, git)
	prefix := hostGitPrefix(t, git)
	for _, c := range sparseOutsideCases {
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
			gitWant := want
			if c.gitBefore54OK && host.minor < 54 {
				gitWant = tree
			}
			if got := globalSetupGit(t, git, folder, env); got != gitWant {
				t.Fatalf("Git root = %q; want %q", got, gitWant)
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
