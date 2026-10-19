// relationships:
//
//	references: [cli, ../../reference/cli/src/spec-node/devContainersSpecCLI]
package cli

import (
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// booleanOptionArgs adapts yargs-parser's separated boolean values to pflag's
// equals form before Cobra selects a command. Only lowercase true and false
// are consumed (yargs-parser 21.1.1, build/lib/yargs-parser.js:232-246).
func booleanOptionArgs(root *cobra.Command, args []string) []string {
	out := make([]string, 0, len(args))
	cmd := root
	selecting := true
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			out = append(out, args[i:]...)
			break
		}
		// Cobra also merges inherited persistent flags here.
		cmd.InitDefaultHelpFlag()
		var flag *pflag.Flag
		name, _, equals := strings.Cut(arg, "=")
		if strings.HasPrefix(name, "--") {
			name = strings.TrimPrefix(name, "--")
			flag = cmd.Flags().Lookup(name)
		} else if arg == "-h" {
			flag = cmd.Flags().ShorthandLookup("h")
		}
		if flag != nil && !equals && i+1 < len(args) {
			next := args[i+1]
			if flag.Value.Type() == "bool" && (next == "true" || next == "false") {
				arg += "=" + next
				i++
			} else if flag.NoOptDefVal == "" {
				// Preserve non-boolean values, even when they look like flags or
				// subcommands. pflag consumes the next token for these options.
				out = append(out, arg, next)
				i++
				continue
			}
		}
		if selecting && !strings.HasPrefix(arg, "-") {
			selecting = false
			for _, child := range cmd.Commands() {
				if arg == child.Name() || child.HasAlias(arg) {
					cmd = child
					selecting = true
					break
				}
			}
		}
		out = append(out, arg)
	}
	return out
}
