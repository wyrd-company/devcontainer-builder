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
)

// yargs-parser consumes negative decimal strings as option values.
var documentationNegativeValue = regexp.MustCompile(`^-([0-9]+(\.[0-9]+)?|\.[0-9]+)$`)
var documentationShortOption = regexp.MustCompile(`^(-|--)[^-]`)

func (a *application) featuresDocumentationCommand() *cobra.Command {
	var opts features.DocumentationOptions
	var projects, registries, namespaces, owners, repos []string
	cmd := &cobra.Command{Use: "generate-docs", Short: "Generate READMEs for local Features", Args: cobra.NoArgs}
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
		return features.GenerateDocumentation(cmd.Context(), opts)
	}
	cmd.Flags().StringArrayVarP(&projects, "project-folder", "p", []string{"."}, "Folder containing Feature directories")
	cmd.Flags().StringArrayVarP(&registries, "registry", "r", []string{"ghcr.io"}, "OCI registry name")
	cmd.Flags().StringArrayVarP(&namespaces, "namespace", "n", nil, "Feature collection namespace")
	_ = cmd.MarkFlagRequired("namespace")
	cmd.Flags().StringArrayVar(&owners, "github-owner", nil, "GitHub owner for metadata links")
	cmd.Flags().StringArrayVar(&repos, "github-repo", nil, "GitHub repository for metadata links")
	// yargs supplies the declared default when a string option has no value.
	// Keep explicit empty strings distinct from absent values.
	for name, value := range map[string]string{"project-folder": ".", "registry": "ghcr.io", "namespace": "", "github-owner": "", "github-repo": ""} {
		flag := cmd.Flags().Lookup(name)
		if flag.Annotations == nil {
			flag.Annotations = map[string][]string{}
		}
		flag.Annotations["documentation-no-value"] = []string{value}
	}
	return cmd
}

var documentationAttachedNumber = regexp.MustCompile(`^-?\d+(\.\d*)?(e-?\d+)?$`)
var documentationWord = regexp.MustCompile(`^[A-Za-z0-9_]$`)

func documentationDottedFlag(cmd *cobra.Command, arg string) *pflag.Flag {
	if cmd.Name() != "generate-docs" || cmd.Parent() == nil || cmd.Parent().Name() != "features" {
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
	if cmd.Name() != "generate-docs" || cmd.Parent() == nil || cmd.Parent().Name() != "features" || len(arg) < 3 || arg[0] != '-' {
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
