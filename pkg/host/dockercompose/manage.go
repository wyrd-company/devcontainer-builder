package dockercompose

import (
	"context"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/client"
)

// ProjectInfo is a small projection of a compose project's runtime
// state.
type ProjectInfo struct {
	Name       string
	Workspace  string
	Containers []ContainerInfo
}

// ContainerInfo is a per-container projection.
type ContainerInfo struct {
	ID        string
	Service   string
	Image     string
	Running   bool
	StartedAt string
	CreatedAt string
	Primary   bool
	Labels    map[string]string
}

// Inspect returns the state of every container belonging to the
// compose project for the supplied workspace path. Returns
// ErrProjectNotFound if no containers match.
func (h *Host) Inspect(ctx context.Context, localWorkspace string) (*ProjectInfo, error) {
	projectName := ProjectName(localWorkspace)
	containers, err := h.listProjectContainers(ctx, projectName)
	if err != nil {
		return nil, err
	}
	if len(containers) == 0 {
		return nil, ErrProjectNotFound
	}
	info := &ProjectInfo{Name: projectName, Workspace: localWorkspace}
	for _, c := range containers {
		ci, err := h.inspectOne(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		info.Containers = append(info.Containers, *ci)
	}
	return info, nil
}

func (h *Host) inspectOne(ctx context.Context, id string) (*ContainerInfo, error) {
	resp, err := h.client.ContainerInspect(ctx, id)
	if err != nil {
		if client.IsErrNotFound(err) {
			return nil, ErrProjectNotFound
		}
		return nil, errorf("inspect: %w", err)
	}
	out := &ContainerInfo{
		ID:        resp.ID,
		CreatedAt: resp.Created,
		Labels:    map[string]string{},
	}
	if resp.State != nil {
		out.Running = resp.State.Running
		out.StartedAt = resp.State.StartedAt
	}
	if resp.Config != nil {
		out.Image = resp.Config.Image
		for k, v := range resp.Config.Labels {
			out.Labels[k] = v
			switch k {
			case LabelService:
				out.Service = v
			case LabelPrimary:
				out.Primary = strings.EqualFold(v, "true")
			}
		}
	}
	return out, nil
}

// Stop stops every container in the project. Idempotent on
// already-stopped containers.
func (h *Host) Stop(ctx context.Context, localWorkspace string) error {
	projectName := ProjectName(localWorkspace)
	containers, err := h.listProjectContainers(ctx, projectName)
	if err != nil {
		return err
	}
	if len(containers) == 0 {
		return ErrProjectNotFound
	}
	timeoutSec := int(h.opts.StopTimeout.Seconds())
	var firstErr error
	for _, c := range containers {
		err := h.client.ContainerStop(ctx, c.ID, container.StopOptions{Timeout: &timeoutSec})
		if err != nil && !isAlreadyStoppedErr(err) && firstErr == nil {
			firstErr = errorf("stop %s: %w", short(c.ID), err)
		}
	}
	return firstErr
}

// Remove tears the project down: stops + removes every container,
// then removes project-owned networks and volumes. External
// resources are left alone.
func (h *Host) Remove(ctx context.Context, localWorkspace string) error {
	projectName := ProjectName(localWorkspace)
	if err := h.Stop(ctx, localWorkspace); err != nil && err != ErrProjectNotFound {
		// best-effort; container removal forces below
		h.logf("stop before remove: %v", err)
	}
	containers, err := h.listProjectContainers(ctx, projectName)
	if err != nil {
		return err
	}
	for _, c := range containers {
		if err := h.client.ContainerRemove(ctx, c.ID, container.RemoveOptions{
			Force:         true,
			RemoveVolumes: false, // named volumes are removed below by name
		}); err != nil {
			h.logf("remove container %s: %v", short(c.ID), err)
		}
	}

	args := filters.NewArgs(filters.Arg("label", LabelProject+"="+projectName))
	nets, err := h.client.NetworkList(ctx, network.ListOptions{Filters: args})
	if err != nil {
		return errorf("list networks: %w", err)
	}
	for _, n := range nets {
		if err := h.client.NetworkRemove(ctx, n.ID); err != nil {
			h.logf("remove network %s: %v", n.Name, err)
		}
	}
	vols, err := h.client.VolumeList(ctx, volume.ListOptions{Filters: args})
	if err != nil {
		return errorf("list volumes: %w", err)
	}
	for _, v := range vols.Volumes {
		if err := h.client.VolumeRemove(ctx, v.Name, true); err != nil {
			h.logf("remove volume %s: %v", v.Name, err)
		}
	}
	return nil
}

func (h *Host) listProjectContainers(ctx context.Context, projectName string) ([]container.Summary, error) {
	args := filters.NewArgs(filters.Arg("label", LabelProject+"="+projectName))
	list, err := h.client.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return nil, errorf("list containers: %w", err)
	}
	return list, nil
}

func isAlreadyStoppedErr(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, s := range []string{"not running", "is not running", "is already stopped"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
