// relationships:
//   references: [builder, ../buildkit/dockerfile]

package builder

import (
	"fmt"
	"io"
	"strings"

	dockeropts "github.com/docker/cli/opts"

	"github.com/spf13/pflag"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

type dockerBuildOptions struct {
	Network string
	Labels  map[string]string
	Args    map[string]string
}

// buildNetworkMode retains the network-only view used by callers and tests.
func buildNetworkMode(dc *spec.DevContainer) (string, error) {
	parsed, err := parseBuildOptions(dc)
	return parsed.Network, err
}

// parseBuildOptions uses Docker's pflag string grammar. Validation follows the
// BuildKit build network contract; custom container networks are not build networks.
func parseBuildOptions(dc *spec.DevContainer) (dockerBuildOptions, error) {
	var parsed dockerBuildOptions
	if dc == nil || dc.Build == nil || len(dc.Build.Options) == 0 {
		return parsed, nil
	}
	flags := pflag.NewFlagSet("build.options", pflag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var mode string
	var labels []string
	var args []string
	flags.StringArrayVar(&args, "build-arg", nil, "build-time variables")
	flags.StringArrayVar(&labels, "label", nil, "image labels")
	flags.StringVar(&mode, "network", "default", "network for Dockerfile RUN instructions")
	if err := flags.Parse(dc.Build.Options); err != nil {
		return parsed, fmt.Errorf("build.options is not supported: %w", err)
	}
	if len(flags.Args()) != 0 {
		return parsed, fmt.Errorf("build.options: unexpected positional arguments %q", flags.Args())
	}
	for _, value := range labels {
		key, _, _ := strings.Cut(value, "=")
		if key == "" {
			return parsed, fmt.Errorf("build.options: invalid key-value pair %q: empty key", value)
		}
	}
	parsed.Labels = dockeropts.ConvertKVStringsToMap(labels)
	// Resolve each occurrence before merging, as native buildx does: an unset
	// bare argument leaves an earlier value (including build.args) intact.
	parsed.Args = make(map[string]string)
	for _, arg := range args {
		resolved, err := dockeropts.ValidateEnv(arg)
		if err != nil {
			return parsed, fmt.Errorf("build.options: build-arg: %w", err)
		}
		if key, value, ok := strings.Cut(resolved, "="); ok {
			parsed.Args[key] = value
		}
	}

	switch mode {
	case "default", "":
		parsed.Network = "sandbox"
	case "host", "none":
		parsed.Network = mode
	default:
		return parsed, fmt.Errorf("build.options: network mode %q not supported by BuildKit", mode)
	}
	return parsed, nil
}

// buildImageLabels applies explicit library labels after configuration options.
func buildImageLabels(dc *spec.DevContainer, explicit map[string]string) (map[string]string, error) {
	parsed, err := parseBuildOptions(dc)
	if err != nil {
		return nil, err
	}
	return mergeArgs(parsed.Labels, explicit), nil
}
