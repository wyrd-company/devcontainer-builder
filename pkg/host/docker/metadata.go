// relationships:
//
//	references: [up, spawn, metadata]
package docker

import (
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/lifecycle"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

func runtimeMetadata(dc *spec.DevContainer, entries []metadata.Entry, ctx substitute.Context) (*spec.DevContainer, []string, error) {
	entries, err := metadata.Substitute(entries, func(s string) string { return substitute.Substitute(ctx, s) })
	if err != nil {
		return nil, nil, errorf("substitute image metadata: %w", err)
	}
	merged := metadata.Merge(substituteSpec(dc, ctx), entries)
	return merged.DevContainer, merged.Entrypoints, nil
}

func featureContainerEnv(env map[string]any, resolved []features.Resolved, ctx substitute.Context) map[string]any {
	out := map[string]any{}
	for _, f := range resolved {
		for k, v := range f.Metadata.ContainerEnv {
			out[k] = substitute.Substitute(ctx, v)
		}
	}
	for k, v := range env {
		out[k] = v
	}
	return out
}

func imageMetadataEntries(req UpRequest, labels map[string]string, logf func(string, ...any)) []metadata.Entry {
	entries := metadata.Read(labels, logf)
	// A derived image already records the current config. Upstream labels the
	// new container with that metadata unchanged.
	if req.ImageBuilt && len(entries) > 0 {
		return entries
	}
	entries = metadata.CollectWithOptions(entries, req.Features, req.Spec, metadata.CollectOptions{OmitConfigRemoteEnv: req.OmitConfigRemoteEnvFromMetadata})
	// Upstream adds [] on the direct-image path. Built images inherit their
	// labels instead; nil tells the container writer to add no label.
	if entries == nil && !req.ImageBuilt {
		return []metadata.Entry{}
	}
	return entries
}

func runtimePlan(req UpRequest, entries []metadata.Entry, ctx substitute.Context, containerEnv map[string]string) []lifecycle.Phase {
	if req.SkipLifecycle {
		return nil
	}
	var plan []lifecycle.Phase
	for _, phase := range metadata.Plan(entries) {
		if req.SkipPostAttach && phase.Hook == lifecycle.HookPostAttach {
			continue
		}
		phase.Command = containerSubstituteCommand(substitute.ContainerContext{ContainerEnv: containerEnv}, substituteCommand(ctx, phase.Command))
		plan = append(plan, phase)
	}
	return plan
}
