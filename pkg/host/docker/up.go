// relationships:
//   references: [metadata, spawn, lifecycle]

package docker

import (
	"context"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/errdefs"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/runner"
	"github.com/wyrd-company/devcontainer-builder/pkg/lifecycle"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
	"github.com/wyrd-company/devcontainer-builder/pkg/workspace"
)

// UpRequest captures everything Up needs. The caller is responsible
// for: building/tagging the image (use pkg/builder), resolving feature
// metadata, and passing the host environment in for variable
// substitution. Up does not read os.Environ() itself — that's the
// CLI's responsibility, kept out of the package so a service or
// operator can supply a curated environment.
type UpRequest struct {
	// ContainerDataFolder selects lifecycle and dotfiles marker storage.
	ContainerDataFolder string
	// ContainerSystemDataFolder selects the upstream system patch marker folder.
	ContainerSystemDataFolder string
	// GPUAvailability controls implicit hostRequirements.gpu requests. Empty means detect.
	GPUAvailability string
	// OmitConfigRemoteEnvFromMetadata omits the current config remoteEnv from new labels.
	OmitConfigRemoteEnvFromMetadata bool
	// Spec is the parsed devcontainer.json. Required.
	Spec *spec.DevContainer
	// SpecPath is the host-side path the spec was loaded from. Stored
	// in the devcontainer.config_file label so upstream tooling can
	// reverse-look-up the source file.
	SpecPath string
	// LocalWorkspace is the host-side workspace folder (absolute).
	// Required: it's the discovery key (devcontainer.local_folder
	// label) and the default bind-mount source.
	LocalWorkspace string
	// ContainerWorkspace is the path inside the container where the
	// workspace is mounted. When empty, falls back to spec
	// workspaceFolder, then "/workspaces/<basename>". Without a spec
	// workspaceFolder, a reused container's bind mounts take precedence.
	ContainerWorkspace string
	// Image is the image reference to run (use pkg/builder to produce
	// one). Up pulls it when the daemon does not have it.
	Image string
	// ImageBuilt means Image was built from this request's spec and features.
	// Its label already contains those contributions; append only runtime updates.
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
	// ContainerSessionDataFolder stores user environment probe caches.
	ContainerSessionDataFolder string
	// Secrets are secret environment variables for lifecycle commands and
	// the dotfiles install (upstream --secrets-file). They win over every
	// other layer and are never stored in container configuration, labels
	// or files.
	Secrets map[string]string
	// metadataEntries carries the complete raw list to container creation.
	metadataEntries []metadata.Entry
	entrypoints     []string
	// Features as resolved by pkg/features. Their containerEnv and
	// lifecycle commands are merged into the container.
	Features []features.Resolved
	// LocalEnv is the host environment used for ${localEnv:VAR}
	// substitution. Pass an empty map (not nil) for no host env.
	LocalEnv map[string]string
	// IDLabels, when non-empty, replace the workspace labels
	// (devcontainer.local_folder, devcontainer.config_file) as the
	// container's identity, as upstream --id-label does: Up reuses only a
	// container that carries all of them, sets exactly them on a new
	// container, and derives ${devcontainerId} from them.
	IDLabels map[string]string
	// WorkspaceMount, when non-nil, replaces the default workspace bind
	// mount (LocalWorkspace at the container workspace folder), for
	// example to mount the workspace's Git root. A spec workspaceMount
	// still wins.
	WorkspaceMount *mount.Mount
	// Mounts are added after the spec's and features' mounts, for example
	// a CLI --mount or a Git worktree's common folder. Up adds no host
	// path that is not in the spec or here.
	Mounts []mount.Mount
	// RemoteUserUIDUpdate, when non-nil, asks the host to remap the
	// container's remoteUser to HostUID/HostGID after the container
	// starts so bind-mounted files land with host-side ownership. The
	// spec's `updateRemoteUserUID: false` overrides and disables the
	// remap regardless. The package never reads os.Getuid itself —
	// embedders pass the IDs they want applied.
	RemoteUserUIDUpdate *RemoteUserUIDUpdate
}

