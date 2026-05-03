package kubernetes

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/runner"
	"github.com/wyrd-company/devcontainer-builder/pkg/lifecycle"
	"github.com/wyrd-company/devcontainer-builder/pkg/projection/kubernetes"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

// UpRequest captures everything Up needs. Fields mirror
// pkg/host/docker.UpRequest where possible, plus the few
// kubernetes-specific knobs (Namespace, Name, ContainerName,
// WorkspaceVolumeSource) that pkg/projection/kubernetes already takes.
type UpRequest struct {
	// Spec is the parsed devcontainer.json. Required.
	Spec *spec.DevContainer
	// SpecPath is the host-side path the spec was loaded from. Stored
	// in the AnnotationConfigFile annotation so downstream tooling
	// can reverse-look-up the source file.
	SpecPath string
	// LocalWorkspace is the host-side workspace folder (absolute).
	// Required: it's the discovery key (the LabelLocalFolderHash label
	// is sha256(LocalWorkspace)) and the default HostPath source for
	// the workspace volume.
	LocalWorkspace string
	// ContainerWorkspace is the path inside the container where the
	// workspace is mounted. When empty, falls back to spec
	// workspaceFolder, then "/workspaces/<basename>".
	ContainerWorkspace string
	// Image is the already-built image reference (use pkg/builder to
	// produce one).
	Image string
	// Features as resolved by pkg/features. Their containerEnv,
	// init/privileged/capAdd/securityOpt, and mounts merge into the
	// pod the same way pkg/projection/kubernetes merges them.
	Features []features.Resolved
	// LocalEnv is the host environment used for ${localEnv:VAR}
	// substitution. Pass an empty map (not nil) for no host env.
	LocalEnv map[string]string
	// IDLabels seeds ${devcontainerId} substitution. The
	// AnnotationLocalFolder + AnnotationConfigFile pair is always
	// included; supply additional entries here if you want them folded
	// into the id hash.
	IDLabels map[string]string

	// Namespace overrides Options.Namespace for this call. Empty falls
	// back to Options.Namespace, then "default".
	Namespace string
	// Name is the metadata.name written onto the projected Pod. Empty
	// is allowed: Up will fall back to "devcontainer-<localFolderHash>"
	// which is stable across reruns and matches the discovery key.
	Name string
	// ContainerName overrides the projected container's name. Empty
	// means pkg/projection/kubernetes.DefaultContainerName.
	ContainerName string
	// Labels and Annotations seed the projected Pod's metadata. The
	// host adds AnnotationLocalFolder, AnnotationConfigFile, and
	// LabelLocalFolderHash on top.
	Labels      map[string]string
	Annotations map[string]string
	// WorkspaceVolumeSource overrides the default HostPath workspace
	// volume. See pkg/projection/kubernetes.ProjectRequest.
	WorkspaceVolumeSource *corev1.VolumeSource
	// ReadyTimeout overrides Options.ReadyTimeout for this call.
	ReadyTimeout time.Duration
}

// UpResult reports what happened. Symmetrical with the docker host's
// UpResult so embedders can log "created+started" vs "reused+lifecycle"
// the same way regardless of host.
type UpResult struct {
	Namespace       string
	Name            string
	ContainerName   string
	UID             string
	CreatedThisCall bool
	// PhasesRun lists the lifecycle phases that actually ran (markers
	// may have skipped some).
	PhasesRun []lifecycle.Hook
}

