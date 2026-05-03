package kubernetes

import (
	"errors"
	"fmt"
	"path"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// Annotations written onto the projected Pod so upstream tooling (and
// pkg/host/kubernetes) can reverse-look-up which devcontainer /
// workspace produced the pod. Keys mirror the docker host labels
// exactly — see pkg/host/docker.LabelLocalFolder / LabelConfigFile —
// but live on annotations rather than labels because path values
// aren't valid Kubernetes label values.
const (
	AnnotationLocalFolder = "devcontainer.local_folder"
	AnnotationConfigFile  = "devcontainer.config_file"
)

// DefaultContainerName is the container name used when the request
// doesn't supply one.
const DefaultContainerName = "devcontainer"

// keepaliveCmd holds the pod's single container open the same way the
// docker host does (pkg/host/docker.keepaliveCmd). The trap on SIGTERM
// keeps `kubectl delete pod` shutting down cleanly inside the
// terminationGracePeriod, and the `sleep & wait $!` pattern lets the
// trap actually fire.
const keepaliveCmd = `echo Container started
trap "exit 0" 15
exec "$@"
while sleep 1 & wait $!; do :; done`

// ProjectRequest captures the inputs needed to render a devcontainer
// into Kubernetes objects. The caller is responsible for building the
// image (use pkg/builder) and resolving features (use pkg/features); on
// Kubernetes there's no equivalent of the docker host's host-side
// variable substitution loop, so any ${localEnv:VAR} / ${devcontainerId}
// references in the spec should already be resolved before Project is
// called (the substitute package can do this).
type ProjectRequest struct {
	// Spec is the parsed devcontainer.json. Required.
	Spec *spec.DevContainer

	// SpecPath is the host-side path the spec was loaded from. When
	// non-empty it's stored in the AnnotationConfigFile annotation so
	// downstream tooling can reverse-look-up the source file.
	SpecPath string

	// LocalWorkspace is the host-side workspace folder (absolute). When
	// non-empty it's stored in AnnotationLocalFolder and used as the
	// default HostPath source for the workspace volume.
	LocalWorkspace string

	// ContainerWorkspace is the path inside the container where the
	// workspace is mounted. Falls back to spec.WorkspaceFolder, then
	// "/workspaces/<basename>".
	ContainerWorkspace string

	// Image is the already-built container image reference.
	Image string

	// Features as resolved by pkg/features. Their containerEnv,
	// init/privileged/capAdd/securityOpt, and mounts merge into the
	// pod the same way the docker host merges them.
	Features []features.Resolved

	// Name is the metadata.name written onto the projected Pod. May be
	// empty — the caller can fill it in or use generateName.
	Name string

	// Namespace is metadata.namespace. May be empty.
	Namespace string

	// ContainerName overrides the container's name. Defaults to
	// DefaultContainerName.
	ContainerName string

	// Labels and Annotations are seeded onto metadata. The package adds
	// AnnotationLocalFolder and AnnotationConfigFile on top.
	Labels      map[string]string
	Annotations map[string]string

	// WorkspaceVolumeSource overrides the default workspace volume.
	// When nil, a HostPath sourced from LocalWorkspace is used — a fit
	// for single-node clusters (kind, minikube, docker-desktop) where
	// the host workspace is reachable from the kubelet. Set to a
	// PersistentVolumeClaim or other source for remote clusters.
	WorkspaceVolumeSource *corev1.VolumeSource
}

// Projection is the rendered Kubernetes object set. Today only Pod is
// populated; future fields (Service for forwardPorts, PVC for the
// workspace) will be additive.
type Projection struct {
	Pod *corev1.Pod
}

// Project translates a devcontainer spec into Kubernetes manifests.
//
// It rejects with an error any spec field or runArg that has no clean
// Kubernetes equivalent (e.g. --network, --gpus device IDs, named
// docker volumes), rather than silently dropping it: a missing
// --cap-add or --userns flag changes the container's security model.
// Embedders that want the request to succeed regardless can strip the
// offending fields before calling.
func Project(req ProjectRequest) (*Projection, error) {
	if req.Spec == nil {
		return nil, errors.New("kubernetes projection: ProjectRequest.Spec is required")
	}
	if req.Image == "" {
		return nil, errors.New("kubernetes projection: ProjectRequest.Image is required")
	}

	dc := req.Spec
	containerWorkspace := req.ContainerWorkspace
	if containerWorkspace == "" {
		containerWorkspace = dc.WorkspaceFolder
	}
	if containerWorkspace == "" && req.LocalWorkspace != "" {
		containerWorkspace = "/workspaces/" + basename(req.LocalWorkspace)
	}
	if containerWorkspace != "" {
		containerWorkspace = ensurePosixPath(containerWorkspace)
	}

	containerName := req.ContainerName
	if containerName == "" {
		containerName = DefaultContainerName
	}

	c := corev1.Container{
		Name:       containerName,
		Image:      req.Image,
		Command:    []string{"/bin/sh"},
		Args:       []string{"-c", keepaliveCmd, "-"},
		WorkingDir: containerWorkspace,
	}

	envSlice := envFromMap(stringMap(dc.ContainerEnv))
	for _, f := range req.Features {
		for k, v := range f.Metadata.ContainerEnv {
			envSlice = upsertEnv(envSlice, k, v)
		}
	}
	c.Env = envSlice

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        req.Name,
			Namespace:   req.Namespace,
			Labels:      cloneStringMap(req.Labels),
			Annotations: cloneStringMap(req.Annotations),
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers:    []corev1.Container{c},
		},
	}
	if req.LocalWorkspace != "" {
		setAnnotation(pod, AnnotationLocalFolder, req.LocalWorkspace)
	}
	if req.SpecPath != "" {
		setAnnotation(pod, AnnotationConfigFile, req.SpecPath)
	}

	if dc.ContainerUser != "" {
		if err := applyContainerUser(&pod.Spec.Containers[0], dc.ContainerUser); err != nil {
			return nil, err
		}
	}

	applyFeatureFlags(&pod.Spec.Containers[0], dc, req.Features)

	if err := applyMounts(pod, dc, req); err != nil {
		return nil, err
	}

	if err := applyPorts(&pod.Spec.Containers[0], dc.ForwardPorts, dc.AppPort); err != nil {
		return nil, err
	}

	if len(dc.RunArgs) > 0 {
		if err := applyRunArgs(dc.RunArgs, pod, &pod.Spec.Containers[0]); err != nil {
			return nil, err
		}
	}

	return &Projection{Pod: pod}, nil
}

