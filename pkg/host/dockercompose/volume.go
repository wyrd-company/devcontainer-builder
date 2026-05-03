package dockercompose

import (
	"context"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/volume"
)

// ensureVolumes creates any named volumes declared by the project
// that don't yet exist on the daemon. External volumes are skipped.
// Returns a map keyed by the project's volume key with the resolved
// engine-side name so spawn can mount by full name.
func (h *Host) ensureVolumes(ctx context.Context, proj *types.Project) (map[string]string, error) {
	resolved := make(map[string]string, len(proj.Volumes))
	for key, v := range proj.Volumes {
		name := resourceName(proj.Name, key, v.Name)
		resolved[key] = name
		if bool(v.External) {
			continue
		}
		exists, err := h.findVolume(ctx, name)
		if err != nil {
			return nil, err
		}
		if exists {
			continue
		}
		labels := projectLabels(proj.Name, proj.WorkingDir, "")
		labels["com.docker.compose.volume"] = key
		for k, val := range v.Labels {
			labels[k] = val
		}
		if _, err := h.client.VolumeCreate(ctx, volume.CreateOptions{
			Name:       name,
			Driver:     v.Driver,
			DriverOpts: map[string]string(v.DriverOpts),
			Labels:     labels,
		}); err != nil {
			return nil, errorf("create volume %q: %w", name, err)
		}
		h.logf("created volume %s", name)
	}
	return resolved, nil
}

func (h *Host) findVolume(ctx context.Context, name string) (bool, error) {
	args := filters.NewArgs(filters.Arg("name", name))
	list, err := h.client.VolumeList(ctx, volume.ListOptions{Filters: args})
	if err != nil {
		return false, errorf("list volumes: %w", err)
	}
	for _, v := range list.Volumes {
		if v.Name == name {
			return true, nil
		}
	}
	return false, nil
}