// UpResult reports what happened. Fields with a "thisCall" flag denote
// state changes attributable to *this* Up invocation, so a caller can
// log "created+started" vs "reused+lifecycle-only" cleanly.
type UpResult struct {
	ContainerID     string
	CreatedThisCall bool
	StartedThisCall bool
	// Phases lists the lifecycle phases that actually ran (markers may
	// have skipped some).
	PhasesRun []lifecycle.Hook
}

// Up brings the devcontainer up: find or create the container, start
// it if stopped, then run the appropriate lifecycle phases gated by
// marker files. Idempotent: calling Up twice on the same workspace
// reuses the existing container and only re-runs the phases whose
// markers indicate they need to.
func (h *Host) Up(ctx context.Context, req UpRequest) (*UpResult, error) {
	if req.Spec == nil {
		return nil, errorf("UpRequest.Spec is required")
	}
	if err := req.Spec.ValidateMounts(); err != nil {
		return nil, err
	}
	if req.LocalWorkspace == "" {
		return nil, errorf("UpRequest.LocalWorkspace is required")
	}
	if req.Image == "" {
		return nil, errorf("UpRequest.Image is required")
	}

	containerWorkspace := req.ContainerWorkspace
	if containerWorkspace == "" {
		containerWorkspace = req.Spec.WorkspaceFolder
	}
	if containerWorkspace == "" {
		containerWorkspace = "/workspaces/" + basename(req.LocalWorkspace)
	}
	containerWorkspace = ensurePosixPath(containerWorkspace)

	// 1. Discover. A reused container keeps the workspace placement it was
	// created with; without a spec workspaceFolder, read it from its mounts.
	var id string
	var err error
	if len(req.IDLabels) > 0 {
		id, err = h.findLabeled(ctx, req.IDLabels)
	} else {
		id, err = h.findExisting(ctx, req.LocalWorkspace)
	}
	if err != nil {
		return nil, err
	}
	if id != "" && req.Spec.WorkspaceFolder == "" {
		existing, err := h.client.ContainerInspect(ctx, id)
		if err != nil {
			return nil, errorf("inspect: %w", err)
		}
		if folder := workspace.MountedFolder(req.LocalWorkspace, bindMounts(existing.Mounts)); folder != "" {
			containerWorkspace = folder
		}
	}

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
	var entries []metadata.Entry

	// Create when none exists.
	res := &UpResult{}
	if id == "" {
		if err := h.ensureImage(ctx, req.Image); err != nil {
			return nil, err
		}
		img, inspectErr := h.client.ImageInspect(ctx, req.Image)
		if inspectErr != nil {
			return nil, errorf("inspect image metadata: %w", inspectErr)
		}
		var labels map[string]string
		if img.Config != nil {
			labels = img.Config.Labels
		}
		entries = imageMetadataEntries(req, labels, h.logf)
		runtimeEntries := entries
		if req.OmitConfigRemoteEnvFromMetadata {
			runtimeEntries = metadata.Update(entries, req.Spec)
		}
		dc, req.entrypoints, err = runtimeMetadata(req.Spec, runtimeEntries, subCtx)
		if err != nil {
			return nil, err
		}
		req.metadataEntries = entries
		// Image ENV already carries feature contributions. Keep this existing
		// runtime-only path for callers that resolve features without building.
		if !req.ImageBuilt {
			dc.ContainerEnv = featureContainerEnv(dc.ContainerEnv, req.Features, subCtx)
		}
		req.Features = nil
		id, err = h.create(ctx, dc, req, containerWorkspace)
		if err != nil {
			return nil, err
		}
		res.ContainerID = id
		res.CreatedThisCall = true
		h.logf("created container %s", short(id))
	} else {
		res.ContainerID = id
		h.logf("reusing container %s", short(id))
	}

	// 2. Start if not running.
	inspect, err := h.client.ContainerInspect(ctx, id)
	if err != nil {
		return res, errorf("inspect: %w", err)
	}
	if !res.CreatedThisCall {
		var labels map[string]string
		if inspect.Config != nil {
			labels = inspect.Config.Labels
		}
		entries = metadata.Read(labels, h.logf)
		if len(entries) > 0 {
			entries = metadata.Update(entries, req.Spec)
		} else {
			entries = metadata.Collect(nil, req.Features, req.Spec)
		}
		dc, _, err = runtimeMetadata(req.Spec, entries, subCtx)
		if err != nil {
			return res, err
		}
	}
	if dc.RemoteUser == "" {
		dc.RemoteUser = dc.ContainerUser
		if dc.RemoteUser == "" && inspect.Config != nil {
			dc.RemoteUser = inspect.Config.User
		}
	}
	if !inspect.State.Running {
		if err := h.client.ContainerStart(ctx, id, container.StartOptions{}); err != nil {
			return res, errorf("start: %w", err)
		}
		res.StartedThisCall = true
		h.logf("started container %s", short(id))
		// Re-inspect to pick up StartedAt.
		inspect, err = h.client.ContainerInspect(ctx, id)
		if err != nil {
			return res, errorf("inspect after start: %w", err)
		}
	}

	// 3. Optional: remap the remoteUser's UID/GID to the host's so
	// bind-mounted files don't land with surprising ownership. Runs
	// before lifecycle so onCreate/postCreate already see the new
	// IDs. Idempotent — converges on every Up.
	if shouldUpdateUID(dc, req.RemoteUserUIDUpdate) {
		if err := h.applyUIDUpdate(ctx, id, dc.RemoteUser, *req.RemoteUserUIDUpdate); err != nil {
			return res, err
		}
	}

	// 4. Run lifecycle phases through the shared runner.
	execer := h.Execer(id)
	homeDir, err := runner.DetectHome(ctx, execer, dc.RemoteUser)
	if err != nil {
		return res, errorf("detect home: %w", err)
	}
	udf := lifecycle.UserDataFolder(homeDir, req.ContainerDataFolder)
	containerEnv, err := runner.DetectContainerEnv(ctx, execer)
	if err != nil {
		// Non-fatal: lifecycle commands will just see whatever shell sets
		// up. Log and continue with the spec's remoteEnv only.
		h.logf("warning: detect container env: %v", err)
		containerEnv = map[string]string{}
	}
	// Second-phase substitution: ${containerEnv:VAR}.
	dc = substituteContainerEnv(dc, containerEnv)
	systemEnv := map[string]string{}
	if inspect.Config != nil {
		systemEnv = runner.EnvFromList(inspect.Config.Env)
	}
	if err := runner.PatchSystemEnv(ctx, execer, req.ContainerSystemDataFolder, systemEnv); err != nil {
		return res, err
	}
	var probed map[string]string
	if !req.SkipLifecycle {
		probed = runner.ProbeUserEnv(ctx, execer, dc.UserEnvProbe, req.DefaultUserEnvProbe, dc.RemoteUser, systemEnv, h.logf, runner.ProbeOptions{CanPatchSystemConfig: true, ContainerSessionDataFolder: req.ContainerSessionDataFolder})
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
		return res, err
	}
	for _, p := range runResult.RanPhases() {
		res.PhasesRun = append(res.PhasesRun, p.Hook)
	}
	return res, nil
}

