// relationships:
//
//	references: [cli, ../host/runner/runner, ../../reference/cli/src/spec-node/devContainersSpecCLI]
package cli

import (
	"github.com/spf13/cobra"
)

// terminalFlags shares output dimensions across the commands that accept them.
// Like upstream, zero in either dimension leaves the terminal size unspecified.
func (a *application) terminalFlags(root *cobra.Command) {
	for _, cmd := range root.Commands() {
		switch cmd.Name() {
		case "outdated", "up", "set-up", "run-user-commands", "read-configuration", "exec":
		default:
			continue
		}
		var columns, rows float64
		cmd.Flags().Float64Var(&columns, "terminal-columns", 0, "Number of columns to render the output for (requires --terminal-rows)")
		cmd.Flags().Float64Var(&rows, "terminal-rows", 0, "Number of rows to render the output for (requires --terminal-columns)")
		cmd.MarkFlagsRequiredTogether("terminal-columns", "terminal-rows")
		action := cmd.RunE
		cmd.RunE = func(cmd *cobra.Command, args []string) error {
			if columns != 0 && rows != 0 {
				a.terminalSize = &[2]uint{uint(rows), uint(columns)}
			}
			return action(cmd, args)
		}
	}
}
