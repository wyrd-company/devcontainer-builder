package kubernetes

import (
	"errors"
	"fmt"
	"sort"
	"strconv"

	composetypes "github.com/compose-spec/compose-go/v2/types"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// AnnotationComposeProject and AnnotationComposeService record which
// compose project/primary-service produced the Pod. Useful for an
// operator that wants to correlate a Pod back to the source spec
// without re-parsing.
const (
	AnnotationComposeProject = "devcontainer.compose.project"
	AnnotationComposeService = "devcontainer.compose.service"
)

// DefaultComposeVolumeStorage is the storage request applied to PVCs
// generated for compose named volumes when the request doesn't set
// one. Small enough that a 1Gi-default storage class is happy; large
// enough that a basic devcontainer workflow has somewhere to write.
var DefaultComposeVolumeStorage = resource.MustParse("1Gi")

// ComposeProjectRequest captures the inputs needed to render a
// compose-mode devcontainer into a single multi-container Pod plus
// any backing PVCs.
//
// The shape mirrors ProjectRequest where it can — Spec, Image
// override, Features, metadata, workspace — and adds compose-specific
// inputs: the parsed *types.Project (use
// pkg/host/dockercompose.LoadProject to produce it) and the storage
// knobs for named-volume PVCs.
type ComposeProjectRequest struct {
	// Spec is the parsed devcontainer.json (must be in compose mode).
	Spec *spec.DevContainer

	// SpecPath is the host path the spec was loaded from. Stored as
	// AnnotationConfigFile.
	SpecPath string

	// LocalWorkspace is the host workspace path. Stored as
	// AnnotationLocalFolder and used as the default workspace
	// HostPath source.
	LocalWorkspace string

	// ContainerWorkspace is where the workspace mounts inside the
	// primary container. Falls back to spec.WorkspaceFolder, then
	// "/workspaces/<basename>".
	ContainerWorkspace string

	// Project is the parsed compose project. Required.
	Project *composetypes.Project

	// PrimaryImage, when non-empty, replaces the primary service's
	// compose `image:`. Used to point the Pod at a feature-derived
	// image produced by pkg/host/dockercompose.BuildPrimaryImage.
	PrimaryImage string

	// Features as resolved by pkg/features. Their containerEnv,
	// init/privileged/capAdd/securityOpt, and mounts merge into the
	// primary container only — sidecar services don't see them.
	Features []features.Resolved

	// Name and Namespace seed metadata.
	Name      string
	Namespace string

	// Labels and Annotations seed metadata.
	Labels      map[string]string
	Annotations map[string]string

	// WorkspaceVolumeSource overrides the workspace volume source.
	// When nil, falls back to a HostPath sourced from LocalWorkspace
	// (matching the single-container projection's default).
	WorkspaceVolumeSource *corev1.VolumeSource

	// VolumeStorageClass is set on PVCs generated for compose named
	// volumes. Empty means the cluster's default storage class.
	VolumeStorageClass string

	// VolumeStorageRequest is the per-PVC storage request. Defaults
	// to DefaultComposeVolumeStorage when zero.
	VolumeStorageRequest resource.Quantity

	// Profiles activates compose service profiles. Services whose
	// `profiles:` list is non-empty are excluded from the projected
	// Pod unless one of their entries matches Profiles (or Profiles
	// contains "*"). The primary service stays enabled regardless.
	Profiles []string

	// LocalEnv is the lookup table for compose `secrets:` /
	// `configs:` declared with `environment:` source. Materializing
	// happens at projection time, so the env variable's value is
	// baked into the resulting Secret/ConfigMap.
	LocalEnv map[string]string
}

// ComposeProjection bundles the rendered Pod with any PVCs, Secrets,
// and ConfigMaps that need to exist for the Pod's volume references
// to resolve. The caller applies them in order (PVCs/ConfigMaps/
// Secrets first, then the Pod).
type ComposeProjection struct {
	Pod                    *corev1.Pod
	PersistentVolumeClaims []*corev1.PersistentVolumeClaim
	ConfigMaps             []*corev1.ConfigMap
	Secrets                []*corev1.Secret
}

// ProjectCompose translates a compose-mode devcontainer into a
// single multi-container Pod (sidecar model) plus PVCs for any
// compose named volumes.
//
// Scope cuts in this iteration (mirroring pkg/host/dockercompose so
// behavior stays consistent across hosts):
//
//   - depends_on: containers in a Pod can't be ordered through the
//     compose model; compose `depends_on` is ignored.
//   - watch / develop: file-watch loops aren't a static manifest
//     concept and aren't honored by the projection. The imperative
//     pkg/host/dockercompose host has Watch() for that.
//   - Compose-side `build:` for any service (including the primary):
//     not built. Each service must reference a pullable `image:`.
//   - tmpfs/image/cluster volume types: tmpfs translates to
//     EmptyDir(memory); image and cluster types return an error.
//
// Profiles, configs, secrets, and service-name DNS are honored:
//
//   - Profiles narrow the projected Pod to services whose `profiles:`
//     match `req.Profiles` (with "*" as a wildcard). The primary
//     stays enabled regardless.
//   - Configs become ConfigMaps (or external references) and are
//     mounted at `/<source>` by default.
//   - Secrets become Secrets (or external references) and are
//     mounted at `/run/secrets/<source>` by default.
//   - Service-name DNS works inside the Pod via pod.spec.hostAliases:
//     each compose service name (and any compose network alias) is
//     mapped to 127.0.0.1, where its container is actually listening
//     since all containers in a Pod share the network namespace. So
//     `db:5432` resolves the same way it would on a compose
//     user-defined network.
func ProjectCompose(req ComposeProjectRequest) (*ComposeProjection, error) {
	if req.Spec == nil {
		return nil, errors.New("kubernetes projection: ComposeProjectRequest.Spec is required")
	}
	if err := req.Spec.ValidateMounts(); err != nil {
		return nil, err
	}
	if req.Spec.Mode() != spec.ModeCompose {
		return nil, errors.New("kubernetes projection: spec is not in compose mode")
	}
	if req.Project == nil {
		return nil, errors.New("kubernetes projection: ComposeProjectRequest.Project is required")
	}
	proj, err := applyComposeProfiles(req.Project, req.Profiles, req.Spec.Service)
	if err != nil {
		return nil, fmt.Errorf("kubernetes projection: %w", err)
	}
	primary, err := proj.GetService(req.Spec.Service)
	if err != nil {
		return nil, fmt.Errorf("kubernetes projection: primary service %q not found in compose project", req.Spec.Service)
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

	storage := req.VolumeStorageRequest
	if storage.IsZero() {
		storage = DefaultComposeVolumeStorage
	}

	// Resolve the volume source/PVC shape for every named volume in
	// the project. External named volumes assume a pre-existing PVC
	// with the literal compose name and don't generate a PVC of our
	// own. The result map is keyed by the compose volume key (e.g.
	// "data") and carries the Pod-side volume name + an optional PVC.
	volMap, pvcs, err := projectComposeNamedVolumes(proj, req.Name, req.Namespace, req.VolumeStorageClass, storage, req.Labels)
	if err != nil {
		return nil, err
	}

	// Project compose configs/secrets into ConfigMaps/Secrets. Each
	// returns a resource (or nil for `external: true`) and a binding
	// the per-service translator uses to wire up Pod volumes.
	configMaps, configBindings, err := projectComposeConfigs(proj, req)
	if err != nil {
		return nil, err
	}
	secrets, secretBindings, err := projectComposeSecrets(proj, req)
	if err != nil {
		return nil, err
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        req.Name,
			Namespace:   req.Namespace,
			Labels:      cloneStringMap(req.Labels),
			Annotations: cloneStringMap(req.Annotations),
		},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
		},
	}
	setAnnotation(pod, AnnotationComposeProject, req.Project.Name)
	setAnnotation(pod, AnnotationComposeService, dc.Service)
	if req.LocalWorkspace != "" {
		setAnnotation(pod, AnnotationLocalFolder, req.LocalWorkspace)
	}
	if req.SpecPath != "" {
		setAnnotation(pod, AnnotationConfigFile, req.SpecPath)
	}

	// Pod-level volume set: every volume the primary OR a sidecar
	// references, deduplicated by compose volume key. Bind sources
	// share a Pod-side name across services so two services binding
	// the same host path share a single Volume entry.
	addedVolumes := map[string]struct{}{}
	addVolume := func(v corev1.Volume) {
		if _, ok := addedVolumes[v.Name]; ok {
			return
		}
		addedVolumes[v.Name] = struct{}{}
		pod.Spec.Volumes = append(pod.Spec.Volumes, v)
	}

	// Primary container.
	primaryImage := req.PrimaryImage
	if primaryImage == "" {
		primaryImage = primary.Image
	}
	if primaryImage == "" {
		return nil, fmt.Errorf("kubernetes projection: primary service %q has no image (compose-side build for primary is out of scope)", dc.Service)
	}

	primaryC, primaryVols, err := buildPrimaryContainer(dc, &primary, primaryImage, containerWorkspace, req, volMap, configBindings, secretBindings)
	if err != nil {
		return nil, err
	}
	for _, v := range primaryVols {
		addVolume(v)
	}
	pod.Spec.Containers = append(pod.Spec.Containers, primaryC)

	// Sidecars: every other service in the project, in name order
	// for stable output.
	sideNames := make([]string, 0, len(proj.Services))
	for _, name := range proj.ServiceNames() {
		if name == dc.Service {
			continue
		}
		sideNames = append(sideNames, name)
	}
	sort.Strings(sideNames)
	for _, name := range sideNames {
		svc, _ := proj.GetService(name)
		c, vols, err := buildSidecarContainer(svc, volMap, configBindings, secretBindings)
		if err != nil {
			return nil, fmt.Errorf("service %q: %w", name, err)
		}
		for _, v := range vols {
			addVolume(v)
		}
		pod.Spec.Containers = append(pod.Spec.Containers, c)
	}

	// Compose service-name DNS in a single Pod is faked through
	// /etc/hosts: every service name (and its compose network
	// aliases) resolves to 127.0.0.1, where the corresponding
	// container is actually listening because all containers in a
	// Pod share the network namespace. Without this, a primary that
	// connects to "db:5432" would fail — Pod containers don't get
	// the compose embedded-DNS that user-defined docker networks
	// provide.
	if aliases := composeHostnames(proj); len(aliases) > 0 {
		pod.Spec.HostAliases = append(pod.Spec.HostAliases, corev1.HostAlias{
			IP:        "127.0.0.1",
			Hostnames: aliases,
		})
	}

	return &ComposeProjection{
		Pod:                    pod,
		PersistentVolumeClaims: pvcs,
		ConfigMaps:             configMaps,
		Secrets:                secrets,
	}, nil
}

