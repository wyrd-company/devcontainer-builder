package dockercompose

import (
	"context"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
)

// ensureNetworks creates any networks declared by the project that
// don't yet exist on the daemon. External networks are skipped
// (they're the user's to provision). Returns a map keyed by the
// project's network key (e.g. "default") with the resolved engine-
// side name attached, so the spawn step can wire EndpointSettings.
func (h *Host) ensureNetworks(ctx context.Context, proj *types.Project) (map[string]string, error) {
	resolved := make(map[string]string, len(proj.Networks))
	for key, n := range proj.Networks {
		name := resourceName(proj.Name, key, n.Name)
		resolved[key] = name
		if bool(n.External) {
			continue
		}
		exists, err := h.findNetwork(ctx, name)
		if err != nil {
			return nil, err
		}
		if exists {
			continue
		}
		labels := projectLabels(proj.Name, proj.WorkingDir)
		labels["com.docker.compose.network"] = key
		for k, v := range n.Labels {
			labels[k] = v
		}
		opts := network.CreateOptions{
			Driver:     n.Driver,
			Internal:   n.Internal,
			Attachable: n.Attachable,
			Labels:     labels,
			Options:    map[string]string(n.DriverOpts),
		}
		if n.EnableIPv4 != nil {
			opts.EnableIPv4 = n.EnableIPv4
		}
		if n.EnableIPv6 != nil {
			opts.EnableIPv6 = n.EnableIPv6
		}
		if _, err := h.client.NetworkCreate(ctx, name, opts); err != nil {
			return nil, errorf("create network %q: %w", name, err)
		}
		h.logf("created network %s", name)
	}
	return resolved, nil
}

func (h *Host) findNetwork(ctx context.Context, name string) (bool, error) {
	args := filters.NewArgs(filters.Arg("name", name))
	list, err := h.client.NetworkList(ctx, network.ListOptions{Filters: args})
	if err != nil {
		return false, errorf("list networks: %w", err)
	}
	for _, n := range list {
		if n.Name == name {
			return true, nil
		}
	}
	return false, nil
}
