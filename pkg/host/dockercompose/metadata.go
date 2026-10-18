// relationships:
//
//	references: [up, metadata, spawn]
package dockercompose

import (
	"maps"
	"slices"
	"strings"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/wyrd-company/devcontainer-builder/pkg/lifecycle"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

func imageMetadataEntries(req UpRequest, labels map[string]string, logf func(string, ...any)) []metadata.Entry {
	entries := metadata.Read(labels, logf)
	// A derived image already records the current config. Upstream labels the
	// new container with that metadata unchanged.
	if req.ImageBuilt && len(entries) > 0 {
		return entries
	}
	return metadata.Collect(entries, req.Features, req.Spec)
}

// setPrimaryMetadata makes captured metadata part of the service shape before
// config hashing and container creation. Lifecycle-only changes cause drift.
func setPrimaryMetadata(proj *types.Project, service string, entries []metadata.Entry) error {
	value, err := metadata.Encode(entries)
	if err != nil {
		return errorf("encode container metadata: %w", err)
	}
	svc := proj.Services[service]
	svc.Labels = maps.Clone(svc.Labels)
	if svc.Labels == nil {
		svc.Labels = types.Labels{}
	}
	svc.Labels[metadata.Label] = value
	proj.Services[service] = svc
	return nil
}

func runtimeMetadata(dc *spec.DevContainer, entries []metadata.Entry, ctx substitute.Context) (metadata.Merged, error) {
	entries, err := metadata.Substitute(entries, func(s string) string { return substitute.Substitute(ctx, s) })
	if err != nil {
		return metadata.Merged{}, errorf("substitute image metadata: %w", err)
	}
	return metadata.Merge(substituteSpec(dc, ctx), entries), nil
}

// applyPrimaryMetadata adds the merged devcontainer settings to the native
// Compose service. Native OR/union settings survive false or absent metadata;
// scalar settings change only when metadata supplies a value.
func applyPrimaryMetadata(proj *types.Project, dc *spec.DevContainer, req UpRequest, ctx substitute.Context) {
	svc := proj.Services[dc.Service]
	if dc.Init != nil && *dc.Init {
		svc.Init = dc.Init
	}
	svc.Privileged = svc.Privileged || dc.Privileged != nil && *dc.Privileged
	svc.CapAdd = appendUnique(svc.CapAdd, dc.CapAdd)
	svc.SecurityOpt = appendUnique(svc.SecurityOpt, dc.SecurityOpt)
	if dc.ContainerUser != "" {
		svc.User = dc.ContainerUser
	}
	// Like upstream's override, only containerEnv reaches the service;
	// remoteEnv applies to exec and lifecycle commands.
	env := map[string]string{}
	if !req.ImageBuilt {
		for _, f := range req.Features {
			for key, value := range f.Metadata.ContainerEnv {
				env[key] = substitute.Substitute(ctx, value)
			}
		}
	}
	for key, value := range stringMap(dc.ContainerEnv) {
		env[key] = value
	}
	if svc.Environment == nil {
		svc.Environment = types.MappingWithEquals{}
	}
	for key, value := range env {
		svc.Environment[key] = &value
	}
	for _, raw := range dc.Mounts {
		vol, external := metadataVolume(raw)
		if vol.Target == "" {
			continue
		}
		// Metadata mounts override native mounts at the same destination.
		svc.Volumes = slices.DeleteFunc(svc.Volumes, func(v types.ServiceVolumeConfig) bool { return v.Target == vol.Target })
		svc.Volumes = append(svc.Volumes, vol)
		if vol.Type == types.VolumeTypeVolume && vol.Source != "" {
			// As in upstream's override file, the source is a project volume
			// (<project>_<source>, removed by down) unless marked external. A
			// native declaration of the same key is used as is.
			if proj.Volumes == nil {
				proj.Volumes = types.Volumes{}
			}
			if _, exists := proj.Volumes[vol.Source]; !exists {
				declaration := types.VolumeConfig{Name: proj.Name + "_" + vol.Source}
				if external {
					declaration = types.VolumeConfig{Name: vol.Source, External: true}
				}
				proj.Volumes[vol.Source] = declaration
			}
		}
	}
	proj.Services[dc.Service] = svc
}

func appendUnique(dst, src []string) []string {
	for _, value := range src {
		if !slices.Contains(dst, value) {
			dst = append(dst, value)
		}
	}
	return dst
}

// metadataVolume accepts the devcontainer string and object forms and reports
// whether the mount is marked external. Unknown fields follow the Docker
// host's permissive mount parsing.
func metadataVolume(raw any) (types.ServiceVolumeConfig, bool) {
	fields := map[string]any{}
	switch value := raw.(type) {
	case string:
		for _, part := range strings.Split(value, ",") {
			key, value, _ := strings.Cut(strings.TrimSpace(part), "=")
			fields[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	case map[string]any:
		fields = value
	}
	vol := types.ServiceVolumeConfig{Type: types.VolumeTypeBind}
	external := false
	for key, raw := range fields {
		value, _ := raw.(string)
		switch key {
		case "type":
			if value != "" {
				vol.Type = value
			}
		case "source", "src":
			vol.Source = value
		case "target", "dst", "destination":
			vol.Target = value
		case "consistency":
			vol.Consistency = value
		case "readonly", "readOnly", "ro":
			if flag, ok := raw.(bool); ok {
				vol.ReadOnly = flag
			} else {
				vol.ReadOnly = value == "" || value == "true" || value == "1"
			}
		case "external":
			flag, _ := raw.(bool)
			external = flag || value == "true"
		}
	}
	return vol, external
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