// composeHostnames returns the deduplicated list of names that the
// containers in the projected Pod should reach via 127.0.0.1: every
// active service's name plus any compose network aliases declared on
// it. Sorted for stable manifest output.
func composeHostnames(proj *composetypes.Project) []string {
	if proj == nil || len(proj.Services) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	for _, name := range proj.ServiceNames() {
		if _, dup := seen[name]; !dup {
			seen[name] = struct{}{}
		}
		svc, err := proj.GetService(name)
		if err != nil {
			continue
		}
		for _, ep := range svc.Networks {
			if ep == nil {
				continue
			}
			for _, a := range ep.Aliases {
				if a == "" {
					continue
				}
				seen[a] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// applyComposeProfiles narrows the project to services enabled by
// req.Profiles, always keeping the primary. Returns the original
// project unchanged when no profiles are supplied.
func applyComposeProfiles(proj *composetypes.Project, profiles []string, primary string) (*composetypes.Project, error) {
	if proj == nil {
		return nil, errors.New("project is nil")
	}
	out := proj
	if len(profiles) > 0 {
		f, err := proj.WithProfiles(profiles)
		if err != nil {
			return nil, fmt.Errorf("apply profiles %v: %w", profiles, err)
		}
		out = f
	}
	if primary != "" {
		f, err := out.WithServicesEnabled(primary)
		if err != nil {
			return nil, fmt.Errorf("ensure primary service %q: %w", primary, err)
		}
		out = f
	}
	return out, nil
}

// composeVolumeBinding resolves how a named compose volume is exposed
// to the Pod. Either a PVC (default) or, for `external: true`, a
// reference to a pre-existing PVC that the cluster operator owns.
type composeVolumeBinding struct {
	// Name is the Pod-side volume name (used in volumeMounts).
	Name string
	// PVCName is the PersistentVolumeClaim name to reference. For
	// generated PVCs that's the same string the package writes onto
	// the PVC's metadata.name; for `external: true` it's whatever
	// compose's `name:` field said (and the operator must ensure the
	// PVC exists).
	PVCName string
}

// projectComposeNamedVolumes generates one PVC per non-external
// compose named volume and returns the mapping the spawn step needs.
func projectComposeNamedVolumes(
	proj *composetypes.Project, podName, namespace, storageClass string,
	storage resource.Quantity, labels map[string]string,
) (map[string]composeVolumeBinding, []*corev1.PersistentVolumeClaim, error) {
	out := map[string]composeVolumeBinding{}
	var pvcs []*corev1.PersistentVolumeClaim
	keys := make([]string, 0, len(proj.Volumes))
	for k := range proj.Volumes {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		v := proj.Volumes[key]
		volumeName := sanitizeVolumeName("vol-" + key)
		if bool(v.External) {
			pvcName := v.Name
			if pvcName == "" {
				pvcName = key
			}
			out[key] = composeVolumeBinding{Name: volumeName, PVCName: pvcName}
			continue
		}
		pvcName := composePVCName(podName, key)
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:        pvcName,
				Namespace:   namespace,
				Labels:      cloneStringMap(labels),
				Annotations: nil,
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: storage,
					},
				},
			},
		}
		if storageClass != "" {
			pvc.Spec.StorageClassName = stringPtr(storageClass)
		}
		pvcs = append(pvcs, pvc)
		out[key] = composeVolumeBinding{Name: volumeName, PVCName: pvcName}
	}
	return out, pvcs, nil
}

