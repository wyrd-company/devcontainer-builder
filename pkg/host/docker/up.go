package docker

import (
	"context"
	"strings"

	"github.com/docker/docker/api/types/container"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/runner"
	"github.com/wyrd-company/devcontainer-builder/pkg/lifecycle"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

// UpRequest captures everything Up needs. The caller is responsible
// for: building/tagging the image (use pkg/builder), resolving feature
// metadata, and passing the host environment in for variable
// substitution. Up does not read os.Environ() itself — that's the
// CLI's responsibility, kept out of the package so a service or
// operator can supply a curated environment.
type UpRequest struct {
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
	// workspaceFolder, then "/workspaces/<basename>".
	ContainerWorkspace string
	// Image is the already-built image reference (use pkg/builder to
	// produce one).
	Image string
	// Features as resolved by pkg/features. Their containerEnv and
	// lifecycle commands are merged into the container.
	Features []features.Resolved
	// LocalEnv is the host environment used for ${localEnv:VAR}
	// substitution. Pass an empty map (not nil) for no host env.
	LocalEnv map[string]string
	// IDLabels seeds ${devcontainerId} substitution. The
	// devcontainer.local_folder + devcontainer.config_file labels are
	// always included; supply additional labels here if you want them
	// folded into the id hash.
	IDLabels map[string]string
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
	ContainerID    string
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

	idLabels := map[string]string{
		LabelLocalFolder: req.LocalWorkspace,
	}
	if req.SpecPath != "" {
		idLabels[LabelConfigFile] = req.SpecPath
	}
	for k, v := range req.IDLabels {
		idLabels[k] = v
	}

	subCtx := substitute.Context{
		LocalWorkspaceFolder:     req.LocalWorkspace,
		ContainerWorkspaceFolder: containerWorkspace,
		LocalEnv:                 req.LocalEnv,
		IDLabels:                 idLabels,
	}
	dc := substituteSpec(req.Spec, subCtx)

	// 1. Discover or create.
	id, err := h.findExisting(ctx, req.LocalWorkspace)
	if err != nil {
		return nil, err
	}
	res := &UpResult{}
	if id == "" {
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
	if req.RemoteUserUIDUpdate != nil &&
		(dc.UpdateRemoteUserUID == nil || *dc.UpdateRemoteUserUID) {
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
	udf := lifecycle.UserDataFolder(homeDir, "")
	containerEnv, err := runner.DetectContainerEnv(ctx, execer)
	if err != nil {
		// Non-fatal: lifecycle commands will just see whatever shell sets
		// up. Log and continue with the spec's remoteEnv only.
		h.logf("warning: detect container env: %v", err)
		containerEnv = map[string]string{}
	}
	// Second-phase substitution: ${containerEnv:VAR}.
	dc = substituteContainerEnv(dc, containerEnv)
	mergedEnv := mergeStringMaps(stringMap(dc.UpdateRemoteEnv), nil)

	plan := lifecycle.Plan(dc, req.Features)
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
		return res, err
	}
	for _, p := range plan {
		res.PhasesRun = append(res.PhasesRun, p.Hook)
	}
	return res, nil
}

func (h *Host) create(
	ctx context.Context, dc *spec.DevContainer, req UpRequest, containerWorkspace string,
) (string, error) {
	cfg, host, err := buildContainerConfig(dc, req.Features, req.Image, containerWorkspace, req.LocalWorkspace, req.SpecPath)
	if err != nil {
		return "", errorf("build container config: %w", err)
	}
	resp, err := h.client.ContainerCreate(ctx, cfg, host, nil, nil, "")
	if err != nil {
		return "", errorf("create: %w", err)
	}
	return resp.ID, nil
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

func mergeStringMaps(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
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