// Up brings the devcontainer up: find or create the Pod, wait for it
// to reach Ready, then run the appropriate lifecycle phases gated by
// marker files. Idempotent: calling Up twice on the same workspace
// reuses the existing pod and only re-runs the phases whose markers
// indicate they need to.
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

	ns := h.namespaceOr(req.Namespace)
	hash := LocalFolderHash(req.LocalWorkspace)
	podName := req.Name
	if podName == "" {
		podName = "devcontainer-" + hash
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
		AnnotationLocalFolder: req.LocalWorkspace,
	}
	if req.SpecPath != "" {
		idLabels[AnnotationConfigFile] = req.SpecPath
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

	// 1. Discover existing pod by the local-folder-hash label. We
	// can't filter on annotations server-side, so the hash label is
	// what makes List cheap.
	existing, err := h.findExisting(ctx, ns, hash)
	if err != nil {
		return nil, err
	}

	res := &UpResult{Namespace: ns, ContainerName: req.ContainerName}
	if res.ContainerName == "" {
		res.ContainerName = kubernetes.DefaultContainerName
	}

	var pod *corev1.Pod
	if existing != nil {
		pod = existing
		res.Name = pod.Name
		res.UID = string(pod.UID)
		h.logf("reusing pod %s/%s", ns, pod.Name)
	} else {
		built, err := h.buildPod(dc, req, ns, podName, hash, containerWorkspace)
		if err != nil {
			return nil, err
		}
		created, err := h.cs.CoreV1().Pods(ns).Create(ctx, built, metav1.CreateOptions{})
		if err != nil {
			// Race: another caller may have created the same pod
			// between our List and Create. Re-fetch and continue.
			if apierrors.IsAlreadyExists(err) {
				got, getErr := h.cs.CoreV1().Pods(ns).Get(ctx, built.Name, metav1.GetOptions{})
				if getErr != nil {
					return nil, errorf("get after AlreadyExists: %w", getErr)
				}
				created = got
			} else {
				return nil, errorf("create pod: %w", err)
			}
		} else {
			res.CreatedThisCall = true
			h.logf("created pod %s/%s", ns, created.Name)
		}
		pod = created
		res.Name = pod.Name
		res.UID = string(pod.UID)
	}

	// 2. Wait until the target container reports Ready. Mirrors the
	// docker host's "start, then re-inspect for StartedAt" — on
	// kubernetes the Ready condition is the right signal because the
	// kubelet may pull the image, run init containers, etc.
	timeout := req.ReadyTimeout
	if timeout == 0 {
		timeout = h.opts.ReadyTimeout
	}
	ready, err := h.waitContainerReady(ctx, ns, pod.Name, res.ContainerName, timeout)
	if err != nil {
		return res, err
	}

	// 3. Run lifecycle phases via the apiserver exec subresource.
	execer := h.Execer(ns, pod.Name, res.ContainerName)
	homeDir, err := runner.DetectHome(ctx, execer, dc.RemoteUser)
	if err != nil {
		return res, errorf("detect home: %w", err)
	}
	udf := lifecycle.UserDataFolder(homeDir, "")
	containerEnv, err := runner.DetectContainerEnv(ctx, execer)
	if err != nil {
		// Non-fatal: lifecycle commands will see whatever the shell
		// inherits from PID 1. Log and continue with remoteEnv only.
		h.logf("warning: detect container env: %v", err)
		containerEnv = map[string]string{}
	}
	dc = substituteContainerEnv(dc, containerEnv)
	mergedEnv := mergeStringMaps(stringMapFromAny(dc.UpdateRemoteEnv), nil)

	plan := lifecycle.Plan(dc, req.Features)
	rt := runner.Runtime{
		User:    dc.RemoteUser,
		Workdir: containerWorkspace,
		Env:     mergedEnv,
		// CreatedAt is the pod's CreationTimestamp; StartedAt is the
		// per-container start time inside the status — both must
		// change when the pod is recreated / restarted so WhenOnce
		// and WhenStart phases re-run.
		CreatedAt:      pod.CreationTimestamp.UTC().Format(time.RFC3339Nano),
		StartedAt:      ready.StartedAt,
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

// buildPod runs the pure projection and folds in the
// imperative-host-only fields (Name override, discovery label,
// namespace).
func (h *Host) buildPod(
	dc *spec.DevContainer, req UpRequest, ns, podName, hash, containerWorkspace string,
) (*corev1.Pod, error) {
	labels := cloneStringMap(req.Labels)
	if labels == nil {
		labels = map[string]string{}
	}
	labels[LabelLocalFolderHash] = hash

	pj, err := kubernetes.Project(kubernetes.ProjectRequest{
		Spec:                  dc,
		SpecPath:              req.SpecPath,
		LocalWorkspace:        req.LocalWorkspace,
		ContainerWorkspace:    containerWorkspace,
		Image:                 req.Image,
		Features:              req.Features,
		Name:                  podName,
		Namespace:             ns,
		ContainerName:         req.ContainerName,
		Labels:                labels,
		Annotations:           cloneStringMap(req.Annotations),
		WorkspaceVolumeSource: req.WorkspaceVolumeSource,
	})
	if err != nil {
		return nil, fmt.Errorf("kubernetes host: project: %w", err)
	}
	return pj.Pod, nil
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