// composePVCName produces a stable, DNS-1123-safe PVC name. Falls
// back to "<podName>-<volumeKey>" when podName is set, otherwise just
// the volume key.
func composePVCName(podName, key string) string {
	if podName == "" {
		return sanitizeVolumeName(key)
	}
	return sanitizeVolumeName(podName + "-" + key)
}

// buildPrimaryContainer assembles the primary service's container —
// which carries the lifecycle keepalive, workspace mount, the
// devcontainer.json env, ports, runArgs, and feature mounts.
func buildPrimaryContainer(
	dc *spec.DevContainer, svc *composetypes.ServiceConfig, image, containerWorkspace string,
	req ComposeProjectRequest, volMap map[string]composeVolumeBinding,
	configBindings, secretBindings map[string]composeObjectBinding,
) (corev1.Container, []corev1.Volume, error) {
	c := corev1.Container{
		Name:       dc.Service,
		Image:      image,
		Command:    []string{"/bin/sh"},
		Args:       []string{"-c", keepaliveCmd, "-"},
		WorkingDir: containerWorkspace,
		TTY:        true,
		Stdin:      true,
	}

	envSlice := envFromMap(stringMap(dc.ContainerEnv))
	for _, f := range req.Features {
		for k, v := range f.Metadata.ContainerEnv {
			envSlice = upsertEnv(envSlice, k, v)
		}
	}
	for _, kv := range sortedComposeEnv(svc.Environment) {
		envSlice = upsertEnv(envSlice, kv.k, kv.v)
	}
	c.Env = envSlice

	if dc.ContainerUser != "" {
		if err := applyContainerUser(&c, dc.ContainerUser); err != nil {
			return c, nil, err
		}
	}
	applyFeatureFlags(&c, dc, req.Features)

	// Workspace volume + mount.
	var vols []corev1.Volume
	if wv := workspaceVolume(dc, ProjectRequest{
		LocalWorkspace:        req.LocalWorkspace,
		WorkspaceVolumeSource: req.WorkspaceVolumeSource,
	}); wv != nil {
		vols = append(vols, *wv)
		c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
			Name:      wv.Name,
			MountPath: containerWorkspace,
		})
	}

	// Compose service-declared volumes for the primary.
	svcVols, mounts, err := translateComposeServiceVolumes(svc.Volumes, volMap)
	if err != nil {
		return c, nil, err
	}
	vols = append(vols, svcVols...)
	c.VolumeMounts = append(c.VolumeMounts, mounts...)

	objVols, objMounts, err := translateComposeServiceObjects(*svc, configBindings, secretBindings)
	if err != nil {
		return c, nil, err
	}
	vols = append(vols, objVols...)
	c.VolumeMounts = append(c.VolumeMounts, objMounts...)

	// devcontainer.json mounts and feature mounts (same translator the
	// single-container projection uses).
	for _, raw := range dc.Mounts {
		v, vm, err := parseMount(raw)
		if err != nil {
			return c, nil, err
		}
		if v == nil || vm == nil {
			continue
		}
		vols = append(vols, *v)
		c.VolumeMounts = append(c.VolumeMounts, *vm)
	}
	for _, f := range req.Features {
		for _, raw := range f.Metadata.Mounts {
			v, vm, err := parseMount(raw)
			if err != nil {
				return c, nil, fmt.Errorf("feature %q: %w", f.Source, err)
			}
			if v == nil || vm == nil {
				continue
			}
			vols = append(vols, *v)
			c.VolumeMounts = append(c.VolumeMounts, *vm)
		}
	}

	// Ports: forwardPorts/appPort + compose `ports:` for the primary.
	if err := applyPorts(&c, dc.ForwardPorts, dc.AppPort); err != nil {
		return c, nil, err
	}
	composePorts, err := composePortsToContainerPorts(svc.Ports, dc.Service)
	if err != nil {
		return c, nil, err
	}
	c.Ports = append(c.Ports, composePorts...)

	if len(dc.RunArgs) > 0 {
		// runArgs lands on the Pod and the primary container — same
		// shape as single-container projection. We pass a synthetic
		// pod here because applyRunArgs writes onto pod.Spec; the
		// caller will see the container's portion via &c.
		dummyPod := &corev1.Pod{}
		if err := applyRunArgs(dc.RunArgs, dummyPod, &c); err != nil {
			return c, nil, err
		}
		// Note: pod-wide runArgs settings (HostNetwork, NodeSelector,
		// SecurityContext) are not propagated up here; the multi-Pod
		// projection would need them, but for sidecar mode the
		// container-level fields are enough.
	}

	return c, vols, nil
}

