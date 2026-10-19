// relationships:
//
//	references: [build, stage, ../builder/builder]
package cli

import "github.com/spf13/cobra"

func hiddenBool(cmd *cobra.Command, value *bool, name, description string) {
	cmd.Flags().BoolVar(value, name, false, description)
	_ = cmd.Flags().MarkHidden(name)
}

func (o *buildOptions) featureControlFlags(cmd *cobra.Command) {
	if cmd.Name() != "build" && cmd.Name() != "up" {
		return
	}
	hiddenBool(cmd, &o.options.SkipFeatureAutoMapping, "skip-feature-auto-mapping", "Temporary option for testing: disable legacy feature identifier mapping")
	hiddenBool(cmd, &o.options.OmitSyntaxDirective, "omit-syntax-directive", "Omit Dockerfile syntax directives")
	if cmd.Name() == "build" {
		hiddenBool(cmd, &o.options.Metadata.SkipFeatureCustomizations, "skip-persisting-customizations-from-features", "Do not persist customizations from referenced features in image metadata")
	}
	if cmd.Name() == "up" {
		hiddenBool(cmd, &o.options.Metadata.OmitConfigRemoteEnv, "omit-config-remote-env-from-metadata", "Omit devcontainer.json remoteEnv from metadata")
	}
}

func (a *application) featureControlFlags(root *cobra.Command) {
	for _, cmd := range root.Commands() {
		switch cmd.Name() {
		case "exec", "read-configuration", "run-user-commands":
			hiddenBool(cmd, &a.skipFeatureAutoMapping, "skip-feature-auto-mapping", "Temporary option for testing: disable legacy feature identifier mapping")
		}
	}
}
