// relationships:
//
//	references: [options_environment, ../host/runner/probe]
package cli

import (
	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/runner"
)

type userEnvProbeOptions struct{ mode string }

func (o *userEnvProbeOptions) flags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.mode, "default-user-env-probe", runner.DefaultUserEnvProbe, "User environment probe: none, loginInteractiveShell, interactiveShell, loginShell")
	_ = cmd.RegisterFlagCompletionFunc("default-user-env-probe", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return []string{"none", "loginInteractiveShell", "interactiveShell", "loginShell"}, cobra.ShellCompDirectiveNoFileComp
	})
}
func (o userEnvProbeOptions) validate() error {
	if !runner.ValidUserEnvProbe(o.mode) {
		return usageError("--default-user-env-probe must be none, loginInteractiveShell, interactiveShell or loginShell")
	}
	return nil
}