// buildSidecarContainer assembles a container for a non-primary
// compose service. Features and devcontainer.json fields don't apply
// to sidecars; they take only what compose declared.
func buildSidecarContainer(
	svc composetypes.ServiceConfig, volMap map[string]composeVolumeBinding,
	configBindings, secretBindings map[string]composeObjectBinding,
) (corev1.Container, []corev1.Volume, error) {
	c := corev1.Container{
		Name:       svc.Name,
		Image:      svc.Image,
		WorkingDir: svc.WorkingDir,
		TTY:        svc.Tty,
		Stdin:      svc.StdinOpen,
	}
	if svc.Image == "" {
		return c, nil, fmt.Errorf("compose-side `build:` is out of scope; service %q must have an image", svc.Name)
	}
	if len(svc.Entrypoint) > 0 {
		c.Command = []string(svc.Entrypoint)
	}
	if len(svc.Command) > 0 {
		c.Args = []string(svc.Command)
	}
	c.Env = composeEnvToK8s(svc.Environment)
	if svc.User != "" {
		if err := applyContainerUser(&c, svc.User); err != nil {
			return c, nil, fmt.Errorf("service %q: %w", svc.Name, err)
		}
	}
	if svc.Privileged {
		ensureSecCtx(&c).Privileged = boolPtr(true)
	}
	if len(svc.CapAdd) > 0 {
		sc := ensureSecCtx(&c)
		if sc.Capabilities == nil {
			sc.Capabilities = &corev1.Capabilities{}
		}
		for _, cap := range svc.CapAdd {
			sc.Capabilities.Add = append(sc.Capabilities.Add, corev1.Capability(cap))
		}
	}
	if svc.ReadOnly {
		ensureSecCtx(&c).ReadOnlyRootFilesystem = boolPtr(true)
	}

	vols, mounts, err := translateComposeServiceVolumes(svc.Volumes, volMap)
	if err != nil {
		return c, nil, fmt.Errorf("service %q: %w", svc.Name, err)
	}
	c.VolumeMounts = mounts

	objVols, objMounts, err := translateComposeServiceObjects(svc, configBindings, secretBindings)
	if err != nil {
		return c, nil, fmt.Errorf("service %q: %w", svc.Name, err)
	}
	vols = append(vols, objVols...)
	c.VolumeMounts = append(c.VolumeMounts, objMounts...)

	ports, err := composePortsToContainerPorts(svc.Ports, svc.Name)
	if err != nil {
		return c, nil, fmt.Errorf("service %q: %w", svc.Name, err)
	}
	c.Ports = ports

	return c, vols, nil
}

