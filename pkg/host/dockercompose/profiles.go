package dockercompose

import (
	"fmt"

	"github.com/compose-spec/compose-go/v2/types"
)

// applyProfiles narrows proj.Services to the set enabled by profiles
// plus any services explicitly listed in keepEnabled (which transitively
// activates each kept service's own profiles via WithServicesEnabled).
//
// compose-go's Project is immutable: WithProfiles and WithServicesEnabled
// each return a new *Project, so the caller has to thread the returned
// pointer through downstream steps. Returns the original project when
// neither selector is set so the caller doesn't pay for a deep-copy.
func applyProfiles(proj *types.Project, profiles []string, keepEnabled []string) (*types.Project, error) {
	if proj == nil {
		return nil, fmt.Errorf("applyProfiles: project is nil")
	}
	out := proj
	if len(profiles) > 0 {
		filtered, err := proj.WithProfiles(profiles)
		if err != nil {
			return nil, fmt.Errorf("apply profiles %v: %w", profiles, err)
		}
		out = filtered
	}
	if len(keepEnabled) > 0 {
		ensured, err := out.WithServicesEnabled(keepEnabled...)
		if err != nil {
			return nil, fmt.Errorf("ensure services enabled %v: %w", keepEnabled, err)
		}
		out = ensured
	}
	return out, nil
}
