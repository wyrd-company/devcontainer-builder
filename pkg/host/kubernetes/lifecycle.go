package kubernetes

import (
	"context"

	"github.com/wyrd-company/devcontainer-builder/pkg/host/runner"
)

// runnerExecer adapts *Host onto runner.Execer for a single
// namespace+pod+container triple. The runner package owns the
// marker-gating and the phase dispatch; this type only translates one
// runner.ExecRequest into a kubernetes Exec call.
type runnerExecer struct {
	host          *Host
	namespace     string
	podName       string
	containerName string
}

// Execer returns a runner.Execer that runs commands inside the given
// namespace/pod/container. Embedders that drive lifecycle commands
// themselves (rather than calling Up) can hand the result to
// runner.Run — the same pattern pkg/host/docker exposes.
func (h *Host) Execer(namespace, podName, containerName string) runner.Execer {
	return runnerExecer{
		host:          h,
		namespace:     h.namespaceOr(namespace),
		podName:       podName,
		containerName: containerName,
	}
}

func (a runnerExecer) Exec(ctx context.Context, req runner.ExecRequest) (*runner.ExecResult, error) {
	res, err := a.host.Exec(ctx, ExecRequest{
		Namespace:     a.namespace,
		PodName:       a.podName,
		ContainerName: a.containerName,
		Cmd:           req.Cmd,
		User:          req.User,
		WorkDir:       req.WorkDir,
		Env:           req.Env,
		Stdin:         req.Stdin,
		Stdout:        req.Stdout,
		Stderr:        req.Stderr,
	})
	if err != nil {
		return nil, err
	}
	return &runner.ExecResult{
		ExitCode: res.ExitCode,
		Stdout:   res.Stdout,
		Stderr:   res.Stderr,
	}, nil
}