// translateComposeServiceVolumes renders one ServiceVolumeConfig per
// entry into a (Pod-volume, container-volumeMount) pair. Bind types
// produce HostPath volumes; named-volume types reference the PVC
// generated by projectComposeNamedVolumes.
func translateComposeServiceVolumes(
	in []composetypes.ServiceVolumeConfig, volMap map[string]composeVolumeBinding,
) ([]corev1.Volume, []corev1.VolumeMount, error) {
	if len(in) == 0 {
		return nil, nil, nil
	}
	var vols []corev1.Volume
	var mounts []corev1.VolumeMount
	for _, v := range in {
		switch v.Type {
		case composetypes.VolumeTypeBind, "":
			if v.Source == "" {
				return nil, nil, fmt.Errorf("compose volume on %s: bind needs source", v.Target)
			}
			if v.Source[0] != '/' {
				return nil, nil, fmt.Errorf("compose bind source %q must be absolute on Kubernetes", v.Source)
			}
			name := sanitizeVolumeName(v.Source)
			vols = append(vols, corev1.Volume{
				Name: name,
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{Path: v.Source},
				},
			})
			mounts = append(mounts, corev1.VolumeMount{
				Name:      name,
				MountPath: v.Target,
				ReadOnly:  v.ReadOnly,
			})
		case composetypes.VolumeTypeVolume:
			binding, ok := volMap[v.Source]
			if !ok {
				return nil, nil, fmt.Errorf("compose named volume %q is not declared in the project's top-level volumes:", v.Source)
			}
			vols = append(vols, corev1.Volume{
				Name: binding.Name,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: binding.PVCName,
						ReadOnly:  v.ReadOnly,
					},
				},
			})
			subPath := ""
			if v.Volume != nil {
				subPath = v.Volume.Subpath
			}
			mounts = append(mounts, corev1.VolumeMount{
				Name:      binding.Name,
				MountPath: v.Target,
				ReadOnly:  v.ReadOnly,
				SubPath:   subPath,
			})
		case composetypes.VolumeTypeTmpfs:
			name := sanitizeVolumeName("tmpfs-" + v.Target)
			src := corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory}
			vols = append(vols, corev1.Volume{
				Name:         name,
				VolumeSource: corev1.VolumeSource{EmptyDir: &src},
			})
			mounts = append(mounts, corev1.VolumeMount{
				Name:      name,
				MountPath: v.Target,
			})
		default:
			return nil, nil, fmt.Errorf("compose volume type %q is not supported in the multi-container Pod projection (target %s)", v.Type, v.Target)
		}
	}
	return vols, mounts, nil
}

