package docker

import (
	"context"
	"strings"

	"github.com/wyrd-company/devcontainer-builder/pkg/host/runner"
)

// runnerExecer adapts *Host onto runner.Execer for a single
// containerID. The runner package owns the marker-gating and the
// phase dispatch; this type only translates one ExecRequest into a
// docker exec call.
type runnerExecer struct {
	host        *Host
	containerID string
}

// Execer returns a runner.Execer that runs commands inside the given
// container ID. Embedders that drive lifecycle commands themselves
// (rather than calling Up) can hand the result to runner.Run.
func (h *Host) Execer(containerID string) runner.Execer {
	return runnerExecer{host: h, containerID: containerID}
}

func (a runnerExecer) Exec(ctx context.Context, req runner.ExecRequest) (*runner.ExecResult, error) {
	res, err := a.host.Exec(ctx, ExecRequest{
		ContainerID: a.containerID,
		Cmd:         req.Cmd,
		User:        req.User,
		WorkDir:     req.WorkDir,
		Env:         req.Env,
		Stdin:       req.Stdin,
		Stdout:      req.Stdout,
		Stderr:      req.Stderr,
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

// shellSQRaw escapes single quotes for safe interpolation inside a
// single-quoted shell literal, using the standard '\'' construction.
// Kept here as the only remaining shell-quoting need on the docker
// side (the uid update script). Marker-script quoting lives with the
// marker shell, in pkg/host/runner.
func shellSQRaw(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}
