package kubernetes

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// workspaceVolumeName is the volume name for the workspace mount.
// Kept stable so an embedder that wants to PATCH the projected pod
// (e.g. swap HostPath for a PVC) can find the volume by name.
const workspaceVolumeName = "workspace"

// applyMounts wires the workspace mount and any spec.Mounts entries
// onto the projected pod. Bind mounts become HostPath volumes, named
// volume mounts become PVCs *only* when the embedder has supplied a
// matching corev1.Volume in WorkspaceVolumeSource — otherwise they're
// rejected, since docker's named volumes don't have a Kubernetes
// equivalent that doesn't already exist as a PVC.
func applyMounts(pod *corev1.Pod, dc *spec.DevContainer, req ProjectRequest) error {
	if c := workspaceVolume(dc, req); c != nil {
		pod.Spec.Volumes = append(pod.Spec.Volumes, *c)
		pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, corev1.VolumeMount{
			Name:      c.Name,
			MountPath: workspaceMountPath(dc, req),
		})
	}

	for _, raw := range dc.Mounts {
		v, vm, err := parseMount(raw)
		if err != nil {
			return err
		}
		if v == nil || vm == nil {
			continue
		}
		// De-duplicate volume names so multiple mounts to /run can
		// land on the same volume.
		if i := volumeIndex(pod.Spec.Volumes, v.Name); i < 0 {
			pod.Spec.Volumes = append(pod.Spec.Volumes, *v)
		}
		pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, *vm)
	}

	for _, f := range req.Features {
		for _, raw := range f.Metadata.Mounts {
			v, vm, err := parseMount(raw)
			if err != nil {
				return fmt.Errorf("feature %q: %w", f.Source, err)
			}
			if v == nil || vm == nil {
				continue
			}
			if i := volumeIndex(pod.Spec.Volumes, v.Name); i < 0 {
				pod.Spec.Volumes = append(pod.Spec.Volumes, *v)
			}
			pod.Spec.Containers[0].VolumeMounts = append(pod.Spec.Containers[0].VolumeMounts, *vm)
		}
	}
	return nil
}

func workspaceMountPath(dc *spec.DevContainer, req ProjectRequest) string {
	if req.ContainerWorkspace != "" {
		return ensurePosixPath(req.ContainerWorkspace)
	}
	if dc.WorkspaceFolder != "" {
		return ensurePosixPath(dc.WorkspaceFolder)
	}
	if req.LocalWorkspace != "" {
		return "/workspaces/" + basename(req.LocalWorkspace)
	}
	return ""
}

// workspaceVolume returns the workspace volume to attach. Falls back
// to a HostPath sourced from LocalWorkspace, matching the docker
// host's default behavior. Returns nil when no workspace path was
// configured and the embedder didn't supply an override (a valid
// case for image-only specs that don't expect any bind).
func workspaceVolume(dc *spec.DevContainer, req ProjectRequest) *corev1.Volume {
	if dc.WorkspaceMount != "" {
		// Spec-level override: a docker-style "type=bind,source=/x,target=/y"
		// mount string. Translate it into a Volume; the mount path comes
		// from the target so override workspaceMountPath via the parsed
		// VolumeMount.
		if v, _, err := parseMountString(dc.WorkspaceMount, workspaceVolumeName); err == nil && v != nil {
			return v
		}
	}
	if req.WorkspaceVolumeSource != nil {
		return &corev1.Volume{Name: workspaceVolumeName, VolumeSource: *req.WorkspaceVolumeSource}
	}
	if req.LocalWorkspace == "" {
		return nil
	}
	return &corev1.Volume{
		Name: workspaceVolumeName,
		VolumeSource: corev1.VolumeSource{
			HostPath: &corev1.HostPathVolumeSource{Path: req.LocalWorkspace},
		},
	}
}

// parseMount accepts the same string and object forms pkg/host/docker
// does (parseMountString / parseMountObject) and projects each into a
// (Volume, VolumeMount) pair. The volume name is derived from the
// source for bind mounts (path-safe sanitized) and the volume label
// otherwise.
func parseMount(raw any) (*corev1.Volume, *corev1.VolumeMount, error) {
	switch v := raw.(type) {
	case string:
		return parseMountString(v, "")
	case map[string]any:
		return parseMountObject(v, "")
	}
	return nil, nil, fmt.Errorf("mount: unsupported entry %T", raw)
}

