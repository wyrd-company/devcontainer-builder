// relationships:
//
// references: [stage, ../features/documentation]
package cli

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/templates"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
)

// yargs-parser consumes negative decimal strings as option values.
var documentationNegativeValue = regexp.MustCompile(`^-([0-9]+(\.[0-9]+)?|\.[0-9]+)$`)
var documentationShortOption = regexp.MustCompile(`^(-|--)[^-]`)

func (a *application) featuresDocumentationCommand() *cobra.Command {
	return a.documentationCommand(false)
}

func (a *application) templatesDocumentationCommand() *cobra.Command {
	return a.documentationCommand(true)
}

func (a *application) documentationCommand(template bool) *cobra.Command {
	var opts features.DocumentationOptions
	var projects, registries, namespaces, owners, repos []string
	cmd := &cobra.Command{Use: "generate-docs", Short: "Generate READMEs for local Features", Args: func(cmd *cobra.Command, args []string) error {
		// yargs ignores the delimiter tail, but rejects ordinary positionals.
		if end := cmd.ArgsLenAtDash(); end >= 0 {
			args = args[:end]
		}
		return cobra.NoArgs(cmd, args)
	}}
	cmd.Flags().SetNormalizeFunc(func(_ *pflag.FlagSet, name string) pflag.NormalizedName {
		switch name {
		case "projectFolder":
			name = "project-folder"
		case "githubOwner":
			name = "github-owner"
		case "githubRepo":
			name = "github-repo"
		case "logLevel":
			name = "log-level"
		}
		return pflag.NormalizedName(name)
	})
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if len(projects) != 1 {
			return fmt.Errorf("project-folder must be a single path")
		}
		opts.ProjectFolder = projects[0]
		opts.Registry = strings.Join(registries, ",")
		opts.Namespace = strings.Join(namespaces, ",")
		opts.GitHubOwner = strings.Join(owners, ",")
		opts.GitHubRepo = strings.Join(repos, ",")
		opts.Logger = func(format string, args ...any) {
			// Preserve the library's diagnostic severity through the shared logger.
			text := fmt.Sprintf(format, args...)
			level := "info"
			for _, severity := range []string{"warning:", "error:"} {
				if strings.HasPrefix(text, severity) {
					level = strings.TrimSuffix(severity, ":")
					if level == "warning" {
						level = "warn"
					}
					text = strings.TrimPrefix(text, severity+" ")
					break
				}
			}
			a.diagnostic(level, text)
		}
		if template {
			a.exportStdout = true
			return templates.GenerateDocumentation(cmd.Context(), templates.DocumentationOptions{ProjectFolder: opts.ProjectFolder, GitHubOwner: opts.GitHubOwner, GitHubRepo: opts.GitHubRepo, GitHubOwnerValue: opts.GitHubOwnerValue, GitHubRepoValue: opts.GitHubRepoValue, Logger: opts.Logger})
		}
		return features.GenerateDocumentation(cmd.Context(), opts)
	}
	cmd.Flags().StringArrayVarP(&projects, "project-folder", "p", []string{"."}, "Folder containing Feature directories")
	if !template {
		cmd.Flags().StringArrayVarP(&registries, "registry", "r", []string{"ghcr.io"}, "OCI registry name")
		cmd.Flags().StringArrayVarP(&namespaces, "namespace", "n", nil, "Feature collection namespace")
		_ = cmd.MarkFlagRequired("namespace")
	}
	cmd.Flags().StringArrayVar(&owners, "github-owner", nil, "GitHub owner for metadata links")
	cmd.Flags().StringArrayVar(&repos, "github-repo", nil, "GitHub repository for metadata links")
	for name, target := range map[string]**features.DocumentationValue{
		"registry": &opts.RegistryValue, "namespace": &opts.NamespaceValue,
		"github-owner": &opts.GitHubOwnerValue, "github-repo": &opts.GitHubRepoValue,
	} {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			continue
		}
		flag.Value = &documentationFlagValue{Value: flag.Value, target: target}
	}
	// yargs supplies the declared default when a string option has no value.
	// Keep explicit empty strings distinct from absent values.
	for name, value := range map[string]string{"project-folder": ".", "registry": "ghcr.io", "namespace": "", "github-owner": "", "github-repo": ""} {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			continue
		}
		if flag.Annotations == nil {
			flag.Annotations = map[string][]string{}
		}
		flag.Annotations["documentation-no-value"] = []string{value}
	}
	if template {
		// Shadow the inherited flag so this command can adapt yargs' default
		// without changing flag parsing on other commands.
		cmd.Flags().StringVar(&a.logLevel, "log-level", "info", "Diagnostic level")
		cmd.Flags().Lookup("log-level").Annotations = map[string][]string{"documentation-no-value": {"info"}}
		cmd.Short = "Generate READMEs for local Templates"
	}
	return cmd
}

