// Package kubernetes brings a devcontainer up against a Kubernetes
// apiserver: discover-or-create the Pod, exec lifecycle phases through
// the pod-exec subresource, port-forward, delete on teardown.
//
// The package builds on pkg/projection/kubernetes for the spec → Pod
// translation and on pkg/host/runner for marker-gated lifecycle
// dispatch, mirroring the shape of pkg/host/docker against the docker
// engine. The same `runner.Execer` surface that the docker host adapts
// onto docker exec is adapted here onto remotecommand.NewSPDYExecutor,
// so an operator that wraps the kubernetes Exec by hand sees the same
// upstream-compatible marker shell.
//
// Compatibility with the upstream devcontainer CLI is intentional:
// container labels (devcontainer.local_folder, devcontainer.config_file)
// land on the Pod's annotations, marker file paths and lifecycle command
// semantics mirror microsoft/devcontainers-cli, and the in-pod
// keepalive is the same shell loop used by pkg/host/docker.
//
// # Embedding
//
// Construct a Host with a kubernetes.Interface and the matching
// *rest.Config (the latter needed for SPDY exec / port-forward, both of
// which talk a streaming protocol the typed clientset doesn't expose):
//
//	cfg, _ := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
//	cs, _ := kubernetes.NewForConfig(cfg)
//	h := k8shost.New(cs, cfg, k8shost.Options{Logger: log.Printf})
//	res, err := h.Up(ctx, k8shost.UpRequest{...})
//
// The package never reads $KUBECONFIG, never writes to stdout/stderr
// unless explicitly given writers in Options or per-call request,
// never consults global state. Safe to drop into an HTTP handler, a
// controller-runtime reconcile loop, or a test.
//
// # CLI surface
//
// Deliberately none. The upstream "kubectl + manifest" workflow is the
// expected UX for the rendering case (covered by
// pkg/projection/kubernetes); the imperative side belongs in services
// and operators that already own a kubernetes.Interface, not in a
// shell-out CLI that re-implements kubeconfig discovery.
package kubernetes
