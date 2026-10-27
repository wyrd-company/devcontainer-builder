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
