// relationships:
//   references: [../docker/up, ../dockercompose/up, ../../../reference/cli/src/spec-node/utils]

package gpu

import (
	"context"
	"fmt"
	"strings"

	"github.com/docker/docker/api/types/system"
)

// InfoClient reads the Engine's registered runtime records.
type InfoClient interface {
	Info(context.Context) (system.Info, error)
}

// Requested applies the shared upstream availability decision. Explicit GPU
// selection takes precedence over automatic host requirement requests.
func Requested(ctx context.Context, client InfoClient, requirement any, mode string, explicit bool, logf func(string, ...any)) (bool, error) {
	if mode == "" {
		mode = "detect"
	}
	switch mode {
	case "all", "detect", "none":
	default:
		return false, fmt.Errorf("GPU availability must be all, detect, or none")
	}
	if requirement == nil || requirement == false || requirement == "" || explicit {
		return false, nil
	}
	available := mode == "all"
	if mode == "detect" {
		info, err := client.Info(ctx)
		if err != nil {
			return false, fmt.Errorf("detect GPU support: %w", err)
		}
		// Match upstream's search of the complete formatted runtime record.
		available = strings.Contains(fmt.Sprint(info.Runtimes["nvidia"]), "nvidia-container-runtime")
	}
	if !available && requirement != "optional" && logf != nil {
		logf("warning: No GPU support found yet a GPU was required - consider marking it as optional")
	}
	return available, nil
}
