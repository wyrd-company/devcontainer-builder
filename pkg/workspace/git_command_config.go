// relationships:
//   references: [git, git_config_include, git_config_syntax, ../../reference/cli/src/spec-common/git]

package workspace

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// gitCommandConfigRead follows git_config_from_parameters in Git's config.c.
// Git reads the count-indexed environment pairs, then GIT_CONFIG_PARAMETERS,
// last in its full config read after setup. Invalid syntax or an include
// failure stops the probe. Other keys' typed values and includeIf conditions
// are not applied. No file owns a command-line include, so its expanded path
// must be absolute; includes inside that file keep their own relative base.
func gitCommandConfigRead(cwd, prefix string) bool {
	folder := newGitConfigCwd(cwd)
	defer folder.close()
	pair := func(key string, value *string) bool {
		name, ok := gitCommandConfigKey(key)
		if !ok {
			return false
		}
		if name != "include.path" {
			return true
		}
		if value == nil {
			return false
		}
		path, follow, ok := gitIncludePath(*value, prefix)
		if !ok || !follow {
			return ok
		}
		return filepath.IsAbs(path) && gitConfigIncludeRead(folder, path, 0, prefix)
	}
	if raw, set := os.LookupEnv("GIT_CONFIG_COUNT"); set {
		count, ok := gitCommandConfigCount(raw)
		if !ok {
			return false
		}
		for i := uint64(0); i < count; i++ {
			index := strconv.FormatUint(i, 10)
			key, keySet := os.LookupEnv("GIT_CONFIG_KEY_" + index)
			value, valueSet := os.LookupEnv("GIT_CONFIG_VALUE_" + index)
			if !keySet || !valueSet || !pair(key, &value) {
				return false
			}
		}
	}
	return gitCommandConfigParameters(os.Getenv("GIT_CONFIG_PARAMETERS"), pair)
}

// gitCommandConfigCount follows strtoul(base 10), then Git's INT_MAX check.
// strtoul uses C whitespace (also VT and FF), an optional sign, and unsigned
// negation; it accepts an empty string but no other digit-free input. An
// overflow saturates before negation. Git does not inspect errno.
func gitCommandConfigCount(raw string) (uint64, bool) {
	if raw == "" {
		return 0, true
	}
	s := strings.TrimLeft(raw, " \t\n\r\v\f")
	negative := strings.HasPrefix(s, "-")
	if negative || strings.HasPrefix(s, "+") {
		s = s[1:]
	}
	count, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, false
	}
	if negative {
		count = -count
	}
	return count, count <= 1<<31-1
}

// gitCommandConfigKey follows do_parse_config_key: section and variable use
// ASCII key characters, the variable starts with a letter, and a subsection
// keeps its spelling and can contain anything except LF.
func gitCommandConfigKey(key string) (string, bool) {
	last := strings.LastIndexByte(key, '.')
	if last <= 0 || last == len(key)-1 {
		return "", false
	}
	name := []byte(key)
	dot := false
	for i, c := range name {
		if c == '.' {
			dot = true
		}
		if !dot || i > last {
			if !gitKeyChar(c) || i == last+1 && !gitAlpha(c) {
				return "", false
			}
			name[i] = gitLower(c)
		} else if c == '\n' {
			return "", false
		}
	}
	return string(name), true
}

// gitCommandConfigParameters follows parse_config_env_list. It accepts both
// 'key=value' and 'key'='value', including valueless keys, and uses Git's
// ASCII whitespace between entries. Leading whitespace is not admitted.
func gitCommandConfigParameters(raw string, pair func(string, *string) bool) bool {
	for raw != "" {
		key, rest, ok := gitConfigDequote(raw)
		if !ok {
			return false
		}
		var value *string
		switch {
		case rest == "" || gitSpace(rest[0]):
			if k, v, found := strings.Cut(key, "="); found {
				key, value = k, &v
			}
		case rest[0] == '=':
			rest = rest[1:]
			if strings.HasPrefix(rest, "'") {
				v, next, ok := gitConfigDequote(rest)
				if !ok || next != "" && !gitSpace(next[0]) {
					return false
				}
				value, rest = &v, next
			} else if rest != "" && !gitSpace(rest[0]) {
				return false
			}
		default:
			return false
		}
		if !pair(key, value) {
			return false
		}
		for rest != "" && gitSpace(rest[0]) {
			rest = rest[1:]
		}
		raw = rest
	}
	return true
}

// gitConfigDequote follows sq_dequote_step in Git's quote.c: a single-quoted
// segment can resume after a backslashed apostrophe or exclamation mark.
// It returns the unconsumed suffix so its caller can validate the separator.
func gitConfigDequote(raw string) (string, string, bool) {
	if !strings.HasPrefix(raw, "'") {
		return "", "", false
	}
	var value strings.Builder
	for i := 1; i < len(raw); i++ {
		if raw[i] != '\'' {
			value.WriteByte(raw[i])
			continue
		}
		rest := raw[i+1:]
		if len(rest) >= 3 && rest[0] == '\\' && (rest[1] == '\'' || rest[1] == '!') && rest[2] == '\'' {
			value.WriteByte(rest[1])
			i += 3
			continue
		}
		return value.String(), rest, true
	}
	return "", "", false
}