// applyContainerUser parses the containerUser / -u value the same way
// docker does: "uid", "uid:gid", "name", "name:group". Numeric forms
// land on securityContext.RunAsUser/RunAsGroup; the literal name form
// is preserved on container's User field equivalents — except
// Kubernetes containers don't have a User field, so a pure-name form
// returns an error pointing the user at remoteUser/runAsUser instead.
func applyContainerUser(c *corev1.Container, user string) error {
	uid, gid, err := parseUserSpec(user)
	if err != nil {
		return fmt.Errorf("containerUser %q: %w", user, err)
	}
	if c.SecurityContext == nil {
		c.SecurityContext = &corev1.SecurityContext{}
	}
	c.SecurityContext.RunAsUser = &uid
	if gid != nil {
		c.SecurityContext.RunAsGroup = gid
	}
	return nil
}

// applyFeatureFlags merges the boolean security flags from the spec
// and from each feature's metadata onto the container/pod. Features
// can opt the container into init / privileged / extra capabilities
// the same way the docker host does.
func applyFeatureFlags(c *corev1.Container, dc *spec.DevContainer, resolved []features.Resolved) {
	privileged := dc.Privileged != nil && *dc.Privileged
	capAdd := append([]string(nil), dc.CapAdd...)
	secOpts := append([]string(nil), dc.SecurityOpt...)
	for _, f := range resolved {
		if f.Metadata.Privileged != nil && *f.Metadata.Privileged {
			privileged = true
		}
		capAdd = append(capAdd, f.Metadata.CapAdd...)
		secOpts = append(secOpts, f.Metadata.SecurityOpt...)
	}
	if privileged {
		ensureSecCtx(c).Privileged = boolPtr(true)
	}
	if len(capAdd) > 0 {
		sc := ensureSecCtx(c)
		if sc.Capabilities == nil {
			sc.Capabilities = &corev1.Capabilities{}
		}
		for _, cap := range capAdd {
			sc.Capabilities.Add = append(sc.Capabilities.Add, corev1.Capability(cap))
		}
	}
	for _, opt := range secOpts {
		applySecurityOpt(c, opt)
	}
}

