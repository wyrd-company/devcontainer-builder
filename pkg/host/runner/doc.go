// Package runner executes a pkg/lifecycle.Plan against an
// already-running container, abstracted from the transport that talks
// to that container.
//
// The transport — docker exec, the apiserver pod-exec subresource, an
// SSH session, an in-process shim for tests — is supplied by the
// embedder as an Execer. The runner owns the parts that are identical
// across transports: marker-file gating (WhenOnce / WhenStart /
// WhenAttach), Shell / Args / Parallel command-form dispatch,
// per-parallel-command output buffering, and error formatting. Those
// behaviors mirror upstream's src/spec-common/injectHeadless.ts so a
// container created by either host stays compatible with the upstream
// devcontainer CLI.
//
// pkg/host/docker uses this package internally; a future
// pkg/host/kubernetes will use it the same way against the apiserver
// exec subresource.
//
// # Embedding
//
// An operator that wants to drive lifecycle commands itself can plug
// in any Execer:
//
//	plan := lifecycle.Plan(dc, features)
//	rt := runner.Runtime{
//	    User:           dc.RemoteUser,
//	    Workdir:        containerWorkspace,
//	    CreatedAt:      pod.CreationTimestamp.UTC().Format(time.RFC3339Nano),
//	    StartedAt:      pod.Status.StartTime.UTC().Format(time.RFC3339Nano),
//	    UserDataFolder: lifecycle.UserDataFolder(homeDir, ""),
//	}
//	err := runner.Run(ctx, myK8sExecer{client: cs, pod: pod}, plan, rt, runner.Options{
//	    Logger: klog.Infof,
//	    Stdout: os.Stdout,
//	    Stderr: os.Stderr,
//	})
package runner
