package dockercompose

import (
	"context"
	"fmt"
	"sort"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/runner"
	"github.com/wyrd-company/devcontainer-builder/pkg/lifecycle"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

// UpRequest is everything Up needs. The caller resolves features
// (typically via pkg/features) and supplies a derived image — or, if
// they want the package to build it, leaves PrimaryImage empty and
// supplies an ImageBuilder.
type UpRequest struct {
	// Spec is the parsed devcontainer.json (must be in compose mode).
	Spec *spec.DevContainer
	// SpecPath is the host-side path the spec was loaded from. Used
	// for the devcontainer.config_file label on every container.
	SpecPath string
	// LocalWorkspace is the host workspace path; absolute. Used as
	// the project name source and the workspace bind-mount source.
	LocalWorkspace string
	// ContainerWorkspace is the in-container workspace folder. When
	// empty, falls back to spec.WorkspaceFolder, then
	// "/workspaces/<basename>".
	ContainerWorkspace string
	// PrimaryImage, when set, overrides the primary service's image
	// in the override layer. Used to point compose at a feature-built
	// derived image.
	PrimaryImage string
	// Features as resolved by pkg/features. Their containerEnv and
	// lifecycle commands are merged into the primary service.
	Features []features.Resolved
	// LocalEnv is forwarded to compose's `${VAR}` interpolation and
	// to ${localEnv:VAR} substitution in the spec.
	LocalEnv map[string]string
}

// UpResult reports what happened.
type UpResult struct {
	// ProjectName is the resolved compose project name.
	ProjectName string
	// PrimaryContainerID is the ID of the container the spec's
	// `service` runs in.
	PrimaryContainerID string
	// ContainerIDs maps service name → container ID for every service
	// that was started this call (or reused).
	ContainerIDs map[string]string
	// CreatedThisCall enumerates services whose container was created
	// this call (vs reused).
	CreatedThisCall []string
	// StartedThisCall enumerates services whose container had to be
	// started this call (vs already running).
	StartedThisCall []string
	// PhasesRun lists lifecycle phases that actually ran on the
	// primary (markers may have skipped some).
	PhasesRun []lifecycle.Hook
}

// Up brings the compose project up: build override, load+merge
// project, ensure networks/volumes, find-or-create each service in
// dependency order, then run lifecycle on the primary. Idempotent.
func (h *Host) Up(ctx context.Context, req UpRequest) (*UpResult, error) {
	if req.Spec == nil {
		return nil, errorf("UpRequest.Spec is required")
	}
	if req.Spec.Mode() != spec.ModeCompose {
		return nil, errorf("UpRequest.Spec is not in compose mode")
	}
	if req.LocalWorkspace == "" {
		return nil, errorf("UpRequest.LocalWorkspace is required")
	}

	containerWorkspace := req.ContainerWorkspace
	if containerWorkspace == "" {
		containerWorkspace = req.Spec.WorkspaceFolder
	}
	if containerWorkspace == "" {
		containerWorkspace = "/workspaces/" + basename(req.LocalWorkspace)
	}
	containerWorkspace = ensurePosixPath(containerWorkspace)

	idLabels := map[string]string{LabelLocalFolder: req.LocalWorkspace}
	if req.SpecPath != "" {
		idLabels[LabelConfigFile] = req.SpecPath
	}

	subCtx := substitute.Context{
		LocalWorkspaceFolder:     req.LocalWorkspace,
		ContainerWorkspaceFolder: containerWorkspace,
		LocalEnv:                 req.LocalEnv,
		IDLabels:                 idLabels,
	}
	dc := substituteSpec(req.Spec, subCtx)

	overrideEnv := stringMap(dc.UpdateRemoteEnv)
	for _, f := range req.Features {
		for k, v := range f.Metadata.ContainerEnv {
			if overrideEnv == nil {
				overrideEnv = map[string]string{}
			}
			overrideEnv[k] = v
		}
	}
	overrideCommand := true
	if req.Spec.OverrideCommand != nil {
		overrideCommand = *req.Spec.OverrideCommand
	}

	override, err := OverrideYAML(OverrideInputs{
		Spec:               dc,
		LocalWorkspace:     req.LocalWorkspace,
		ContainerWorkspace: containerWorkspace,
		PrimaryImage:       req.PrimaryImage,
		RemoteEnv:          overrideEnv,
		OverrideCommand:    overrideCommand,
	})
	if err != nil {
		return nil, errorf("override yaml: %w", err)
	}

	proj, err := LoadProject(ctx, LoadInputs{
		Spec:      req.Spec,
		Workspace: req.LocalWorkspace,
		LocalEnv:  req.LocalEnv,
		Override:  override,
	})
	if err != nil {
		return nil, err
	}

	netNames, err := h.ensureNetworks(ctx, proj)
	if err != nil {
		return nil, err
	}
	volNames, err := h.ensureVolumes(ctx, proj)
	if err != nil {
		return nil, err
	}

	res := &UpResult{
		ProjectName:  proj.Name,
		ContainerIDs: map[string]string{},
	}

	startNames, err := selectServices(proj, req.Spec.RunServices)
	if err != nil {
		return nil, err
	}

	if err := proj.ForEachService(startNames, func(name string, svc *types.ServiceConfig) error {
		isPrimary := name == req.Spec.Service
		// Wait for any service_healthy / service_completed_successfully
		// deps before creating the dependent. ForEachService visits in
		// topological order, so the dep's container ID is already in
		// res.ContainerIDs by the time we get here.
		if err := h.waitDependencies(ctx, *svc, res.ContainerIDs); err != nil {
			return fmt.Errorf("service %q: %w", name, err)
		}
		labels := containerLabels(proj.Name, req.LocalWorkspace, req.SpecPath, name, dependsOnList(*svc), isPrimary)
		id, created, started, err := h.bringUp(ctx, proj, *svc, labels, netNames, volNames)
		if err != nil {
			return fmt.Errorf("service %q: %w", name, err)
		}
		res.ContainerIDs[name] = id
		if created {
			res.CreatedThisCall = append(res.CreatedThisCall, name)
		}
		if started {
			res.StartedThisCall = append(res.StartedThisCall, name)
		}
		if isPrimary {
			res.PrimaryContainerID = id
		}
		return nil
	}); err != nil {
		return res, err
	}

	if res.PrimaryContainerID == "" {
		return res, fmt.Errorf("%w: %q", ErrPrimaryNotFound, req.Spec.Service)
	}

	sort.Strings(res.CreatedThisCall)
	sort.Strings(res.StartedThisCall)

	if err := h.runLifecycle(ctx, dc, req.Features, res.PrimaryContainerID, containerWorkspace, res); err != nil {
		return res, err
	}
	return res, nil
}

// bringUp finds or creates a container for one service and starts it
// if not running. Returns (id, createdThisCall, startedThisCall).
func (h *Host) bringUp(
	ctx context.Context, proj *types.Project, svc types.ServiceConfig,
	labels map[string]string, netNames, volNames map[string]string,
) (string, bool, bool, error) {
	containerName := composeContainerName(proj.Name, svc.Name)
	id, err := h.findExisting(ctx, proj.Name, svc.Name)
	if err != nil {
		return "", false, false, err
	}
	created := false
	if id == "" {
		t, err := translateService(proj, svc, containerName, labels, netNames, volNames)
		if err != nil {
			return "", false, false, err
		}
		resp, err := h.client.ContainerCreate(ctx, t.Config, t.HostConfig, t.NetConfig, nil, t.Name)
		if err != nil {
			return "", false, false, errorf("create %q: %w", containerName, err)
		}
		id = resp.ID
		created = true
		h.logf("created container %s (%s)", containerName, short(id))
	} else {
		h.logf("reusing container %s (%s)", containerName, short(id))
	}

	inspect, err := h.client.ContainerInspect(ctx, id)
	if err != nil {
		return id, created, false, errorf("inspect %s: %w", short(id), err)
	}
	started := false
	if !inspect.State.Running {
		if err := h.client.ContainerStart(ctx, id, container.StartOptions{}); err != nil {
			return id, created, false, errorf("start %s: %w", short(id), err)
		}
		started = true
		h.logf("started container %s", short(id))
	}
	return id, created, started, nil
}

// findExisting returns the container ID for a (project, service) pair,
// or "" if none. Uses compose's own labels so a container created by
// `docker compose` is reused.
func (h *Host) findExisting(ctx context.Context, projectName, serviceName string) (string, error) {
	args := filters.NewArgs(
		filters.Arg("label", LabelProject+"="+projectName),
		filters.Arg("label", LabelService+"="+serviceName),
	)
	list, err := h.client.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		return "", errorf("list containers: %w", err)
	}
	if len(list) == 0 {
		return "", nil
	}
	return list[0].ID, nil
}