// mountFields is the intermediate set parseMountString and
// parseMountObject decode into before projecting onto k8s types.
type mountFields struct {
	typ      string
	source   string
	target   string
	readOnly bool
	tmpfsOpt string
}

func parseMountString(s, nameOverride string) (*corev1.Volume, *corev1.VolumeMount, error) {
	mf := mountFields{}
	for _, kv := range strings.Split(s, ",") {
		kv = strings.TrimSpace(kv)
		k, v := kv, ""
		if eq := strings.IndexByte(kv, '='); eq >= 0 {
			k = strings.TrimSpace(kv[:eq])
			v = strings.TrimSpace(kv[eq+1:])
		}
		switch k {
		case "type":
			mf.typ = v
		case "source", "src":
			mf.source = v
		case "target", "dst", "destination":
			mf.target = v
		case "readonly", "ro":
			mf.readOnly = v == "" || v == "true" || v == "1"
		case "tmpfs-size":
			mf.tmpfsOpt = v
		}
	}
	return projectMount(mf, nameOverride)
}

func parseMountObject(o map[string]any, nameOverride string) (*corev1.Volume, *corev1.VolumeMount, error) {
	if err := (&spec.DevContainer{Mounts: []any{o}}).ValidateMounts(); err != nil {
		return nil, nil, err
	}
	mf := mountFields{}
	for k, raw := range o {
		switch k {
		case "type":
			if s, ok := raw.(string); ok {
				mf.typ = s
			}
		case "source":
			if s, ok := raw.(string); ok {
				mf.source = s
			}
		case "target":
			if s, ok := raw.(string); ok {
				mf.target = s
			}
		}
	}
	return projectMount(mf, nameOverride)
}

func projectMount(mf mountFields, nameOverride string) (*corev1.Volume, *corev1.VolumeMount, error) {
	if mf.typ == "" {
		mf.typ = "bind"
	}
	if mf.target == "" {
		return nil, nil, fmt.Errorf("mount: missing target")
	}

	switch mf.typ {
	case "bind":
		if mf.source == "" {
			return nil, nil, fmt.Errorf("mount: bind requires source")
		}
		if !strings.HasPrefix(mf.source, "/") {
			return nil, nil, fmt.Errorf("mount: bind source %q must be an absolute host path on Kubernetes", mf.source)
		}
		name := nameOverride
		if name == "" {
			name = sanitizeVolumeName(mf.source)
		}
		return &corev1.Volume{
				Name:         name,
				VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: mf.source}},
			},
			&corev1.VolumeMount{Name: name, MountPath: mf.target, ReadOnly: mf.readOnly},
			nil
	case "tmpfs":
		name := nameOverride
		if name == "" {
			name = sanitizeVolumeName("tmpfs-" + mf.target)
		}
		src := corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory}
		return &corev1.Volume{Name: name, VolumeSource: corev1.VolumeSource{EmptyDir: &src}},
			&corev1.VolumeMount{Name: name, MountPath: mf.target, ReadOnly: mf.readOnly},
			nil
	case "volume":
		// Docker named volumes don't translate cleanly: a Kubernetes
		// PVC has to exist (or be provisioned) before the pod can mount
		// it, and the name on the docker side is just a label. Reject
		// rather than guess at a PVC name.
		return nil, nil, fmt.Errorf("mount: type=volume %q is not supported on Kubernetes (use a pre-provisioned PVC and supply via WorkspaceVolumeSource or pod.Spec.Volumes)", mf.source)
	default:
		return nil, nil, fmt.Errorf("mount: unsupported type %q", mf.typ)
	}
}

// sanitizeVolumeName turns an absolute path (or arbitrary identifier)
// into a Kubernetes DNS-1123 label-safe volume name. Lowercase,
// replace non-[a-z0-9-] with '-', collapse repeats, trim, fall back
// to "vol" when empty.
func sanitizeVolumeName(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	b.Grow(len(s))
	prevDash := false
	for _, r := range s {
		ok := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
		if !ok {
			r = '-'
		}
		if r == '-' {
			if prevDash {
				continue
			}
			prevDash = true
		} else {
			prevDash = false
		}
		b.WriteRune(r)
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "vol"
	}
	if len(out) > 63 {
		out = strings.TrimRight(out[:63], "-")
	}
	return out
}

func volumeIndex(vs []corev1.Volume, name string) int {
	for i, v := range vs {
		if v.Name == name {
			return i
		}
	}
	return -1
}
