// relationships:
//
//	references: [cli, ../../test/parity/README]
package cli

import (
	"io"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// RegisteredOptions reports the real command tree's accepted long flag names.
// The empty command path is the root. Actions are never executed. Inherited and
// hidden flags are included; positional arguments and shorthands are not flags.
func RegisteredOptions() map[string][]string {
	a := &application{stdin: strings.NewReader(""), stdout: io.Discard, stderr: io.Discard}
	return registeredOptions(a.command())
}

func registeredOptions(root *cobra.Command) map[string][]string {
	out := map[string][]string{}
	var visit func(*cobra.Command, string)
	visit = func(cmd *cobra.Command, path string) {
		cmd.InitDefaultHelpFlag()
		flags := pflag.NewFlagSet(path, pflag.ContinueOnError)
		flags.AddFlagSet(cmd.InheritedFlags())
		flags.AddFlagSet(cmd.Flags())
		out[path] = []string{}
		flags.VisitAll(func(f *pflag.Flag) { out[path] = append(out[path], f.Name) })
		for _, child := range cmd.Commands() {
			visit(child, strings.TrimSpace(path+" "+child.Name()))
		}
	}
	visit(root, "")
	return out
}
