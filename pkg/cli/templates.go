// relationships:
//
//	references: [../templates/metadata, cli]
package cli

import (
	"encoding/json"
	"errors"

	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/pkg/templates"
)

func (a *application) templatesCommand() *cobra.Command {
	group := &cobra.Command{Use: "templates", Short: "Development container Template commands"}
	metadata := &cobra.Command{
		Use: "metadata <templateId>", Short: "Fetch a published Template's metadata", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			a.exportStdout = true
			value, err := templates.Metadata(nil, args[0])
			if errors.Is(err, templates.ErrUnavailable) {
				value = json.RawMessage(`{}`)
			}
			if value == nil {
				return err
			}
			if writeErr := json.NewEncoder(a.stdout).Encode(value); writeErr != nil {
				a.outputErr = writeErr
				return writeErr
			}
			return err
		},
	}
	group.AddCommand(metadata)
	return group
}
