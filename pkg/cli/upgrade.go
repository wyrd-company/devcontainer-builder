// relationships:
//
//	references: [cli, ../features/upgrade]
package cli

import (
	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
)

func (a *application) upgradeCommand() *cobra.Command {
	var o features.UpgradeOptions
	var level string
	cmd := &cobra.Command{Use: "upgrade", Short: "Upgrade Feature requirements and generate a lockfile", Args: cobra.NoArgs}
	f := cmd.Flags()
	f.StringVar(&o.Workspace, "workspace-folder", ".", "Workspace folder")
	f.StringVar(&o.ConfigPath, "config", "", "Path to devcontainer.json")
	f.StringVar(&level, "log-level", "info", "Log level: error, info, debug or trace")
	f.BoolVar(&o.DryRun, "dry-run", false, "Print generated lockfile instead of forcing a write")
	f.StringVarP(&o.Feature, "feature", "f", "", "Feature requirement to update")
	f.StringVarP(&o.TargetVersion, "target-version", "v", "", "Target major, minor, or patch version")
	_ = f.MarkHidden("feature")
	_ = f.MarkHidden("target-version")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		switch level {
		case "error", "info", "debug", "trace":
		default:
			return usageError("--log-level must be error, info, debug or trace")
		}
		if err := features.ValidateUpgradeOptions(o); err != nil {
			return usageError("%v", err)
		}
		a.logLevel = level
		a.exportStdout = true
		resolver := features.New()
		resolver.Logger = a.logf
		out, err := resolver.Upgrade(cmd.Context(), o)
		if err != nil {
			return err
		}
		if o.DryRun {
			if _, err = a.stdout.Write(out); err != nil {
				a.outputErr = err
				return err
			}
		}
		return nil
	}
	return cmd
}
