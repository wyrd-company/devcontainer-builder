// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package builder

import "github.com/wyrd-company/devcontainer-builder/pkg/spec"

func loadBuildSpec(opts Options) (*spec.DevContainer, error) {
	dc, err := readBuildSpec(opts)
	if err != nil {
		return nil, err
	}
	if err := dc.ValidateSource(); err != nil {
		return nil, err
	}
	return dc, nil
}

func readBuildSpec(opts Options) (*spec.DevContainer, error) {
	if opts.Spec != nil {
		if err := opts.Spec.Validate(); err != nil {
			return nil, err
		}
		return opts.Spec, nil
	}
	if opts.SpecPath != "" {
		return spec.Load(opts.SpecPath)
	}
	return spec.LoadFromWorkspace(opts.Workspace)
}
