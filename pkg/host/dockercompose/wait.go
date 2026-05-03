package dockercompose

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/container"
)

// waitDependencies blocks until each of svc's required depends_on
// entries has reached the condition declared in the compose file. It's
// invoked from the ForEachService callback in Up before the dependent
// service is created, so by the time control reaches a service every
// dep has already been brought up by an earlier callback (and recorded
// in containerIDs).
//
// service_started: satisfied implicitly — bringUp on the dep already
// returned, so the dep is at minimum running. No inspect needed.
//
// service_healthy: poll inspect until State.Health.Status == "healthy".
// If the dep image has no HEALTHCHECK the dep can never report healthy;
// returning an error there is more useful than blocking forever.
//
// service_completed_successfully: poll inspect until State.Status ==
// "exited" with ExitCode 0. A non-zero exit is fatal.
func (h *Host) waitDependencies(
	ctx context.Context,
	svc types.ServiceConfig,
	containerIDs map[string]string,
) error {
	if len(svc.DependsOn) == 0 {
		return nil
	}
	timeout := h.opts.DependencyTimeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	for _, name := range sortedDepKeys(svc.DependsOn) {
		dep := svc.DependsOn[name]
		if !dep.Required {
			continue
		}
		cond := dep.Condition
		if cond == "" {
			cond = types.ServiceConditionStarted
		}
		if cond == types.ServiceConditionStarted {
			continue
		}
		id, ok := containerIDs[name]
		if !ok || id == "" {
			return fmt.Errorf("dependency %q has no container to wait on", name)
		}
		if err := h.waitForCondition(ctx, id, name, cond, timeout); err != nil {
			return err
		}
	}
	return nil
}

// waitForCondition polls ContainerInspect until the dep reaches the
// requested condition, ctx is cancelled, or the per-dep timeout fires.
func (h *Host) waitForCondition(
	ctx context.Context, id, name, condition string, timeout time.Duration,
) error {
	deadline := time.Now().Add(timeout)
	const interval = 500 * time.Millisecond
	for {
		insp, err := h.client.ContainerInspect(ctx, id)
		if err != nil {
			return errorf("inspect %s for %s on %q: %w", short(id), condition, name, err)
		}
		done, err := evalCondition(name, condition, insp.State)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout after %s waiting for dependency %q to reach %s", timeout, name, condition)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(interval):
		}
	}
}

// evalCondition returns (true, nil) when the supplied state satisfies
// the requested condition, (false, nil) when the caller should keep
// polling, or (_, err) when the condition can never be met (e.g.
// service_healthy on a container with no healthcheck, or
// service_completed_successfully with a non-zero exit).
func evalCondition(name, condition string, state *container.State) (bool, error) {
	if state == nil {
		return false, nil
	}
	switch condition {
	case types.ServiceConditionHealthy:
		if state.Health == nil {
			return false, fmt.Errorf("dependency %q requires service_healthy but the container has no healthcheck", name)
		}
		switch state.Health.Status {
		case container.Healthy:
			return true, nil
		case container.Unhealthy:
			return false, fmt.Errorf("dependency %q reported unhealthy", name)
		}
		return false, nil
	case types.ServiceConditionCompletedSuccessfully:
		if state.Status == "exited" {
			if state.ExitCode == 0 {
				return true, nil
			}
			return false, fmt.Errorf("dependency %q exited with code %d", name, state.ExitCode)
		}
		return false, nil
	default:
		return false, fmt.Errorf("dependency %q: unsupported condition %q", name, condition)
	}
}

// sortedDepKeys returns the keys of a DependsOnConfig in stable order.
func sortedDepKeys(d types.DependsOnConfig) []string {
	keys := make([]string, 0, len(d))
	for k := range d {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
