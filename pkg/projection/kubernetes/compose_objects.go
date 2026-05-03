package kubernetes

import (
	"fmt"
	"os"
	"sort"

	composetypes "github.com/compose-spec/compose-go/v2/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// composeObjectBinding ties a compose config/secret key (e.g. "api-key")
// to the Kubernetes resource the Pod's volumeMount references.
//
//   - VolumeName is the per-Pod volume name; same compose key shared
//     across services produces one Pod volume mounted into each.
//   - ResourceName is the metadata.name of the underlying ConfigMap or
//     Secret. Equals the source compose key by default; for
//     `external: true` it's whatever compose's `name:` field said.
//   - DataKey is the key inside the resource's data map under which
//     the file content is stored (defaults to the compose key).
type composeObjectBinding struct {
	VolumeName   string
	ResourceName string
	DataKey      string
	IsSecret     bool
}

// projectComposeConfigs walks the project's top-level configs and
// emits one ConfigMap per non-external entry. Returns the resources
// to create plus a binding map keyed by the compose volume key.
func projectComposeConfigs(
	proj *composetypes.Project, req ComposeProjectRequest,
) ([]*corev1.ConfigMap, map[string]composeObjectBinding, error) {
	bindings := map[string]composeObjectBinding{}
	if len(proj.Configs) == 0 {
		return nil, bindings, nil
	}
	keys := make([]string, 0, len(proj.Configs))
	for k := range proj.Configs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []*corev1.ConfigMap
	for _, key := range keys {
		entry := composetypes.FileObjectConfig(proj.Configs[key])
		volumeName := sanitizeVolumeName("config-" + key)
		dataKey := sanitizeDataKey(key)
		if bool(entry.External) {
			name := entry.Name
			if name == "" {
				name = key
			}
			bindings[key] = composeObjectBinding{
				VolumeName: volumeName, ResourceName: name, DataKey: dataKey,
			}
			continue
		}
		content, err := readFileObjectContent("config", key, entry, req.LocalEnv)
		if err != nil {
			return nil, nil, err
		}
		resourceName := sanitizeResourceName(req.Name, "config", key, entry.Name)
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      resourceName,
				Namespace: req.Namespace,
				Labels:    cloneStringMap(req.Labels),
			},
			BinaryData: map[string][]byte{
				dataKey: content,
			},
		}
		out = append(out, cm)
		bindings[key] = composeObjectBinding{
			VolumeName: volumeName, ResourceName: resourceName, DataKey: dataKey,
		}
	}
	return out, bindings, nil
}

// projectComposeSecrets walks the project's top-level secrets and
// emits one Secret per non-external entry. Mirror of
// projectComposeConfigs.
func projectComposeSecrets(
	proj *composetypes.Project, req ComposeProjectRequest,
) ([]*corev1.Secret, map[string]composeObjectBinding, error) {
	bindings := map[string]composeObjectBinding{}
	if len(proj.Secrets) == 0 {
		return nil, bindings, nil
	}
	keys := make([]string, 0, len(proj.Secrets))
	for k := range proj.Secrets {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []*corev1.Secret
	for _, key := range keys {
		entry := composetypes.FileObjectConfig(proj.Secrets[key])
		volumeName := sanitizeVolumeName("secret-" + key)
		dataKey := sanitizeDataKey(key)
		if bool(entry.External) {
			name := entry.Name
			if name == "" {
				name = key
			}
			bindings[key] = composeObjectBinding{
				VolumeName: volumeName, ResourceName: name, DataKey: dataKey, IsSecret: true,
			}
			continue
		}
		content, err := readFileObjectContent("secret", key, entry, req.LocalEnv)
		if err != nil {
			return nil, nil, err
		}
		resourceName := sanitizeResourceName(req.Name, "secret", key, entry.Name)
		s := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      resourceName,
				Namespace: req.Namespace,
				Labels:    cloneStringMap(req.Labels),
			},
			Type: corev1.SecretTypeOpaque,
			Data: map[string][]byte{
				dataKey: content,
			},
		}
		out = append(out, s)
		bindings[key] = composeObjectBinding{
			VolumeName: volumeName, ResourceName: resourceName, DataKey: dataKey, IsSecret: true,
		}
	}
	return out, bindings, nil
}

