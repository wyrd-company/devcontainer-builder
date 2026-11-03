// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package builder

import (
	"encoding/json"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

func loadBuildSpec(opts Options) (*spec.DevContainer, error) {
	dc, err := readBuildSpec(opts)
	if err != nil {
		return nil, err
	}
	if err := dc.ValidateSource(); err != nil {
		return nil, err
	}
	return substituteBuildSpec(dc, opts), nil
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

// substituteBuildSpec copies only inputs consumed before container creation.
// Runtime contributions retain their authored values for the host phase.
func substituteBuildSpec(dc *spec.DevContainer, opts Options) *spec.DevContainer {
	ctx := opts.Substitution
	ctx.LocalWorkspaceFolder, ctx.LocalEnv = opts.Workspace, opts.LocalEnv
	out := *dc
	out.Image = substitute.Substitute(ctx, dc.Image)
	out.Dockerfile = substitute.Substitute(ctx, dc.Dockerfile)
	out.Context = substitute.Substitute(ctx, dc.Context)
	if dc.Build != nil {
		build := *dc.Build
		build.Dockerfile = substitute.Substitute(ctx, build.Dockerfile)
		build.Context = substitute.Substitute(ctx, build.Context)
		build.Target = substitute.Substitute(ctx, build.Target)
		build.Args = substitute.ApplyMap(ctx, build.Args)
		build.CacheFrom = spec.StringOrSlice(substitute.Apply(ctx, []string(build.CacheFrom)))
		build.Options = substitute.Apply(ctx, build.Options)
		out.Build = &build
	}
	out.Features = spec.OrderedMap{Keys: append([]string(nil), dc.Features.Keys...), Values: make(map[string]json.RawMessage, len(dc.Features.Values))}
	for key, value := range dc.Features.Values {
		out.Features.Values[key] = append(json.RawMessage(nil), value...)
		// Feature values are JSON objects or booleans validated by spec loading.
		// Traverse strings while retaining keys and their declaration order.
		var v any
		if json.Unmarshal(value, &v) == nil {
			out.Features.Values[key], _ = json.Marshal(substituteBuildValue(ctx, v))
		}
	}
	return &out
}

func substituteBuildValue(ctx substitute.Context, value any) any {
	switch v := value.(type) {
	case string:
		return substitute.Substitute(ctx, v)
	case []any:
		for i := range v {
			v[i] = substituteBuildValue(ctx, v[i])
		}
		return v
	case map[string]any:
		for key := range v {
			v[key] = substituteBuildValue(ctx, v[key])
		}
		return v
	default:
		return value
	}
}