func (h *Host) create(
	ctx context.Context, dc *spec.DevContainer, req UpRequest, containerWorkspace string,
) (string, error) {
	var cfg *container.Config
	var host *container.HostConfig
	var name string
	var err error
	if req.metadataEntries != nil {
		cfg, host, name, err = buildContainerConfigWithEntrypoints(dc, nil, req.entrypoints, req.Image, containerWorkspace, req.LocalWorkspace, req.SpecPath)
	} else {
		cfg, host, name, err = buildContainerConfig(dc, req.Features, req.Image, containerWorkspace, req.LocalWorkspace, req.SpecPath)
	}
	if err == nil && req.metadataEntries != nil {
		value, encodeErr := metadata.Encode(req.metadataEntries)
		if encodeErr != nil {
			return "", errorf("encode container metadata: %w", encodeErr)
		}
		cfg.Labels[metadata.Label] = value
	}
	if err != nil {
		return "", errorf("build container config: %w", err)
	}
	applyRequestIdentity(cfg, host, dc, req)
	if err := h.applyGPUAvailability(ctx, dc, req.GPUAvailability, host); err != nil {
		return "", err
	}
	if !overrideCommand(dc) && (len(req.entrypoints) > 0 || len(featureEntrypoints(req.Features)) > 0) {
		img, err := h.client.ImageInspect(ctx, req.Image)
		if err != nil {
			return "", errorf("inspect image %s: %w", req.Image, err)
		}
		if img.Config != nil {
			appendImageCommand(cfg, img.Config.Entrypoint, img.Config.Cmd)
		}
	}
	resp, err := h.client.ContainerCreate(ctx, cfg, host, nil, nil, name)
	if err != nil {
		if name != "" && errdefs.IsConflict(err) {
			return "", errorf("create: container name %q is already in use; choose another --name or remove the conflicting container: %w", name, err)
		}
		return "", errorf("create: %w", err)
	}
	return resp.ID, nil
}