var documentationAttachedNumber = regexp.MustCompile(`^-?\d+(\.\d*)?(e-?\d+)?$`)
var documentationWord = regexp.MustCompile(`^[A-Za-z0-9_]$`)

// yargs-parser expands each component of a dashed key independently. Preserve
// mixed case, repeated separators, leading hyphens and full Unicode casing.
func documentationCamelProperty(path string) string {
	parts := strings.Split(path, ".")
	lowerCase, upperCase := cases.Lower(language.Und), cases.Upper(language.Und)
	for i, part := range parts {
		lower, upper := lowerCase.String(part), upperCase.String(part)
		if part == lower || part == upper {
			part = lower
		}
		start := len(part) - len(strings.TrimLeft(part, "-"))
		var text strings.Builder
		nextUpper := false
		for index, ch := range part[start:] {
			letter := string(ch)
			if nextUpper {
				letter = upperCase.String(letter)
				nextUpper = false
			}
			if index+start != 0 && (letter == "-" || letter == "_") {
				nextUpper = true
			} else if letter != "-" && letter != "_" {
				text.WriteString(letter)
			}
		}
		parts[i] = text.String()
	}
	return strings.Join(parts, ".")
}

// Keep pflag's ordinary string parsing and required-flag checks while retaining
// the structured argument for the library's deferred publication conversion.
type documentationFlagValue struct {
	pflag.Value
	target **features.DocumentationValue
}

func documentationScalar(flag *pflag.Flag, text string) {
	value, ok := flag.Value.(*documentationFlagValue)
	if !ok {
		return
	}
	next := features.DocumentationValue{Text: text}
	if *value.target == nil {
		*value.target = &next
	} else if (*value.target).Elements != nil {
		(*value.target).Elements = append((*value.target).Elements, next)
	} else {
		*value.target = &features.DocumentationValue{Elements: []features.DocumentationValue{**value.target, next}}
	}
}

func documentationProperty(flag *pflag.Flag, arg string, next []string) {
	value, ok := flag.Value.(*documentationFlagValue)
	if !ok {
		return
	}
	name, text, equals := strings.Cut(arg, "=")
	_, path, _ := strings.Cut(name, ".")
	if !equals && len(next) > 0 && (!strings.HasPrefix(next[0], "-") || strings.HasPrefix(arg, "--") && documentationNegativeValue.MatchString(next[0])) {
		text = next[0]
	}
	object := *value.target
	if object == nil || object.Properties == nil {
		object = &features.DocumentationValue{Properties: map[string]features.DocumentationValue{}}
		if *value.target == nil {
			*value.target = object
		} else if (*value.target).Elements != nil {
			(*value.target).Elements = append((*value.target).Elements, *object)
		} else {
			*value.target = &features.DocumentationValue{Elements: []features.DocumentationValue{**value.target, *object}}
		}
	}
	set := func(path string) {
		properties := object.Properties
		keys := strings.Split(path, ".")
		for i, key := range keys {
			// yargs-parser sanitizes this key rather than changing a prototype.
			if key == "__proto__" {
				key = "___proto___"
			}
			if i == len(keys)-1 {
				item := features.DocumentationValue{Text: text}
				if old, exists := properties[key]; exists {
					if old.Elements != nil {
						item.Elements = append(old.Elements, item)
					} else {
						item.Elements = []features.DocumentationValue{old, item}
					}
				}
				properties[key] = item
				return
			}
			child := properties[key]
			if child.Properties == nil {
				child = features.DocumentationValue{Properties: map[string]features.DocumentationValue{}}
				properties[key] = child
			}
			properties = child.Properties
		}
	}
	set(path)
	// yargs expands dashed property names as well as dashed option names.
	camel := path
	if strings.Contains(path, "-") {
		camel = documentationCamelProperty(path)
	}
	// Full-key expansion writes the camel-case root. First-element aliases
	// receive only the original property path, as in yargs-parser's setArg.
	root, _, _ := strings.Cut(strings.TrimLeft(name, "-"), ".")
	if camel != path && root == flag.Name && !strings.Contains(root, "-") {
		set(camel)
	}
}

