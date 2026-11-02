// relationships:
//
//	references: [stage, ../features/dependencies]
package cli

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
)

func (a *application) featuresDependenciesCommand() *cobra.Command {
	var workspace, level string
	cmd := &cobra.Command{Use: "resolve-dependencies", Short: "Report the Feature dependency graph and installation order", Args: cobra.NoArgs}
	cmd.Flags().StringVar(&workspace, "workspace-folder", "", "Workspace folder (default: current directory)")
	cmd.Flags().StringVar(&level, "log-level", "error", "Log level: error, info, debug or trace")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		switch level {
		case "error", "info", "debug", "trace":
		default:
			return usageError("--log-level must be error, info, debug or trace")
		}
		a.exportStdout = true
		out, err := features.ResolveDependencies(nil, workspace)
		if _, e := fmt.Fprint(a.stdout, out); e != nil {
			a.outputErr = e
			return e
		}
		return err
	}
	return cmd
}