// applySecurityOpt translates the docker --security-opt value forms
// that have a Kubernetes equivalent. Unknown forms are preserved on a
// best-effort basis: the imperative host can re-validate at apply
// time.
func applySecurityOpt(c *corev1.Container, opt string) {
	switch {
	case opt == "no-new-privileges:true" || opt == "no-new-privileges":
		ensureSecCtx(c).AllowPrivilegeEscalation = boolPtr(false)
	case strings.HasPrefix(opt, "seccomp="):
		v := strings.TrimPrefix(opt, "seccomp=")
		switch v {
		case "unconfined":
			ensureSecCtx(c).SeccompProfile = &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeUnconfined}
		case "default", "runtime/default":
			ensureSecCtx(c).SeccompProfile = &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault}
		default:
			ensureSecCtx(c).SeccompProfile = &corev1.SeccompProfile{
				Type:             corev1.SeccompProfileTypeLocalhost,
				LocalhostProfile: stringPtr(v),
			}
		}
	case strings.HasPrefix(opt, "label="):
		applySELinux(c, strings.TrimPrefix(opt, "label="))
	}
}

// applySELinux maps "user:foo,role:bar,type:baz,level:s0:c123,c456"
// onto SELinuxOptions. "disable" / "type:KEY=VAL" forms not covered
// here are dropped — the imperative host can warn.
func applySELinux(c *corev1.Container, val string) {
	if val == "disable" {
		// No clean PodSpec equivalent. Leave securityContext alone; an
		// operator can patch SELinuxOptions: nil if it really needs to.
		return
	}
	sc := ensureSecCtx(c)
	if sc.SELinuxOptions == nil {
		sc.SELinuxOptions = &corev1.SELinuxOptions{}
	}
	for _, kv := range strings.Split(val, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(kv), ":")
		if !ok {
			continue
		}
		switch strings.ToLower(k) {
		case "user":
			sc.SELinuxOptions.User = v
		case "role":
			sc.SELinuxOptions.Role = v
		case "type":
			sc.SELinuxOptions.Type = v
		case "level":
			sc.SELinuxOptions.Level = v
		}
	}
}

func ensureSecCtx(c *corev1.Container) *corev1.SecurityContext {
	if c.SecurityContext == nil {
		c.SecurityContext = &corev1.SecurityContext{}
	}
	return c.SecurityContext
}

func boolPtr(b bool) *bool       { return &b }
func stringPtr(s string) *string { return &s }
func int64Ptr(i int64) *int64    { return &i }

func setAnnotation(p *corev1.Pod, k, v string) {
	if p.Annotations == nil {
		p.Annotations = map[string]string{}
	}
	p.Annotations[k] = v
}

// envFromMap converts a Go string map into the API's []EnvVar form,
// dropping empty keys.
func envFromMap(m map[string]string) []corev1.EnvVar {
	out := make([]corev1.EnvVar, 0, len(m))
	for k, v := range m {
		if k == "" {
			continue
		}
		out = append(out, corev1.EnvVar{Name: k, Value: v})
	}
	return out
}

// upsertEnv replaces an existing entry by name, or appends.
func upsertEnv(env []corev1.EnvVar, k, v string) []corev1.EnvVar {
	for i := range env {
		if env[i].Name == k {
			env[i].Value = v
			return env
		}
	}
	return append(env, corev1.EnvVar{Name: k, Value: v})
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

// stringMap unwraps a map[string]any whose values are strings,
// matching pkg/host/docker's stringMap helper. Values that aren't
// stringly are skipped — Kubernetes envvars are string-only.
func stringMap(in map[string]any) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		switch t := v.(type) {
		case string:
			out[k] = t
		case nil:
			out[k] = ""
		}
	}
	return out
}

// parseUserSpec accepts "uid", "uid:gid", or rejects "name" / "name:group".
// Kubernetes containers have no User string equivalent — only numeric
// IDs land cleanly on RunAsUser / RunAsGroup.
func parseUserSpec(s string) (int64, *int64, error) {
	user, group, hasGroup := strings.Cut(s, ":")
	uid, err := parseInt64(user)
	if err != nil {
		return 0, nil, fmt.Errorf("containerUser must be numeric (Kubernetes runAsUser is an int)")
	}
	if !hasGroup {
		return uid, nil, nil
	}
	gid, err := parseInt64(group)
	if err != nil {
		return 0, nil, fmt.Errorf("group must be numeric (Kubernetes runAsGroup is an int)")
	}
	return uid, int64Ptr(gid), nil
}

func parseInt64(s string) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("empty integer")
	}
	var n int64
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("not a positive integer: %q", s)
		}
		n = n*10 + int64(c-'0')
	}
	return n, nil
}

func basename(p string) string {
	p = strings.TrimRight(p, "/")
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

// ensurePosixPath canonicalizes a container-side path. Container paths
// are always posix; the caller may have passed a windows-style host
// path by mistake.
func ensurePosixPath(p string) string {
	return path.Clean("/" + strings.TrimLeft(strings.ReplaceAll(p, "\\", "/"), "/"))
}