// applyRequestIdentity applies the request's id labels, workspace mount and
// additional mounts to a configuration built from the spec.
func applyRequestIdentity(cfg *container.Config, host *container.HostConfig, dc *spec.DevContainer, req UpRequest) {
	if len(req.IDLabels) > 0 {
		delete(cfg.Labels, LabelLocalFolder)
		delete(cfg.Labels, LabelConfigFile)
		for key, value := range req.IDLabels {
			cfg.Labels[key] = value
		}
	}
	if req.WorkspaceMount != nil && dc.WorkspaceMount == "" && len(host.Mounts) > 0 {
		// Without a spec workspaceMount the default workspace mount is first.
		host.Mounts[0] = *req.WorkspaceMount
	}
	host.Mounts = append(host.Mounts, req.Mounts...)
}

// bindMounts lists a container's bind mounts for workspace.MountedFolder.
func bindMounts(points []container.MountPoint) []workspace.Bind {
	var out []workspace.Bind
	for _, m := range points {
		if m.Type == mount.TypeBind {
			out = append(out, workspace.Bind{Source: m.Source, Destination: m.Destination})
		}
	}
	return out
}

// substituteContainerEnv resolves ${containerEnv:VAR} inside a few
// known string-y fields. We deliberately don't substitute the entire
// spec (no need for two passes on workspace folders etc); only
// remoteEnv values and lifecycle commands look up containerEnv.
func substituteContainerEnv(dc *spec.DevContainer, env map[string]string) *spec.DevContainer {
	if dc == nil {
		return nil
	}
	cctx := substitute.ContainerContext{ContainerEnv: env}
	out := *dc
	if dc.UpdateRemoteEnv != nil {
		newEnv := make(map[string]any, len(dc.UpdateRemoteEnv))
		for k, v := range dc.UpdateRemoteEnv {
			if s, ok := v.(string); ok {
				newEnv[k] = substitute.ContainerSubstitute(cctx, s)
			} else {
				newEnv[k] = v
			}
		}
		out.UpdateRemoteEnv = newEnv
	}
	out.OnCreate = containerSubstituteCommand(cctx, dc.OnCreate)
	out.UpdateContent = containerSubstituteCommand(cctx, dc.UpdateContent)
	out.PostCreate = containerSubstituteCommand(cctx, dc.PostCreate)
	out.PostStart = containerSubstituteCommand(cctx, dc.PostStart)
	out.PostAttach = containerSubstituteCommand(cctx, dc.PostAttach)
	return &out
}

func containerSubstituteCommand(ctx substitute.ContainerContext, c spec.Command) spec.Command {
	if c.IsZero() {
		return c
	}
	out := spec.Command{}
	if c.Shell != "" {
		out.Shell = substitute.ContainerSubstitute(ctx, c.Shell)
	}
	if len(c.Args) > 0 {
		out.Args = make([]string, len(c.Args))
		for i, a := range c.Args {
			out.Args[i] = substitute.ContainerSubstitute(ctx, a)
		}
	}
	if len(c.Parallel) > 0 {
		out.Parallel = make(map[string]spec.Command, len(c.Parallel))
		for k, v := range c.Parallel {
			out.Parallel[k] = containerSubstituteCommand(ctx, v)
		}
	}
	return out
}

func basename(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}
