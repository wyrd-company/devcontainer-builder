// relationships:
//   references: [git_config_full, git_config_include_test, git_command_config_test]

package workspace

import (
	"path/filepath"
	"strings"
	"testing"
)

// ignoreCaseSource selects the same key through each full-read source. All
// sources run through the native contract and eight real-Git discovery routes,
// including Resolve's source, target and workspace folder assertions.
func ignoreCaseSource(source, body, value string) func(*testing.T, includeFixture) map[string]*string {
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
			return commandPairs("CORE.IGNORECASE", value)(t, f)
		case "parameters":
			return commandParameters("'CoRe.IgNoReCaSe'="+commandQuote(value))(t, f)
		case "command-include":
			return commandInclude(body, false)(t, f)
		case "parameters-include":
			return commandInclude(body, true)(t, f)
		default:
			return includeGlobal(includeLine("one.cfg"), map[string]string{"global/one.cfg": includeLine("two.cfg"), "global/two.cfg": body})(t, f)
		}
	}
}

var ignoreCaseCases = func() []includeCase {
	var cases []includeCase
	for _, source := range []string{"global", "home", "xdg", "system", "system-include", "repository", "repository-include", "worktree", "worktree-include", "worktree-disabled", "count", "parameters", "command-include", "parameters-include", "global-nested"} {
		for _, value := range []struct {
			name, value string
			ok          bool
		}{
			{"invalid", "invalid", false}, {"true", "true", true}, {"false", "OFF", true}, {"empty", "", true}, {"integer", "-0x1", true}, {"overflow", "2147483648", false},
		} {
			cases = append(cases, includeCase{name: "ignorecase-" + source + "-" + value.name, setup: ignoreCaseSource(source, "[core]\nignorecase="+value.value+"\n", value.value), ok: value.ok || source == "worktree-disabled"})
		}
	}
	for _, c := range []struct {
		name, body string
		ok         bool
	}{
		{"valueless", "[core]\nignorecase\n", true},
		{"subsection", "[core \"other\"]\nignorecase=invalid\n", true},
		{"old-subsection", "[core.other]\nignorecase=invalid\n", true},
		{"unrelated-key", "[sample]\nignorecase=invalid\n", true},
		{"upper-key", "[CORE]\nIGNORECASE=invalid\n", false},
		{"invalid-before-valid", "[core]\nignorecase=invalid\nignorecase=true\n", false},
		{"valid-before-invalid", "[core]\nignorecase=true\nignorecase=invalid\n", false},
		{"valid-duplicates", "[core]\nignorecase=true\nignorecase=false\n", true},
	} {
		cases = append(cases, includeCase{name: "ignorecase-" + c.name, setup: includeGlobal(c.body, nil), ok: c.ok})
	}
	for _, value := range []string{"yes", "No", "ON", "0", "1", "-1", "01", "0x1", "1k", "2m", "1g", "-2147483648", "2147483647", "-2147483649", "2g", "1.0", "truth", "1 ", "0b10"} {
		// A trailing space survives quoting so Git's integer parser must reject it.
		ok := value != "-2147483649" && value != "2g" && value != "1.0" && value != "truth" && value != "1 "
		cases = append(cases, includeCase{name: "ignorecase-value-" + strings.ReplaceAll(value, " ", "space"), setup: commandPairs("core.ignorecase", value), ok: ok})
	}
	cases = append(cases,
		includeCase{name: "ignorecase-prefix-invalid", setup: prefixGlobal("[core]\nignorecase=invalid\n"), gitPrefix: true},
		includeCase{name: "ignorecase-prefix-valid", setup: prefixGlobal("[core]\nignorecase=true\n"), gitPrefix: true, ok: true},
		includeCase{name: "ignorecase-count-prefix-invalid", gitPrefix: true, setup: func(t *testing.T, f includeFixture) map[string]*string {
			target := prefixTarget(t, f, "[core]\nignorecase=invalid\n")
			return commandPairs("include.path", "%(prefix)/"+prefixRest(f.prefix, target))(t, f)
		}},
		includeCase{name: "ignorecase-parameters-prefix-invalid", gitPrefix: true, setup: func(t *testing.T, f includeFixture) map[string]*string {
			target := prefixTarget(t, f, "[core]\nignorecase=invalid\n")
			return commandParameters("'include.path'="+commandQuote("%(prefix)/"+prefixRest(f.prefix, target)))(t, f)
		}},
		includeCase{name: "ignorecase-command-valueless", setup: commandParameters("'core.ignorecase'"), ok: true},
		includeCase{name: "ignorecase-command-invalid-before-valid", setup: commandParameters("'core.ignorecase=invalid' 'core.ignorecase=true'")},
		includeCase{name: "ignorecase-count-before-parameters", setup: func(t *testing.T, f includeFixture) map[string]*string {
			e := commandPairs("core.ignorecase", "invalid")(t, f)
			e["GIT_CONFIG_PARAMETERS"] = globalValue("'core.ignorecase=true'")
			return e
		}},
		includeCase{name: "ignorecase-global-before-command", setup: func(t *testing.T, f includeFixture) map[string]*string {
			e := includeGlobal("[core]\nignorecase=invalid\n", nil)(t, f)
			for k, v := range commandPairs("core.ignorecase", "true")(t, f) {
				e[k] = v
			}
			return e
		}},
	)
	return cases
}()
