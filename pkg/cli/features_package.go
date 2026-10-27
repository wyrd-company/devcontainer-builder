// relationships:
//
//	references: [stage, ../features/package]
package cli

import (
	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
)

func (a *application) featuresPackageCommand() *cobra.Command {
	var opts features.PackageOptions
	cmd := &cobra.Command{
		Use: "package <target>", Short: "Package a local Feature or Feature collection",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				opts.Target = args[0]
			}
			if cmd.Flags().Changed("output-folder") && opts.OutputDir == "" {
				// path.resolve('') in upstream selects the command's current folder.
				opts.OutputDir = "."
			}
			collection, err := features.Package(cmd.Context(), opts)
			if err != nil {
				return err
			}
			a.result["features"] = collection.Features
			return nil
		},
	}
	cmd.Flags().StringVarP(&opts.OutputDir, "output-folder", "o", "./output", "Output directory (must not exist unless forced)")
	cmd.Flags().BoolVarP(&opts.ForceCleanOutputDir, "force-clean-output-folder", "f", false, "Remove an existing output directory before packaging")
	return cmd
}
