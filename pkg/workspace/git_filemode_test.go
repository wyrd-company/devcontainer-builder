// relationships:
//   references: [git_config_full, git_config_include_test, git_command_config_test]

package workspace

import (
	"path/filepath"
	"strings"
	"testing"
)

// fileModeSource selects the same key through each full-read source. All
// sources run through the native contract and eight real-Git discovery routes,
// including Resolve's source, target and workspace folder assertions.
func fileModeSource(source, body, value string) func(*testing.T, includeFixture) map[string]*string {
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
			return commandPairs("CORE.FILEMODE", value)(t, f)
		case "parameters":
			return commandParameters("'CoRe.FiLeMoDe'="+commandQuote(value))(t, f)
		case "command-include":
			return commandInclude(body, false)(t, f)
		case "parameters-include":
			return commandInclude(body, true)(t, f)
		default:
			return includeGlobal(includeLine("one.cfg"), map[string]string{"global/one.cfg": includeLine("two.cfg"), "global/two.cfg": body})(t, f)
		}
	}
}

var fileModeCases = func() []includeCase {
	var cases []includeCase
	for _, source := range []string{"global", "home", "xdg", "system", "system-include", "repository", "repository-include", "worktree", "worktree-include", "worktree-disabled", "count", "parameters", "command-include", "parameters-include", "global-nested"} {
		for _, value := range []struct {
			name, value string
			ok          bool
		}{
			{"invalid", "invalid", false}, {"true", "true", true}, {"false", "OFF", true}, {"empty", "", true}, {"integer", "-0x1", true}, {"overflow", "2147483648", false},
		} {
			cases = append(cases, includeCase{name: "filemode-" + source + "-" + value.name, setup: fileModeSource(source, "[core]\nfilemode="+value.value+"\n", value.value), ok: value.ok || source == "worktree-disabled"})
		}
	}
	for _, c := range []struct {
		name, body string
		ok         bool
	}{
		{"valueless", "[core]\nfilemode\n", true},
		{"subsection", "[core \"other\"]\nfilemode=invalid\n", true},
		{"old-subsection", "[core.other]\nfilemode=invalid\n", true},
		{"unrelated-key", "[sample]\nfilemode=invalid\n", true},
		{"upper-key", "[CORE]\nFILEMODE=invalid\n", false},
		{"invalid-before-valid", "[core]\nfilemode=invalid\nfilemode=true\n", false},
		{"valid-before-invalid", "[core]\nfilemode=true\nfilemode=invalid\n", false},
		{"valid-duplicates", "[core]\nfilemode=true\nfilemode=false\n", true},
		{"invalid-before-valid-ignorecase", "[core]\nfilemode=invalid\nignorecase=true\n", false},
		{"invalid-ignorecase-before-valid", "[core]\nignorecase=invalid\nfilemode=true\n", false},
	} {
		cases = append(cases, includeCase{name: "filemode-" + c.name, setup: includeGlobal(c.body, nil), ok: c.ok})
	}
	for _, value := range []string{"yes", "No", "ON", "0", "1", "-1", "01", "0x1", "1k", "2m", "1g", "-2147483648", "2147483647", "-2147483649", "2g", "1.0", "truth", "1 ", "0b10"} {
		// A trailing space survives quoting so Git's integer parser must reject it.
		ok := value != "-2147483649" && value != "2g" && value != "1.0" && value != "truth" && value != "1 "
		cases = append(cases, includeCase{name: "filemode-value-" + strings.ReplaceAll(value, " ", "space"), setup: commandPairs("core.filemode", value), ok: ok})
	}
	cases = append(cases,
		includeCase{name: "filemode-prefix-invalid", setup: prefixGlobal("[core]\nfilemode=invalid\n"), gitPrefix: true},
		includeCase{name: "filemode-prefix-valid", setup: prefixGlobal("[core]\nfilemode=true\n"), gitPrefix: true, ok: true},
		includeCase{name: "filemode-count-prefix-invalid", gitPrefix: true, setup: func(t *testing.T, f includeFixture) map[string]*string {
			target := prefixTarget(t, f, "[core]\nfilemode=invalid\n")
			return commandPairs("include.path", "%(prefix)/"+prefixRest(f.prefix, target))(t, f)
		}},
		includeCase{name: "filemode-parameters-prefix-invalid", gitPrefix: true, setup: func(t *testing.T, f includeFixture) map[string]*string {
			target := prefixTarget(t, f, "[core]\nfilemode=invalid\n")
			return commandParameters("'include.path'="+commandQuote("%(prefix)/"+prefixRest(f.prefix, target)))(t, f)
		}},
		includeCase{name: "filemode-command-valueless", setup: commandParameters("'core.filemode'"), ok: true},
		includeCase{name: "filemode-command-invalid-before-valid", setup: commandParameters("'core.filemode=invalid' 'core.filemode=true'")},
		includeCase{name: "filemode-count-before-parameters", setup: func(t *testing.T, f includeFixture) map[string]*string {
			e := commandPairs("core.filemode", "invalid")(t, f)
			e["GIT_CONFIG_PARAMETERS"] = globalValue("'core.filemode=true'")
			return e
		}},
		includeCase{name: "filemode-global-before-command", setup: func(t *testing.T, f includeFixture) map[string]*string {
			e := includeGlobal("[core]\nfilemode=invalid\n", nil)(t, f)
			for k, v := range commandPairs("core.filemode", "true")(t, f) {
				e[k] = v
			}
			return e
		}},
	)
	return cases
}()
