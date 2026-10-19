// relationships:
//
//	references: [build, ../buildkit/output]
package cli

import (
	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/pkg/buildkit"
)

func (o *buildOptions) exporterFlags(cmd *cobra.Command) {
	cmd.Flags().StringVar(&o.options.Output, "output", "", "Buildx exporter specification (type=local|tar|oci|docker|image|registry,...) or directory")
}

func (a *application) exporterResult(raw string) {
	out, _ := buildkit.ParseOutput(raw)
	a.result["output"] = raw
	if out.Type == "local" {
		a.result["outputDir"] = out.Destination
	}
}

func (a *application) prepareExporter(raw string) error {
	if raw == "" {
		return nil
	}
	out, err := buildkit.ParseOutput(raw)
	if err != nil {
		return usageError("%v", err)
	}
	a.exportStdout = out.ToStdout()
	return nil
}
