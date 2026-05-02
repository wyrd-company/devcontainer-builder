// Package kubernetes projects a parsed devcontainer.json (plus its
// resolved features) into the Kubernetes API objects that would launch
// the same container on a cluster.
//
// The package is pure: no client-go, no apiserver round-trips. Project
// returns a *corev1.Pod (and any ancillary objects) so the caller — an
// operator's controller, a CLI's render command, an admission webhook —
// can decide how to apply, validate, or serialize them.
//
// The translation mirrors the field mapping that pkg/host/docker
// performs against the docker engine API. Where docker has a single
// flat HostConfig, Kubernetes splits the same surface across PodSpec,
// SecurityContext, Resources, and Volumes — Project handles the
// re-shaping and rejects fields that don't have a clean Kubernetes
// equivalent rather than silently dropping them.
//
// # Embedding
//
//	pj, err := kubernetes.Project(kubernetes.ProjectRequest{
//	    Spec:           dc,
//	    SpecPath:       dc.Path,
//	    LocalWorkspace: workspace,
//	    Image:          "registry.example.com/devbox:1.2.3",
//	    Name:           "alice-devbox",
//	    Namespace:      "dev",
//	})
//	// apply pj.Pod with controller-runtime, kubectl apply -f, etc.
//
// # Lifecycle commands
//
// Project does not embed onCreate/postCreate/postStart/postAttach into
// initContainers or lifecycle hooks: those phases need the running
// container's userspace state (features installed by previous phases,
// the workspace bind contents) and can't be modeled as static manifest
// fields. A future imperative pkg/host/kubernetes will exec them after
// the pod reaches Ready, the same shape pkg/host/docker uses today.
package kubernetes