// composeContainerName matches docker compose v2's default
// container_name format: <project>-<service>-<number>. Container
// number is always 1 in this iteration (no scale support).
func composeContainerName(projectName, serviceName string) string {
	return fmt.Sprintf("%s-%s-1", projectName, serviceName)
}

// selectServices returns the set of service names to bring up. When
// runServices is empty, returns every defined service. When set,
// validates each name and returns the slice (ForEachService pulls in
// dependencies automatically).
func selectServices(proj *types.Project, runServices []string) ([]string, error) {
	if len(runServices) == 0 {
		return proj.ServiceNames(), nil
	}
	for _, n := range runServices {
		if _, err := proj.GetService(n); err != nil {
			return nil, fmt.Errorf("runServices: unknown service %q", n)
		}
	}
	return runServices, nil
}

// runLifecycle drives the lifecycle plan against the primary
// container. Same shape as the docker host: detect home, detect
// container env (for ${containerEnv:VAR}), then runner.Run.
func (h *Host) runLifecycle(
	ctx context.Context, dc *spec.DevContainer, resolved []features.Resolved,
	primaryID, containerWorkspace string, res *UpResult,
) error {
	inspect, err := h.client.ContainerInspect(ctx, primaryID)
	if err != nil {
		return errorf("inspect primary: %w", err)
	}
	execer := h.Execer(primaryID)
	homeDir, err := runner.DetectHome(ctx, execer, dc.RemoteUser)
	if err != nil {
		return errorf("detect home: %w", err)
	}
	udf := lifecycle.UserDataFolder(homeDir, "")
	containerEnv, err := runner.DetectContainerEnv(ctx, execer)
	if err != nil {
		h.logf("warning: detect container env: %v", err)
		containerEnv = map[string]string{}
	}
	dc = substituteContainerEnv(dc, containerEnv)
	mergedEnv := stringMap(dc.UpdateRemoteEnv)

	plan := lifecycle.Plan(dc, resolved)
	rt := runner.Runtime{
		User:           dc.RemoteUser,
		Workdir:        containerWorkspace,
		Env:            mergedEnv,
		CreatedAt:      inspect.Created,
		StartedAt:      inspect.State.StartedAt,
		UserDataFolder: udf,
	}
	if err := runner.Run(ctx, execer, plan, rt, runner.Options{
		Logger: h.opts.Logger,
		Stdout: h.opts.Stdout,
		Stderr: h.opts.Stderr,
	}); err != nil {
		return err
	}
	for _, p := range plan {
		res.PhasesRun = append(res.PhasesRun, p.Hook)
	}
	return nil
}
