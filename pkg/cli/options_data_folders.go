// relationships:
//
// references: [config, stage, ../../internal/datafolder/staging]
package cli

import "github.com/spf13/cobra"

// dataFolderFlags registers exactly the upstream command-option pairs. Empty
// values select upstream defaults; system and session folders have no writer
// until system patching and user environment probing are implemented.
func dataFolderFlags(cmd *cobra.Command) {
	switch cmd.Name() {
	case "build", "exec", "read-configuration", "run-user-commands", "set-up", "up":
		cmd.Flags().String("user-data-folder", "", "Host folder for persistent CLI state shared between sessions")
	default:
		return
	}
	switch cmd.Name() {
	case "exec", "run-user-commands", "set-up", "up":
		cmd.Flags().String("container-data-folder", "", "Container user data folder (default: .devcontainer under the user's home)")
		cmd.Flags().String("container-system-data-folder", "", "Container system data folder (default: /var/devcontainer)")
	}
	switch cmd.Name() {
	case "run-user-commands", "set-up", "up":
		cmd.Flags().String("container-session-data-folder", "", "Container folder for session caches, including user environment probes")
	}
}

func folderOption(cmd *cobra.Command, name string) string {
	value, _ := cmd.Flags().GetString(name)
	return value
}
