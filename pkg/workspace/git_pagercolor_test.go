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

// pagerColorSource selects the same key through each full-read source. All
// sources run through the native contract and eight real-Git discovery routes,
// including Resolve's source, target and workspace folder assertions.
func pagerColorSource(source, body, value string) func(*testing.T, includeFixture) map[string]*string {
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
			return commandPairs("PAGER.COLOR", value)(t, f)
		case "parameters":
			return commandParameters("'PaGeR.CoLoR'="+commandQuote(value))(t, f)
		case "command-include":
			return commandInclude(body, false)(t, f)
		case "parameters-include":
			return commandInclude(body, true)(t, f)
		default:
			return includeGlobal(includeLine("one.cfg"), map[string]string{"global/one.cfg": includeLine("two.cfg"), "global/two.cfg": body})(t, f)
		}
	}
}

var pagerColorCases = func() []includeCase {
	var cases []includeCase
	for _, source := range []string{"global", "home", "xdg", "system", "system-include", "repository", "repository-include", "worktree", "worktree-include", "worktree-disabled", "count", "parameters", "command-include", "parameters-include", "global-nested"} {
		for _, value := range []struct {
			name, value string
			ok          bool
		}{
			{"invalid", "invalid", false}, {"warn", "warn", false}, {"input", "input", false}, {"true", "true", true}, {"false", "OFF", true}, {"empty", "", true}, {"integer", "-0x1", true}, {"overflow", "2147483648", false},
		} {
			cases = append(cases, includeCase{name: "pagercolor-" + source + "-" + value.name, setup: pagerColorSource(source, "[pager]\ncolor="+value.value+"\n", value.value), ok: value.ok || source == "worktree-disabled"})
		}
	}
	for _, c := range []struct {
		name, body string
		ok         bool
	}{
		{"valueless", "[pager]\ncolor\n", true},
		{"subsection", "[pager \"other\"]\ncolor=invalid\n", true},
		{"old-subsection", "[pager.other]\ncolor=invalid\n", true},
		{"unrelated-key", "[sample]\ncolor=invalid\n", true},
		{"upper-key", "[PAGER]\nCOLOR=invalid\n", false},
		{"invalid-before-valid", "[pager]\ncolor=invalid\ncolor=true\n", false},
		{"valid-before-invalid", "[pager]\ncolor=true\ncolor=invalid\n", false},
		{"invalid-before-valid-symlinks", "[pager]\ncolor=invalid\n[core]\nsymlinks=true\n", false},
		{"invalid-symlinks-before-valid", "[core]\nsymlinks=invalid\n[pager]\ncolor=true\n", false},
		{"invalid-before-valid-trustctime", "[pager]\ncolor=invalid\n[core]\ntrustctime=true\n", false},
		{"invalid-trustctime-before-valid", "[core]\ntrustctime=invalid\n[pager]\ncolor=true\n", false},
		{"invalid-before-valid-quotepath", "[pager]\ncolor=invalid\n[core]\nquotepath=true\n", false},
		{"invalid-quotepath-before-valid", "[core]\nquotepath=invalid\n[pager]\ncolor=true\n", false},
		{"invalid-before-valid-ignorestat", "[pager]\ncolor=invalid\n[core]\nignorestat=true\n", false},
		{"invalid-ignorestat-before-valid", "[core]\nignorestat=invalid\n[pager]\ncolor=true\n", false},
		{"invalid-before-valid-sparsecheckout", "[pager]\ncolor=invalid\n[core]\nsparsecheckout=true\n", false},
		{"invalid-sparsecheckout-before-valid", "[core]\nsparsecheckout=invalid\n[pager]\ncolor=true\n", false},
		{"invalid-before-valid-sparsecheckoutcone", "[pager]\ncolor=invalid\n[core]\nsparsecheckoutcone=true\n", false},
		{"invalid-sparsecheckoutcone-before-valid", "[core]\nsparsecheckoutcone=invalid\n[pager]\ncolor=true\n", false},
		{"invalid-before-valid-protecthfs", "[pager]\ncolor=invalid\n[core]\nprotecthfs=true\n", false},
		{"invalid-protecthfs-before-valid", "[core]\nprotecthfs=invalid\n[pager]\ncolor=true\n", false},
		{"invalid-before-valid-protectntfs", "[pager]\ncolor=invalid\n[core]\nprotectntfs=true\n", false},
		{"invalid-protectntfs-before-valid", "[core]\nprotectntfs=invalid\n[pager]\ncolor=true\n", false},
		{"invalid-before-valid-precomposeunicode", "[pager]\ncolor=invalid\n[core]\nprecomposeunicode=true\n", false},
		{"invalid-precomposeunicode-before-valid", "[core]\nprecomposeunicode=invalid\n[pager]\ncolor=true\n", false},
		{"invalid-before-valid-fsyncobjectfiles", "[pager]\ncolor=invalid\n[core]\nfsyncobjectfiles=true\n", false},
		{"invalid-fsyncobjectfiles-before-valid", "[core]\nfsyncobjectfiles=invalid\n[pager]\ncolor=true\n", false},
		{"invalid-before-safecrlf-warn", "[pager]\ncolor=invalid\n[core]\nsafecrlf=warn\n", false},
		{"invalid-before-autocrlf-input", "[pager]\ncolor=invalid\n[core]\nautocrlf=input\n", false},
		{"safecrlf-warn-autocrlf-input-control", "[pager]\ncolor=true\n[core]\nsafecrlf=WaRn\nautocrlf=InPuT\n", true},
		{"safecrlf-input-remains-invalid", "[pager]\ncolor=true\n[core]\nsafecrlf=input\n", false},
		{"autocrlf-warn-remains-invalid", "[pager]\ncolor=true\n[core]\nautocrlf=warn\n", false},
		{"invalid-safecrlf-before-valid", "[core]\nsafecrlf=invalid\n[pager]\ncolor=true\n", false},
		{"invalid-autocrlf-before-valid", "[core]\nautocrlf=invalid\n[pager]\ncolor=true\n", false},
		{"valid-duplicates", "[pager]\ncolor=true\ncolor=false\n", true},
		{"invalid-before-valid-ignorecase", "[pager]\ncolor=invalid\n[core]\nignorecase=true\n", false},
		{"invalid-ignorecase-before-valid", "[core]\nignorecase=invalid\n[pager]\ncolor=true\n", false},
		{"invalid-before-valid-filemode", "[pager]\ncolor=invalid\n[core]\nfilemode=true\n", false},
		{"invalid-filemode-before-valid", "[core]\nfilemode=invalid\n[pager]\ncolor=true\n", false},
	} {
		cases = append(cases, includeCase{name: "pagercolor-" + c.name, setup: includeGlobal(c.body, nil), ok: c.ok})
	}
	for _, c := range []struct {
		name, body string
		ok         bool
	}{
		{"invalid-before-valid-lockfilepid", "[pager]\ncolor=invalid\n[core]\nlockfilepid=true\n", false},
		{"invalid-lockfilepid-before-valid", "[core]\nlockfilepid=invalid\n[pager]\ncolor=true\n", false},
		{"core-namespace-control", "[core]\ncolor=invalid\n", true},
	} {
		// Git before 2.54 ignores only the prior lockfilePid guard in this case.
		cases = append(cases, includeCase{name: "pagercolor-" + c.name, setup: includeGlobal(c.body, nil), ok: c.ok, gitBefore54OK: c.name == "invalid-lockfilepid-before-valid"})
	}
	for _, c := range []struct {
		name, body string
		ok         bool
	}{
		{"invalid-before-valid-sparseoutside", "[pager]\ncolor=invalid\n[sparse]\nexpectfilesoutsideofpatterns=true\n", false},
		{"invalid-sparseoutside-before-valid", "[sparse]\nexpectfilesoutsideofpatterns=invalid\n[pager]\ncolor=true\n", false},
		{"invalid-alias-before-valid", "[color]\npager=invalid\n[pager]\ncolor=true\n", false},
		{"invalid-before-valid-alias", "[pager]\ncolor=invalid\n[color]\npager=true\n", false},
	} {
		cases = append(cases, includeCase{name: "pagercolor-" + c.name, setup: includeGlobal(c.body, nil), ok: c.ok})
	}
	for _, value := range []string{"yes", "No", "ON", "0", "1", "-1", "01", "0x1", "1k", "2m", "1g", "-2147483648", "2147483647", "-2147483649", "2g", "1.0", "truth", "1 ", "0b10"} {
		// A trailing space survives quoting so Git's integer parser must reject it.
		ok := value != "-2147483649" && value != "2g" && value != "1.0" && value != "truth" && value != "1 "
		cases = append(cases, includeCase{name: "pagercolor-value-" + strings.ReplaceAll(value, " ", "space"), setup: commandPairs("pager.color", value), ok: ok})
	}
	cases = append(cases,
		includeCase{name: "pagercolor-prefix-invalid", setup: prefixGlobal("[pager]\ncolor=invalid\n"), gitPrefix: true},
		includeCase{name: "pagercolor-prefix-valid", setup: prefixGlobal("[pager]\ncolor=true\n"), gitPrefix: true, ok: true},
		includeCase{name: "pagercolor-count-prefix-invalid", gitPrefix: true, setup: func(t *testing.T, f includeFixture) map[string]*string {
			target := prefixTarget(t, f, "[pager]\ncolor=invalid\n")
			return commandPairs("include.path", "%(prefix)/"+prefixRest(f.prefix, target))(t, f)
		}},
		includeCase{name: "pagercolor-parameters-prefix-invalid", gitPrefix: true, setup: func(t *testing.T, f includeFixture) map[string]*string {
			target := prefixTarget(t, f, "[pager]\ncolor=invalid\n")
			return commandParameters("'include.path'="+commandQuote("%(prefix)/"+prefixRest(f.prefix, target)))(t, f)
		}},
		includeCase{name: "pagercolor-command-valueless", setup: commandParameters("'pager.color'"), ok: true},
		includeCase{name: "pagercolor-command-invalid-before-valid", setup: commandParameters("'pager.color=invalid' 'pager.color=true'")},
		includeCase{name: "pagercolor-count-before-parameters", setup: func(t *testing.T, f includeFixture) map[string]*string {
			e := commandPairs("pager.color", "invalid")(t, f)
			e["GIT_CONFIG_PARAMETERS"] = globalValue("'pager.color=true'")
			return e
		}},
		includeCase{name: "pagercolor-global-before-command", setup: func(t *testing.T, f includeFixture) map[string]*string {
			e := includeGlobal("[pager]\ncolor=invalid\n", nil)(t, f)
			for k, v := range commandPairs("pager.color", "true")(t, f) {
				e[k] = v
			}
			return e
		}},
	)
	for _, source := range []string{"global", "home", "xdg", "system", "system-include", "repository", "repository-include", "worktree", "worktree-include", "worktree-disabled", "count", "parameters", "command-include", "parameters-include", "global-nested"} {
		for _, v := range []struct {
			name, value string
			ok          bool
		}{{"invalid", "invalid", false}, {"true", "ON", true}, {"warn", "warn", false}, {"input", "input", false}} {
			setup := pagerColorSource(source, "[color]\npager="+v.value+"\n", v.value)
			if source == "count" {
				setup = commandPairs("COLOR.PAGER", v.value)
			}
			if source == "parameters" {
				setup = commandParameters("'CoLoR.PaGeR'=" + commandQuote(v.value))
			}
			cases = append(cases, includeCase{name: "pagercolor-alias-" + source + "-" + v.name, setup: setup, ok: v.ok || source == "worktree-disabled"})
		}
	}
	for _, c := range []struct {
		name, body string
		ok         bool
	}{
		{"valueless", "[color]\npager\n", true}, {"subsection", "[color \"other\"]\npager=invalid\n", true},
		{"upper-key", "[COLOR]\nPAGER=invalid\n", false}, {"unrelated-key", "[sample]\npager=invalid\n", true},
		{"invalid-before-valid", "[color]\npager=invalid\npager=true\n", false},
		{"safecrlf-autocrlf-control", "[color]\npager=true\n[core]\nsafecrlf=WaRn\nautocrlf=InPuT\n", true},
	} {
		cases = append(cases, includeCase{name: "pagercolor-alias-" + c.name, setup: includeGlobal(c.body, nil), ok: c.ok})
	}
	cases = append(cases,
		includeCase{name: "pagercolor-alias-prefix-invalid", gitPrefix: true, setup: prefixGlobal("[color]\npager=invalid\n")},
		includeCase{name: "pagercolor-alias-prefix-valid", gitPrefix: true, setup: prefixGlobal("[color]\npager=true\n"), ok: true},
	)
	return cases
}()

// Options.GitRoot must apply the same full-read admission with an explicit
// prefix, including nested and command-line includes. Expected results come
// from real Git before any native API runs.
func TestGitRootPagerColorOptionsMatchesGit(t *testing.T) {
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git is not installed")
	}
	host := readHostGit(t, git)
	prefix := hostGitPrefix(t, git)
	for _, c := range pagerColorCases {
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
