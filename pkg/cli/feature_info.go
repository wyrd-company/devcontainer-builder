// relationships:
//
//	references: [stage, ../features/info]
package cli

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
)

func (a *application) featuresInfoCommand() *cobra.Command {
	var format string
	cmd := &cobra.Command{Use: "info <mode> <feature>", Short: "Query published Feature information", Args: cobra.ExactArgs(2)}
	cmd.Flags().StringVar(&format, "output-format", "text", "Output format: text or json")
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if format != "text" && format != "json" {
			return usageError("--output-format must be text or json")
		}
		if args[0] != "manifest" && args[0] != "tags" && args[0] != "dependencies" && args[0] != "verbose" {
			return usageError("mode must be manifest, tags, dependencies or verbose")
		}
		if a.logLevel != "info" && a.logLevel != "debug" && a.logLevel != "trace" {
			return usageError("--log-level must be info, debug or trace")
		}
		a.exportStdout = true
		out, err := features.Info(nil, args[0], args[1], format)
		if _, writeErr := fmt.Fprint(a.stdout, out); writeErr != nil {
			a.outputErr = writeErr
			return writeErr
		}
		return err
	}
	return cmd
}
