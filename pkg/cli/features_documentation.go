// relationships:
//
// references: [stage, ../features/documentation]
package cli

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"strings"
)

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
	return cmd
}
