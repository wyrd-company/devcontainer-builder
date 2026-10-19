// relationships:
//   references: [host, ../host/docker/gpu]

package cli

import "github.com/spf13/cobra"

func gpuAvailabilityFlag(cmd *cobra.Command) {
	cmd.Flags().String("gpu-availability", "detect", "Availability of GPUs required by hostRequirements.gpu: all, detect, or none")
}

func resolveGPUAvailability(cmd *cobra.Command) (string, error) {
	mode, err := cmd.Flags().GetString("gpu-availability")
	if err != nil {
		return "", err
	}
	switch mode {
	case "all", "detect", "none":
		return mode, nil
	default:
		return "", usageError("--gpu-availability must be all, detect, or none")
	}
}