// readFileObjectContent resolves a compose config/secret entry to the
// raw bytes that go in the projected ConfigMap/Secret. Mirrors the
// dockercompose host's materializer, but stays in-process since
// Kubernetes resources can carry the value directly.
func readFileObjectContent(kind, key string, obj composetypes.FileObjectConfig, localEnv map[string]string) ([]byte, error) {
	if obj.Driver != "" {
		return nil, fmt.Errorf("%s %q sets driver %q; only file/environment/content sources are supported in the projection", kind, key, obj.Driver)
	}
	if obj.File != "" {
		body, err := os.ReadFile(obj.File)
		if err != nil {
			return nil, fmt.Errorf("%s %q file %s: %w", kind, key, obj.File, err)
		}
		return body, nil
	}
	if obj.Environment != "" {
		val, ok := localEnv[obj.Environment]
		if !ok {
			return nil, fmt.Errorf("%s %q references env %q which is not in LocalEnv", kind, key, obj.Environment)
		}
		return []byte(val), nil
	}
	if obj.Content != "" {
		return []byte(obj.Content), nil
	}
	return nil, fmt.Errorf("%s %q has no source (file/environment/content)", kind, key)
}

// translateComposeServiceObjects produces volumes + volumeMounts for
// a single service's `configs:` and `secrets:` references. Targets
// default to /<source> for configs and /run/secrets/<source> for
// secrets, matching the dockercompose host.
func translateComposeServiceObjects(
	svc composetypes.ServiceConfig,
	configs, secrets map[string]composeObjectBinding,
) ([]corev1.Volume, []corev1.VolumeMount, error) {
	if len(svc.Configs) == 0 && len(svc.Secrets) == 0 {
		return nil, nil, nil
	}
	var vols []corev1.Volume
	var mounts []corev1.VolumeMount
	for _, ref := range svc.Configs {
		b, ok := configs[ref.Source]
		if !ok {
			return nil, nil, fmt.Errorf("service %q references config %q which is not declared at top level", svc.Name, ref.Source)
		}
		vol := corev1.Volume{
			Name: b.VolumeName,
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: b.ResourceName},
				},
			},
		}
		mount := configMountFor(ref, b)
		vols = append(vols, vol)
		mounts = append(mounts, mount)
	}
	for _, ref := range svc.Secrets {
		b, ok := secrets[ref.Source]
		if !ok {
			return nil, nil, fmt.Errorf("service %q references secret %q which is not declared at top level", svc.Name, ref.Source)
		}
		vol := corev1.Volume{
			Name: b.VolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: b.ResourceName,
				},
			},
		}
		mount := secretMountFor(ref, b)
		vols = append(vols, vol)
		mounts = append(mounts, mount)
	}
	return vols, mounts, nil
}

// configMountFor returns the volumeMount for a service config ref. The
// `subPath: <data-key>` projects only the file the compose ref names
// into the target path (avoiding the entire ConfigMap's data getting
// dumped under the target).
func configMountFor(ref composetypes.ServiceConfigObjConfig, b composeObjectBinding) corev1.VolumeMount {
	target := ref.Target
	if target == "" {
		target = "/" + ref.Source
	}
	return corev1.VolumeMount{
		Name:      b.VolumeName,
		MountPath: target,
		SubPath:   b.DataKey,
		ReadOnly:  true,
	}
}

func secretMountFor(ref composetypes.ServiceSecretConfig, b composeObjectBinding) corev1.VolumeMount {
	target := ref.Target
	if target == "" {
		target = "/run/secrets/" + ref.Source
	}
	return corev1.VolumeMount{
		Name:      b.VolumeName,
		MountPath: target,
		SubPath:   b.DataKey,
		ReadOnly:  true,
	}
}

// sanitizeResourceName builds a DNS-1123 safe name for a generated
// ConfigMap/Secret. Honors the user's `name:` override when set.
func sanitizeResourceName(podName, kind, key, override string) string {
	if override != "" {
		return sanitizeVolumeName(override)
	}
	if podName == "" {
		return sanitizeVolumeName(kind + "-" + key)
	}
	return sanitizeVolumeName(podName + "-" + kind + "-" + key)
}

// sanitizeDataKey returns a key suitable for use inside ConfigMap.Data
// / Secret.Data. The Kubernetes API restricts these to alphanumeric +
// '.' '-' '_' (matching the compose source field's typical shape).
func sanitizeDataKey(s string) string {
	if s == "" {
		return "_"
	}
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z',
			c >= 'A' && c <= 'Z',
			c >= '0' && c <= '9',
			c == '.', c == '-', c == '_':
			out = append(out, c)
		default:
			out = append(out, '_')
		}
	}
	return string(out)
}