// composePortsToContainerPorts maps compose `ports:` entries onto
// ContainerPort declarations on the per-service container. The
// publish-to-localhost / hostPort semantics from compose don't carry
// over: published ports become container ports only, since
// containers in a Pod share the network namespace and external
// reachability is the embedder's job (Service / Ingress).
func composePortsToContainerPorts(ports []composetypes.ServicePortConfig, svcName string) ([]corev1.ContainerPort, error) {
	if len(ports) == 0 {
		return nil, nil
	}
	out := make([]corev1.ContainerPort, 0, len(ports))
	for i, p := range ports {
		if p.Target == 0 {
			continue
		}
		proto := corev1.ProtocolTCP
		switch p.Protocol {
		case "", "tcp":
		case "udp":
			proto = corev1.ProtocolUDP
		case "sctp":
			proto = corev1.ProtocolSCTP
		default:
			return nil, fmt.Errorf("port %d: unknown protocol %q", p.Target, p.Protocol)
		}
		name := p.Name
		if name == "" {
			name = svcName + "-" + strconv.Itoa(i)
		}
		// Kubernetes container port names must be DNS-1123-label-ish
		// AND <= 15 chars. Truncate to keep the API server happy.
		if len(name) > 15 {
			name = name[:15]
		}
		out = append(out, corev1.ContainerPort{
			Name:          name,
			ContainerPort: int32(p.Target),
			Protocol:      proto,
		})
	}
	return out, nil
}

type kv struct{ k, v string }

func sortedComposeEnv(env composetypes.MappingWithEquals) []kv {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]kv, 0, len(keys))
	for _, k := range keys {
		v := env[k]
		if v == nil {
			continue
		}
		out = append(out, kv{k: k, v: *v})
	}
	return out
}

func composeEnvToK8s(env composetypes.MappingWithEquals) []corev1.EnvVar {
	pairs := sortedComposeEnv(env)
	out := make([]corev1.EnvVar, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, corev1.EnvVar{Name: p.k, Value: p.v})
	}
	return out
}