func documentationDottedFlag(cmd *cobra.Command, arg string) *pflag.Flag {
	if cmd.Name() != "generate-docs" || cmd.Parent() == nil || (cmd.Parent().Name() != "features" && cmd.Parent().Name() != "templates") {
		return nil
	}
	name, _, _ := strings.Cut(arg, "=")
	var flag *pflag.Flag
	if strings.HasPrefix(name, "--") {
		if key, _, dotted := strings.Cut(name[2:], "."); dotted {
			flag = cmd.Flags().Lookup(key)
		}
	} else if len(name) > 3 && name[0] == '-' && name[2] == '.' {
		flag = cmd.Flags().ShorthandLookup(name[1:2])
	}
	if flag != nil && flag.Annotations["documentation-no-value"] != nil {
		return flag
	}
	return nil
}

// yargs expands a short group even when an earlier letter is a string option.
// Only numeric or punctuation suffixes attach to that letter; the final flag
// uses the same following-token rules as its ungrouped spelling.
func documentationGroupedArgs(cmd *cobra.Command, arg string, next []string) ([]string, int, bool) {
	if cmd.Name() != "generate-docs" || cmd.Parent() == nil || (cmd.Parent().Name() != "features" && cmd.Parent().Name() != "templates") || len(arg) < 3 || arg[0] != '-' {
		return nil, 0, false
	}
	if strings.HasPrefix(arg, "--") {
		name, _, equals := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if key, _, dotted := strings.Cut(name, "."); dotted {
			if flag := cmd.Flags().Lookup(key); flag != nil && flag.Annotations["documentation-no-value"] != nil {
				consumed := 0
				if !equals && len(next) > 0 && (!strings.HasPrefix(next[0], "-") || documentationNegativeValue.MatchString(next[0])) {
					consumed = 1
				}
				return []string{"--" + flag.Name + "=[object Object]"}, consumed, true
			}
		}
		return nil, 0, false
	}
	// Dotted shorthand names set a property on a boxed option value in yargs.
	// That object is converted to text alongside repeated string values.
	if len(arg) > 3 && arg[2] == '.' {
		if flag := cmd.Flags().ShorthandLookup(arg[1:2]); flag != nil && flag.Annotations["documentation-no-value"] != nil {
			consumed := 0
			if !strings.Contains(arg, "=") && len(next) > 0 && !strings.HasPrefix(next[0], "-") {
				consumed = 1
			}
			return []string{"--" + flag.Name + "=[object Object]"}, consumed, true
		}
	}
	var out []string
	for j := 1; j < len(arg); j++ {
		letter := arg[j : j+1]
		flag := cmd.Flags().ShorthandLookup(letter)
		if flag == nil || flag.Annotations["documentation-no-value"] == nil {
			out = append(out, "-"+arg[j:])
			return out, 0, true
		}
		rest := arg[j+1:]
		if rest == "" {
			if value, handled := documentationOptionArg(flag, "-"+letter, next); handled {
				out = append(out, value)
			} else {
				out = append(out, "-"+letter)
			}
			return out, 0, true
		}
		if strings.HasPrefix(rest, "=") {
			value, _ := documentationOptionArg(flag, "-"+letter+rest, next)
			return append(out, value), 0, true
		}
		if rest == "-" || documentationAttachedNumber.MatchString(rest) || !documentationWord.MatchString(rest[:1]) {
			return append(out, "--"+flag.Name+"="+rest), 0, true
		}
		out = append(out, "--"+flag.Name+"="+flag.Annotations["documentation-no-value"][0])
	}
	return out, 0, true
}

// Adapt just this command's string flags before pflag parses them. Its short
// and long forms use yargs-parser's distinct rules for the following token.
func documentationOptionArg(flag *pflag.Flag, arg string, next []string) (string, bool) {
	defaults, ok := flag.Annotations["documentation-no-value"]
	if !ok {
		return "", false
	}
	if _, value, equals := strings.Cut(arg, "="); equals {
		// yargs treats a trailing '=' in a short group as an unknown option.
		if !strings.HasPrefix(arg, "--") && value == "" {
			return "--=", true
		}
		return "--" + flag.Name + "=" + value, true
	}
	missing := len(next) == 0
	if !missing {
		option := strings.HasPrefix(next[0], "-")
		if !strings.HasPrefix(arg, "--") {
			option = documentationShortOption.MatchString(next[0])
		}
		missing = option && !documentationNegativeValue.MatchString(next[0])
	}
	if missing {
		return "--" + flag.Name + "=" + defaults[0], true
	}
	return "", false
}
