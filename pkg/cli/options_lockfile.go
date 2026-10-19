// relationships:
//
//	references: [build, stage, ../features/lockfile]
package cli

import (
	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

type lockfileOptions struct{ create, frozen bool }

func (o *lockfileOptions) flags(cmd *cobra.Command) {
	cmd.Flags().BoolVar(&o.create, "experimental-lockfile", false, "Write lockfile")
	cmd.Flags().BoolVar(&o.frozen, "experimental-frozen-lockfile", false, "Ensure lockfile remains unchanged")
	_ = cmd.Flags().MarkHidden("experimental-lockfile")
	_ = cmd.Flags().MarkHidden("experimental-frozen-lockfile")
}

func (a *application) featureLockfile(dc *spec.DevContainer, options lockfileOptions) (*features.LockfileState, error) {
	if dc.Features.Len() == 0 {
		return nil, nil
	}
	if a.lockfile == nil {
		state, err := features.ReadLockfile(dc.Path, options.create, options.frozen)
		if err != nil {
			return nil, err
		}
		a.lockfile = state
	}
	return a.lockfile, nil
}
