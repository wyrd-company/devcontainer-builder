package docker

import (
	"context"
	"errors"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// ContainerInfo is a small projection of docker's InspectResponse.
// Returned by Inspect; sufficient for the most common embedder needs
// without exposing the full SDK type at the API boundary.
type ContainerInfo struct {
	ID         string
	Image      string
	Running    bool
	StartedAt  string
	CreatedAt  string
	Labels     map[string]string
	WorkingDir string
}

// Inspect returns the current state of the devcontainer for the given
// local workspace path. Returns ErrContainerNotFound if no container
// matches the workspace label.
func (h *Host) Inspect(ctx context.Context, localWorkspace string) (*ContainerInfo, error) {
	id, err := h.findExisting(ctx, localWorkspace)
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, ErrContainerNotFound
	}
	return h.InspectByID(ctx, id)
}

// InspectByID is Inspect when you already have a container ID (from
// e.g. Up's UpResult).
func (h *Host) InspectByID(ctx context.Context, id string) (*ContainerInfo, error) {
	resp, err := h.client.ContainerInspect(ctx, id)
	if err != nil {
		if client.IsErrNotFound(err) {
			return nil, ErrContainerNotFound
		}
		return nil, errorf("inspect: %w", err)
	}
	info := &ContainerInfo{
		ID:        resp.ID,
		Image:     resp.Image,
		Running:   resp.State != nil && resp.State.Running,
		CreatedAt: resp.Created,
		Labels:    map[string]string{},
	}
	if resp.State != nil {
		info.StartedAt = resp.State.StartedAt
	}
	if resp.Config != nil {
		info.WorkingDir = resp.Config.WorkingDir
		for k, v := range resp.Config.Labels {
			info.Labels[k] = v
		}
	}
	return info, nil
}

// Stop sends SIGTERM and waits up to Options.StopTimeout for the
// container to exit. Returns nil even if the container was already
// stopped, so it's safe to call as a teardown step regardless of
// state.
func (h *Host) Stop(ctx context.Context, localWorkspace string) error {
	id, err := h.findExisting(ctx, localWorkspace)
	if err != nil {
		return err
	}
	if id == "" {
		return ErrContainerNotFound
	}
	return h.StopByID(ctx, id)
}

// StopByID stops a container by ID.
func (h *Host) StopByID(ctx context.Context, id string) error {
	timeoutSec := int(h.opts.StopTimeout.Seconds())
	err := h.client.ContainerStop(ctx, id, container.StopOptions{Timeout: &timeoutSec})
	if err != nil {
		// "container is not running" is fine — the desired state is
		// reached.
		if isAlreadyStoppedErr(err) {
			return nil
		}
		return errorf("stop: %w", err)
	}
	return nil
}

// Remove stops (if running) and removes the devcontainer for a given
// workspace, including its anonymous volumes.
func (h *Host) Remove(ctx context.Context, localWorkspace string) error {
	id, err := h.findExisting(ctx, localWorkspace)
	if err != nil {
		return err
	}
	if id == "" {
		return ErrContainerNotFound
	}
	return h.RemoveByID(ctx, id)
}

// RemoveByID is Remove when you already have a container ID.
func (h *Host) RemoveByID(ctx context.Context, id string) error {
	if err := h.StopByID(ctx, id); err != nil && !errors.Is(err, ErrContainerNotFound) {
		// Best-effort stop; proceed to remove with Force regardless.
		h.logf("stop before remove failed (continuing with force): %v", err)
	}
	return h.client.ContainerRemove(ctx, id, container.RemoveOptions{
		Force:         true,
		RemoveVolumes: true,
	})
}

func isAlreadyStoppedErr(err error) bool {
	if err == nil {
		return false
	}
	// Docker SDK has IsErrNotFound but no first-class "not running"
	// helper; fall back to a substring check on the message. This is
	// brittle but harmless: if the check misfires we just return the
	// docker error verbatim, which is still informative.
	msg := err.Error()
	for _, s := range []string{"not running", "is not running", "is already stopped"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
