// relationships:
//
//	references: [README, ../../reference/cli/src/spec-node/devContainersSpecCLI]
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// This reader deliberately accepts the pinned upstream's declarative yargs
// tables, not arbitrary TypeScript. Unrecognized registrations/table entries
// are errors so a reference update cannot silently shrink the denominator.
var (
	optionFunction = regexp.MustCompile(`(?ms)^(?:export )?function (\w*Options)\([^\n]*\) \{\n(.*?)^}\r?$`)
	commandLine    = regexp.MustCompile(`(?m)^\s*y\.command\(([^\n]+)`)
	commandBuilder = regexp.MustCompile(`, (\w+Options), \w+\);$`)
	commandName    = regexp.MustCompile(`^'([a-z-]+)(?:[^']*)'`)
	commandGroup   = regexp.MustCompile(`^'([a-z-]+)', .*=> \{$`)
	optionEntry    = regexp.MustCompile(`^\s*(?:'([a-z-]+)'|([a-z][a-z-]*)):\s*\{.*\},?\s*(?://.*)?$`)
	optionAlias    = regexp.MustCompile(`\balias:\s*'([^']+)'`)
	optionWrapper  = regexp.MustCompile(`^\s*return (\w+Options)\(y, '[a-z]+'\);\s*$`)
)

type sourceOption struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
	Hidden  bool     `json:"hidden,omitempty"`
	Source  string   `json:"source"`
}

type optionBuilder struct {
	body, source string
	line         int
}

func readUpstreamOptions(root string) (map[string][]sourceOption, error) {
	base := filepath.Join(root, "reference/cli/src/spec-node")
	builders := map[string]optionBuilder{}
	err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".ts") {
			return nil
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, match := range optionFunction.FindAllStringSubmatchIndex(string(body), -1) {
			name := string(body[match[2]:match[3]])
			if _, exists := builders[name]; exists {
				return fmt.Errorf("duplicate option builder %s", name)
			}
			rel, _ := filepath.Rel(root, path)
			builders[name] = optionBuilder{string(body[match[4]:match[5]]), filepath.ToSlash(rel), strings.Count(string(body[:match[4]]), "\n") + 1}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	entry, err := os.ReadFile(filepath.Join(base, "devContainersSpecCLI.ts"))
	if err != nil {
		return nil, err
	}
	return upstreamCommandOptions(string(entry), builders)
}

func upstreamCommandOptions(entry string, builders map[string]optionBuilder) (map[string][]sourceOption, error) {
	if strings.Count(entry, "y.command(") != len(commandLine.FindAllStringSubmatch(entry, -1)) {
		return nil, fmt.Errorf("unsupported multiline command registration")
	}
	if !strings.Contains(entry, ".version(version)") {
		return nil, fmt.Errorf("unrecognized upstream global options")
	}
	global := []sourceOption{{Name: "help", Source: "yargs built-in help"}, {Name: "version", Source: "reference/cli/src/spec-node/devContainersSpecCLI.ts (.version)"}}
	out := map[string][]sourceOption{"": global}
	group := ""
	for _, line := range strings.Split(entry, "\n") {
		if strings.TrimSpace(line) == "});" {
			group = ""
		}
		match := commandLine.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		if g := commandGroup.FindStringSubmatch(match[1]); g != nil {
			if group != "" {
				return nil, fmt.Errorf("unsupported nested command group")
			}
			if _, exists := out[g[1]]; exists {
				return nil, fmt.Errorf("duplicate command group %s", g[1])
			}
			group = g[1]
			out[group] = append([]sourceOption(nil), global...)
			continue
		}
		b := commandBuilder.FindStringSubmatch(match[1])
		if b == nil {
			return nil, fmt.Errorf("unrecognized upstream registration: %s", line)
		}
		name := ""
		if n := commandName.FindStringSubmatch(match[1]); n != nil {
			name = n[1]
		} else if strings.HasPrefix(match[1], "restArgs ? ['exec', '*'] : ['exec <cmd> [args..]']") {
			name = "exec"
		}
		if name == "" {
			return nil, fmt.Errorf("unrecognized upstream command: %s", line)
		}
		name = strings.TrimSpace(group + " " + name)
		if _, exists := out[name]; exists {
			return nil, fmt.Errorf("duplicate command %s", name)
		}
		options, err := builderOptions(b[1], builders, map[string]bool{})
		if err != nil {
			return nil, err
		}
		out[name] = append(options, global...)
	}
	if len(out) <= 1 {
		return nil, fmt.Errorf("no upstream commands found")
	}
	return out, nil
}

func builderOptions(name string, builders map[string]optionBuilder, seen map[string]bool) ([]sourceOption, error) {
	b, ok := builders[name]
	if !ok || seen[name] {
		return nil, fmt.Errorf("missing or cyclic option builder %s", name)
	}
	seen[name] = true
	if wrapper := optionWrapper.FindStringSubmatch(b.body); wrapper != nil {
		return builderOptions(wrapper[1], builders, seen)
	}
	if strings.Contains(b.body, ".option(") {
		return nil, fmt.Errorf("unsupported individual option registration in %s", name)
	}
	parts := strings.Split(b.body, ".options({")
	if len(parts) != 2 {
		return nil, fmt.Errorf("unrecognized options in %s", name)
	}
	line := b.line + strings.Count(parts[0], "\n")
	out := []sourceOption{}
	closed := false
	for _, text := range strings.Split(parts[1], "\n") {
		line++
		trim := strings.TrimSpace(text)
		if strings.HasPrefix(trim, "})") {
			closed = true
			break
		}
		if trim == "" || strings.HasPrefix(trim, "//") {
			continue
		}
		m := optionEntry.FindStringSubmatch(text)
		if m == nil || !singleOptionObject(text) {
			return nil, fmt.Errorf("unrecognized option entry %s:%d", b.source, line-1)
		}
		option := sourceOption{Name: m[1] + m[2], Hidden: strings.Contains(text, "hidden: true"), Source: fmt.Sprintf("%s:%d", b.source, line-1)}
		if alias := optionAlias.FindStringSubmatch(text); alias != nil {
			option.Aliases = []string{alias[1]}
		}
		for _, previous := range out {
			if previous.Name == option.Name {
				return nil, fmt.Errorf("duplicate option %s in %s", option.Name, name)
			}
		}
		out = append(out, option)
	}
	if !closed || len(out) == 0 {
		return nil, fmt.Errorf("incomplete option table %s", name)
	}
	return out, nil
}

func sortedCommands[T any](commands map[string]T) []string {
	names := make([]string, 0, len(commands))
	for name := range commands {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// singleOptionObject enforces the supported one-entry-per-line table shape.
// Quoted descriptions (including escaped quotes and template strings) can carry
// braces; a second object property on the same line must not be lost silently.
func singleOptionObject(text string) bool {
	start := strings.IndexByte(text, '{')
	if start < 0 {
		return false
	}
	depth := 0
	var quote byte
	for i := start; i < len(text); i++ {
		c := text[i]
		if quote != 0 {
			if c == '\\' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"', '`':
			quote = c
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				rest := strings.TrimSpace(text[i+1:])
				rest = strings.TrimSpace(strings.TrimPrefix(rest, ","))
				return rest == "" || strings.HasPrefix(rest, "//")
			}
		}
	}
	return false
}
