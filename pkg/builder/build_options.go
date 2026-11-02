// relationships:
//   references: [builder, ../buildkit/dockerfile]

package builder

import (
	"fmt"
	"io"

	"github.com/spf13/pflag"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// buildNetworkMode uses Docker's pflag string grammar. Validation follows the
// BuildKit build network contract; custom container networks are not build networks.
func buildNetworkMode(dc *spec.DevContainer) (string, error) {
	if dc == nil || dc.Build == nil || len(dc.Build.Options) == 0 {
		return "", nil
	}
	flags := pflag.NewFlagSet("build.options", pflag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var mode string
	flags.StringVar(&mode, "network", "default", "network for Dockerfile RUN instructions")
	if err := flags.Parse(dc.Build.Options); err != nil {
		return "", fmt.Errorf("build.options is not supported: %w", err)
	}
	if len(flags.Args()) != 0 {
		return "", fmt.Errorf("build.options: unexpected positional arguments %q", flags.Args())
	}
	switch mode {
	case "default", "":
		return "sandbox", nil
	case "host", "none":
		return mode, nil
	default:
		return "", fmt.Errorf("build.options: network mode %q not supported by BuildKit", mode)
	}
}
