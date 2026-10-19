// relationships:
//
//	references: [lifecycle, host, ../host/runner/controls]
package cli

import (
	"context"
	"errors"

	dockerclient "github.com/docker/docker/client"
	"github.com/spf13/cobra"
	hostdocker "github.com/wyrd-company/devcontainer-builder/pkg/host/docker"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/dockercompose"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/runner"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

type lifecycleOptions struct {
	runner.LifecycleControls
	expectExisting bool
}

func (o *lifecycleOptions) flags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&o.SkipNonBlocking, "skip-non-blocking-commands", false, "Stop after waitFor (default: updateContentCommand)")
	if cmd.Name() != "set-up" {
		cmd.Flags().BoolVar(&o.Prebuild, "prebuild", false, "Stop after updateContentCommand and rerun it even when its marker is current")
	}
	if cmd.Name() == "run-user-commands" {
		cmd.Flags().BoolVar(&o.StopForPersonalization, "stop-for-personalization", false, "Stop after dotfiles, before postStartCommand")
	}
	if cmd.Name() == "up" {
		cmd.Flags().BoolVar(&o.expectExisting, "expect-existing-container", false, "Fail if the expected container does not exist")
	}
}

// requireExisting precedes builds and removal, matching upstream's existence
// check even when --remove-existing-container asks to replace the container.
func (o lifecycleOptions) requireExisting(ctx context.Context, cli *dockerclient.Client, dc *spec.DevContainer, workspace string, idLabels map[string]string) error {
	if !o.expectExisting {
		return nil
	}
	if len(idLabels) > 0 && dc.Mode() != spec.ModeCompose {
		// Upstream looks the container up by its id labels.
		_, err := hostdocker.New(cli, hostdocker.Options{}).FindByLabels(ctx, idLabels)
		if !errors.Is(err, hostdocker.ErrContainerNotFound) {
			return err
		}
	} else if dc.Mode() == spec.ModeCompose {
		proj, err := dockercompose.LoadProject(ctx, dockercompose.LoadInputs{Spec: dc, Workspace: workspace, LocalEnv: envMap()})
		if err != nil {
			return err
		}
		id, err := dockercompose.FindExisting(ctx, cli, proj.Name, dc.Service)
		if err != nil {
			return err
		}
		if id != "" {
			return nil
		}
	} else {
		_, err := hostdocker.New(cli, hostdocker.Options{}).Inspect(ctx, workspace)
		if err == nil {
			return nil
		}
		if !errors.Is(err, hostdocker.ErrContainerNotFound) {
			return err
		}
	}
	return errors.New("The expected container does not exist.")
}
