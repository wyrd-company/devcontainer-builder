// relationships:
//
//	references: [metadata, override, lifecycle]
package dockercompose

import (
	"context"
	"fmt"
	"sort"

	"github.com/compose-spec/compose-go/v2/types"
	"github.com/docker/docker/api/types/container"

	"github.com/wyrd-company/devcontainer-builder/internal/dockerimage"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/runner"
	"github.com/wyrd-company/devcontainer-builder/pkg/lifecycle"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

// UpRequest is everything Up needs. The caller resolves features
// (typically via pkg/features) and optionally supplies a derived image from
// BuildPrimaryImage. Otherwise the primary Compose service selects the image.
type UpRequest struct {
	// ContainerDataFolder selects lifecycle and dotfiles marker storage.
	ContainerDataFolder string
	// ContainerSystemDataFolder selects the upstream system patch marker folder.
	ContainerSystemDataFolder string
	// GPUAvailability controls implicit hostRequirements.gpu reservations. Empty means detect.
	GPUAvailability string
	// OmitConfigRemoteEnvFromMetadata omits the current config remoteEnv from new labels.
	OmitConfigRemoteEnvFromMetadata bool
	// Spec is the parsed devcontainer.json (must be in compose mode).
	Spec *spec.DevContainer
	// SpecPath is the host-side path the spec was loaded from. Used
	// for the devcontainer.config_file label on every container.
	SpecPath string
	// LocalWorkspace is the host workspace path; absolute. Used for
	// project identity and local workspace substitution.
	LocalWorkspace string
	// ContainerWorkspace is the in-container workspace folder. When
	// empty, falls back to spec.WorkspaceFolder, then "/" like upstream.
	// Used for lifecycle commands and substitution; Compose owns the mounts.
	ContainerWorkspace string
	// PrimaryImage, when set, overrides the primary service's image
	// in the override layer. Used to point compose at a feature-built
	// derived image.
	PrimaryImage string
	// ImageBuilt means PrimaryImage contains the complete current config and
	// feature metadata produced by BuildPrimaryImage.BuildResult, not only a
	// Compose service build. Such contributions must not be collected twice.
	ImageBuilt bool
	// SkipLifecycle suppresses all lifecycle commands for this invocation.
	SkipLifecycle bool
	// SkipPostAttach suppresses postAttachCommand for this invocation.
	SkipPostAttach bool
	// Dotfiles installs the user's dotfiles after postCreateCommand unless
	// SkipLifecycle is set.
	Dotfiles runner.Dotfiles
	// Lifecycle selects lifecycle stop points and prebuild behavior.
	Lifecycle runner.LifecycleControls
	// RemoteEnv is the caller's remote environment for lifecycle commands
	// and the dotfiles install (upstream --remote-env). The configuration's
	// remoteEnv wins for the same name, as upstream. It is not stored.
	RemoteEnv map[string]string
	// DefaultUserEnvProbe selects the shell probe when userEnvProbe is not configured.
	DefaultUserEnvProbe string
	// Secrets are secret environment variables for lifecycle commands and
	// the dotfiles install (upstream --secrets-file). They win over every
	// other layer and are never stored in container configuration, labels
	// or files.
	Secrets map[string]string
	// Features as resolved by pkg/features. Their containerEnv and
	// lifecycle commands are merged into the primary service.
	Features []features.Resolved
	// LocalEnv is forwarded to compose's `${VAR}` interpolation and
	// to ${localEnv:VAR} substitution in the spec. It's also the
	// lookup table for `environment:` source secrets/configs.
	LocalEnv map[string]string
	// Profiles activates compose service profiles. Services whose
	// `profiles:` are set to a non-empty list are excluded unless one
	// matches an entry here (or "*" is supplied). The primary service
	// (spec.Service) is always kept; its declared profiles are folded
	// into the active set so its own dependencies-by-profile come up.
	Profiles []string
	// Mounts are added to the primary service after the metadata mounts, in
	// devcontainer mount syntax (a CLI --mount). A named volume is a project
	// volume unless marked external=true. Up adds no host path that is not in
	// the configuration or here.
	Mounts []string
	// IDLabels, when non-empty, are set on the primary container and seed
	// ${devcontainerId} in place of the workspace labels, as upstream
	// --id-label does. The workspace labels stay on the primary so project
	// ownership checks and workspace-scoped Stop/Remove still apply.
	IDLabels map[string]string
	// AllowProjectConflict permits Up to reuse/recreate a project containing
	// another workspace's containers, matching upstream. The default refuses.
	AllowProjectConflict bool
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
	// RecreatedThisCall enumerates services whose container was
	// destroyed and recreated this call because its config-hash label
	// disagreed with the current spec.
	RecreatedThisCall []string
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
	if err := req.Spec.ValidateMounts(); err != nil {
		return nil, err
	}
	if req.LocalWorkspace == "" {
		return nil, errorf("UpRequest.LocalWorkspace is required")
	}

	containerWorkspace := req.ContainerWorkspace
	if containerWorkspace == "" {
		containerWorkspace = req.Spec.WorkspaceFolder
	}
	if containerWorkspace == "" {
		containerWorkspace = "/"
	}
	containerWorkspace = ensurePosixPath(containerWorkspace)

	idLabels := req.IDLabels
	if len(idLabels) == 0 {
		idLabels = map[string]string{LabelLocalFolder: req.LocalWorkspace}
		if req.SpecPath != "" {
			idLabels[LabelConfigFile] = req.SpecPath
		}
	}

	subCtx := substitute.Context{
		LocalWorkspaceFolder:     req.LocalWorkspace,
		ContainerWorkspaceFolder: containerWorkspace,
		LocalEnv:                 req.LocalEnv,
		IDLabels:                 idLabels,
	}
	dc := substituteSpec(req.Spec, subCtx)

	primary := OverrideInputs{Spec: dc, PrimaryImage: req.PrimaryImage}
	override, err := OverrideYAML(primary)
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

	// Check every project container before metadata, materialized configs, or
	// engine resources can be changed, even when its service is not selected.
	containers, err := h.listProjectContainers(ctx, proj.Name)
	if err != nil {
		return nil, err
	}
	if conflict := projectConflict(proj.Name, req.LocalWorkspace, containers); conflict != nil {
		if !req.AllowProjectConflict {
			return nil, conflict
		}
		h.logf("warning: --allow-project-conflict permits upstream behavior despite %s", conflict)
	}

	previousObjects, err := h.previousObjectRecords(ctx, req.LocalWorkspace, proj.Name, containers)
	if err != nil {
		return nil, err
	}

	// Filter the project by active profiles. The primary always
	// stays enabled — without it there's no devcontainer — and any
	// service named in spec.RunServices does too, matching `docker
	// compose up <svc>`'s behavior of implicitly activating each
	// argument's profiles.
	keepEnabled := append([]string{req.Spec.Service}, req.Spec.RunServices...)
	proj, err = applyProfiles(proj, req.Profiles, keepEnabled)
	if err != nil {
		return nil, errorf("apply profiles: %w", err)
	}
	// With NoRecreate an existing primary owns its captured runtime settings.
	// Avoid inspecting its replacement image or allocating current metadata mounts.
	existingPrimary := ""
	if h.opts.NoRecreate {
		existingPrimary, err = FindExisting(ctx, h.client, proj.Name, req.Spec.Service)
		if err != nil {
			return nil, err
		}
	}
	var entries []metadata.Entry
	if existingPrimary == "" {
		// Read image metadata before deriving the runtime service. Keep the raw list
		// separate so container labels retain unresolved substitutions and all hooks.
		svc := proj.Services[req.Spec.Service]
		img, err := dockerimage.InspectConfig(ctx, h.client, svc.Image, svc.Platform, h.opts.RegistryAuth, h.stderrOr(nil))
		if err != nil {
			return nil, errorf("inspect primary image metadata: %w", err)
		}
		var imageLabels map[string]string
		if img != nil {
			imageLabels = img.Labels
		}
		entries = imageMetadataEntries(req, imageLabels, h.logf)
		merged, err := runtimeMetadata(req.Spec, entries, subCtx)
		if err != nil {
			return nil, err
		}
		dc = merged.DevContainer
		if err := h.applyGPUAvailability(ctx, proj, dc, req.GPUAvailability); err != nil {
			return nil, err
		}
		applyPrimaryMetadata(proj, dc, req, subCtx)
		primary.Spec, primary.Entrypoints = dc, merged.Entrypoints
		primary.OverrideCommand = dc.OverrideCommand == nil || *dc.OverrideCommand
		if primary.OverrideCommand {
			svc = proj.Services[dc.Service]
			svc.Entrypoint = types.ShellCommand{"/bin/sh", "-c"}
			svc.Command = types.ShellCommand{keepaliveScript(primary.Entrypoints), "-"}
			svc.StdinOpen, svc.Tty = true, true
			proj.Services[dc.Service] = svc
		}
		if err := h.wrapPrimaryCommand(ctx, proj, primary); err != nil {
			return nil, err
		}
	}

	if existingPrimary == "" {
		if err := setPrimaryMetadata(proj, req.Spec.Service, entries); err != nil {
			return nil, err
		}
	}

	materializer := newObjectMaterializer(func() (string, error) { return h.allocateObjects(ctx, proj.Name, req.LocalWorkspace) }, req.LocalEnv)

	// Build any non-primary services that declare a compose-side
	// `build:` block. The primary's compose `build:` (if any) is
	// already baked into req.PrimaryImage by BuildPrimaryImage, so
	// it's skipped here.
	if !h.opts.SkipServiceBuild {
		built, err := BuildServiceImages(ctx, h.serviceBuilds(proj, req.Spec.Service))
		if err != nil {
			return nil, errorf("build services: %w", err)
		}
		applyBuiltImages(proj, built.Images)
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

	scope := labelScope{Project: proj.Name, WorkingDir: proj.WorkingDir, ComposeFiles: req.Spec.ComposeFiles(), LocalWorkspace: req.LocalWorkspace, SpecPath: req.SpecPath, IDLabels: req.IDLabels}
	if err := proj.ForEachService(startNames, func(name string, svc *types.ServiceConfig) error {
		isPrimary := name == req.Spec.Service
		// Wait for any service_healthy / service_completed_successfully
		// deps before creating the dependent. ForEachService visits in
		// topological order, so the dep's container ID is already in
		// res.ContainerIDs by the time we get here.
		if err := h.waitDependencies(ctx, *svc, res.ContainerIDs); err != nil {
			return fmt.Errorf("service %q: %w", name, err)
		}
		hash := serviceConfigHash(*svc)
		labels := containerLabels(scope, name, hash, dependsOnList(*svc), isPrimary)
		for k, v := range svc.Labels {
			if _, managed := labels[k]; !managed {
				labels[k] = v
			}
		}

		id, created, started, recreated, err := h.bringUp(ctx, proj, *svc, labels, hash, netNames, volNames, materializer)
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
		if recreated {
			res.RecreatedThisCall = append(res.RecreatedThisCall, name)
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
	sort.Strings(res.RecreatedThisCall)

	if err := h.runLifecycle(ctx, req, subCtx, res.PrimaryContainerID, containerWorkspace, res); err != nil {
		return res, err
	}
	if err := h.removeRetiredObjects(ctx, previousObjects); err != nil {
		return res, err
	}
	return res, nil
}

// bringUp finds or creates a container for one service and starts it
// if not running. Returns (id, createdThisCall, startedThisCall,
// recreatedThisCall).
//
// Recreate-on-config-change mirrors `docker compose up`: if an
// existing container's `com.docker.compose.config-hash` label
// disagrees with the just-computed expectedHash (or is missing on a
// container we created in an earlier release), the container is
// stopped, removed, and rebuilt from the current service spec.
// Disable with Options.NoRecreate.
func (h *Host) bringUp(
	ctx context.Context, proj *types.Project, svc types.ServiceConfig,
	labels map[string]string, expectedHash string,
	netNames, volNames map[string]string,
	materializer *objectMaterializer,
) (string, bool, bool, bool, error) {
	containerName := composeContainerName(proj.Name, svc.Name)
	id, err := FindExisting(ctx, h.client, proj.Name, svc.Name)
	if err != nil {
		return "", false, false, false, err
	}
	recreated := false
	if id != "" && !h.opts.NoRecreate {
		drifted, err := h.containerNeedsRecreate(ctx, id, expectedHash)
		if err != nil {
			return "", false, false, false, err
		}
		if drifted {
			h.logf("recreating container %s: config-hash changed", containerName)
			if err := h.removeContainer(ctx, id); err != nil {
				return "", false, false, false, errorf("recreate %q: %w", containerName, err)
			}
			id = ""
			recreated = true
		}
	}
	created := false
	if id == "" {
		t, err := translateService(proj, svc, containerName, labels, netNames, volNames, materializer)
		if err != nil {
			return "", false, false, false, err
		}
		// Image-only services need their layers before creation. The primary
		// also loads any image whose metadata came only from the registry.
		if labels[LabelPrimary] == "true" || (svc.Image != "" && svc.Build == nil) {
			if err := dockerimage.Ensure(ctx, h.client, svc.Image, h.opts.RegistryAuth, h.stderrOr(nil)); err != nil {
				return "", false, false, false, err
			}
		}
		resp, err := h.client.ContainerCreate(ctx, t.Config, t.HostConfig, t.NetConfig, nil, t.Name)
		if err != nil {
			return "", false, false, false, errorf("create %q: %w", containerName, err)
		}
		id = resp.ID
		created = true
		h.logf("created container %s (%s)", containerName, short(id))
	} else {
		h.logf("reusing container %s (%s)", containerName, short(id))
	}

	inspect, err := h.client.ContainerInspect(ctx, id)
	if err != nil {
		return id, created, false, recreated, errorf("inspect %s: %w", short(id), err)
	}
	started := false
	if !inspect.State.Running {
		if err := h.client.ContainerStart(ctx, id, container.StartOptions{}); err != nil {
			return id, created, false, recreated, errorf("start %s: %w", short(id), err)
		}
		started = true
		h.logf("started container %s", short(id))
	}
	return id, created, started, recreated, nil
}

// containerNeedsRecreate reports whether an existing container's
// config-hash label disagrees with the current expectedHash. Missing
// labels are treated as drift — older containers (or those produced
// by upstream `docker compose` with a different hash algo) need to
// be reconciled before they're trusted as up-to-date.
func (h *Host) containerNeedsRecreate(ctx context.Context, id, expectedHash string) (bool, error) {
	if expectedHash == "" {
		return false, nil
	}
	insp, err := h.client.ContainerInspect(ctx, id)
	if err != nil {
		return false, errorf("inspect %s: %w", short(id), err)
	}
	if insp.Config == nil {
		return true, nil
	}
	got := insp.Config.Labels[LabelConfigHash]
	return got != expectedHash, nil
}

// removeContainer stops (best-effort) and removes a container.
func (h *Host) removeContainer(ctx context.Context, id string) error {
	timeout := int(h.opts.StopTimeout.Seconds())
	if err := h.client.ContainerStop(ctx, id, container.StopOptions{Timeout: &timeout}); err != nil {
		if !isAlreadyStoppedErr(err) {
			h.logf("stop %s before recreate: %v", short(id), err)
		}
	}
	return h.client.ContainerRemove(ctx, id, container.RemoveOptions{Force: true})
}

// applyBuiltImages updates the service entries in proj.Services with
// freshly-built tags so the downstream spawn step sees the just-built
// image rather than the (possibly empty) compose-declared one.
// proj.Services is a map[string]ServiceConfig, so the entry has to be
// reassigned rather than mutated through the map index.
func applyBuiltImages(proj *types.Project, images map[string]string) {
	if len(images) == 0 {
		return
	}
	for name, tag := range images {
		svc, ok := proj.Services[name]
		if !ok {
			continue
		}
		svc.Image = tag
		proj.Services[name] = svc
	}
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

func (h *Host) runLifecycle(
	ctx context.Context, req UpRequest, subCtx substitute.Context,
	primaryID, containerWorkspace string, res *UpResult,
) error {
	inspect, err := h.client.ContainerInspect(ctx, primaryID)
	if err != nil {
		return errorf("inspect primary: %w", err)
	}
	var labels map[string]string
	if inspect.Config != nil {
		labels = inspect.Config.Labels
	}
	entries := metadata.Read(labels, h.logf)
	if len(entries) > 0 {
		entries = metadata.Update(entries, req.Spec)
	} else {
		entries = metadata.Collect(nil, req.Features, req.Spec)
	}
	merged, err := runtimeMetadata(req.Spec, entries, subCtx)
	if err != nil {
		return err
	}
	dc := merged.DevContainer
	if dc.RemoteUser == "" {
		dc.RemoteUser = dc.ContainerUser
		if dc.RemoteUser == "" && inspect.Config != nil {
			dc.RemoteUser = inspect.Config.User
		}
	}
	execer := h.Execer(primaryID)
	homeDir, err := runner.DetectHome(ctx, execer, dc.RemoteUser)
	if err != nil {
		return errorf("detect home: %w", err)
	}
	udf := lifecycle.UserDataFolder(homeDir, req.ContainerDataFolder)
	containerEnv, err := runner.DetectContainerEnv(ctx, execer)
	if err != nil {
		h.logf("warning: detect container env: %v", err)
		containerEnv = map[string]string{}
	}
	dc = substituteContainerEnv(dc, containerEnv)
	systemEnv := map[string]string{}
	if inspect.Config != nil {
		systemEnv = runner.EnvFromList(inspect.Config.Env)
	}
	if err := runner.PatchSystemEnv(ctx, execer, req.ContainerSystemDataFolder, systemEnv); err != nil {
		return err
	}
	var probed map[string]string
	if !req.SkipLifecycle {
		probed = runner.ProbeUserEnv(ctx, execer, dc.UserEnvProbe, req.DefaultUserEnvProbe, dc.RemoteUser, systemEnv, h.logf, runner.ProbeOptions{CanPatchSystemConfig: true})
	}
	mergedEnv := runner.LifecycleEnv(probed, req.RemoteEnv, stringMap(dc.UpdateRemoteEnv), req.Secrets)

	plan := runtimePlan(req, entries, subCtx, containerEnv)
	rt := runner.Runtime{
		User:           dc.RemoteUser,
		Workdir:        containerWorkspace,
		Env:            mergedEnv,
		CreatedAt:      inspect.Created,
		StartedAt:      inspect.State.StartedAt,
		UserDataFolder: udf,
	}
	if !req.SkipLifecycle {
		rt.Dotfiles = req.Dotfiles
		rt.Lifecycle = req.Lifecycle
		rt.Lifecycle.WaitFor = dc.WaitFor
	}
	runResult, err := runner.RunWithResult(ctx, execer, plan, rt, runner.Options{
		TerminalSize: h.opts.TerminalSize,
		Logger:       h.opts.Logger,
		Stdout:       h.opts.Stdout,
		Stderr:       h.opts.Stderr,
	})
	if err != nil {
		return err
	}
	for _, p := range runResult.RanPhases() {
		res.PhasesRun = append(res.PhasesRun, p.Hook)
	}
	return nil
}
