package dockercompose

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/compose-spec/compose-go/v2/loader"
	"github.com/compose-spec/compose-go/v2/types"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// LoadInputs are the parameters needed to merge a compose project for
// a devcontainer.
type LoadInputs struct {
	// Spec is the parsed devcontainer.json. Must be in compose mode.
	Spec *spec.DevContainer
	// Workspace is the host workspace path; used for the project name.
	Workspace string
	// LocalEnv is the host environment forwarded to compose's
	// `${VAR}` interpolation. Pass an empty map (not nil) for none.
	LocalEnv map[string]string
	// Override is an additional YAML layer appended to the merge chain.
	// Optional: callers that don't need overrides can leave it nil.
	// When the package builds an internal override (workspace mount,
	// derived image, etc.) it appends it here.
	Override []byte
}

// LoadProject parses and merges the spec's compose files (plus any
// override layer) into a single normalized *types.Project, ready for
// engine-API consumption.
func LoadProject(ctx context.Context, in LoadInputs) (*types.Project, error) {
	if in.Spec == nil {
		return nil, fmt.Errorf("LoadInputs.Spec is required")
	}
	if in.Spec.Mode() != spec.ModeCompose {
		return nil, fmt.Errorf("spec is not in compose mode")
	}
	files := in.Spec.ComposeFiles()
	if len(files) == 0 {
		return nil, fmt.Errorf("spec has no dockerComposeFile entries")
	}
	if in.Spec.Service == "" {
		return nil, fmt.Errorf("spec is missing the service field")
	}

	configFiles := make([]types.ConfigFile, 0, len(files)+1)
	for _, f := range files {
		configFiles = append(configFiles, types.ConfigFile{Filename: f})
	}
	if len(in.Override) > 0 {
		configFiles = append(configFiles, types.ConfigFile{
			Filename: "<dcb-override>",
			Content:  in.Override,
		})
	}

	// As docker compose without --project-directory, relative paths in every
	// file resolve against the first file's directory.
	details := types.ConfigDetails{
		WorkingDir:  filepath.Dir(files[0]),
		ConfigFiles: configFiles,
		Environment: types.Mapping(in.LocalEnv),
	}

	projectName := ProjectName(in.Workspace)

	proj, err := loader.LoadWithContext(ctx, details, func(o *loader.Options) {
		o.SetProjectName(projectName, true)
		o.SkipConsistencyCheck = false
		o.ResolvePaths = true
	})
	if err != nil {
		return nil, fmt.Errorf("compose load: %w", err)
	}

	if _, err := proj.GetService(in.Spec.Service); err != nil {
		return nil, fmt.Errorf("%w: %q", ErrPrimaryNotFound, in.Spec.Service)
	}
	return proj, nil
}
