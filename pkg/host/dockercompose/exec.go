package dockercompose

import (
	"bytes"
	"context"
	"io"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/wyrd-company/devcontainer-builder/pkg/host/runner"
)

// ExecRequest is the per-container exec call; same shape as the
// docker host's so embedders moving between modes have one mental
// model.
type ExecRequest struct {
	// TerminalSize sets the initial PTY size [rows, columns] when TTY is true.
	TerminalSize *[2]uint
	ContainerID  string
	Cmd          []string
	User         string
	WorkDir      string
	Env          map[string]string
	TTY          bool
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer
}

// ExecResult mirrors docker host's: captured output is returned only
// when the corresponding writer was nil.
type ExecResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

// Exec runs a command inside an existing project container.
func (h *Host) Exec(ctx context.Context, req ExecRequest) (*ExecResult, error) {
	if req.ContainerID == "" {
		return nil, errorf("ExecRequest.ContainerID is required")
	}
	if len(req.Cmd) == 0 {
		return nil, errorf("ExecRequest.Cmd is empty")
	}

	envSlice := make([]string, 0, len(req.Env))
	for k, v := range req.Env {
		envSlice = append(envSlice, k+"="+v)
	}

	captureStdout := req.Stdout == nil
	captureStderr := req.Stderr == nil
	var capturedOut, capturedErr bytes.Buffer
	stdoutW := req.Stdout
	stderrW := req.Stderr
	if captureStdout {
		stdoutW = &capturedOut
	}
	if captureStderr {
		stderrW = &capturedErr
	}

	createResp, err := h.client.ContainerExecCreate(ctx, req.ContainerID, container.ExecOptions{
		User:         req.User,
		Tty:          req.TTY,
		AttachStdin:  req.Stdin != nil,
		AttachStdout: true,
		AttachStderr: true,
		Env:          envSlice,
		WorkingDir:   req.WorkDir,
		Cmd:          req.Cmd,
	})
	if err != nil {
		return nil, errorf("exec create: %w", err)
	}

	attachResp, err := h.client.ContainerExecAttach(ctx, createResp.ID, container.ExecAttachOptions{
		Tty:         req.TTY,
		ConsoleSize: req.TerminalSize,
	})
	if err != nil {
		return nil, errorf("exec attach: %w", err)
	}
	defer attachResp.Close()

	errCh := make(chan error, 1)
	go func() {
		var copyErr error
		if req.TTY {
			_, copyErr = io.Copy(orDiscard(stdoutW), attachResp.Reader)
		} else {
			_, copyErr = stdcopy.StdCopy(orDiscard(stdoutW), orDiscard(stderrW), attachResp.Reader)
		}
		errCh <- copyErr
	}()

	if req.Stdin != nil {
		go func() {
			_, _ = io.Copy(attachResp.Conn, req.Stdin)
			if req.TTY {
				// A TTY needs terminal EOF rather than closing the hijacked socket's
				// input side, which can detach before the remaining output is read.
				_, _ = attachResp.Conn.Write([]byte{4})
			} else {
				_ = attachResp.CloseWrite()
			}
		}()
	}

	// A canceled probe must release its attach and stream reader before returning.
	var streamErr error
	select {
	case streamErr = <-errCh:
	case <-ctx.Done():
		attachResp.Close()
		<-errCh
		return nil, ctx.Err()
	}
	if err := streamErr; err != nil && err != io.EOF {
		return nil, errorf("exec stream: %w", err)
	}

	inspect, err := h.client.ContainerExecInspect(ctx, createResp.ID)
	if err != nil {
		return nil, errorf("exec inspect: %w", err)
	}

	res := &ExecResult{ExitCode: inspect.ExitCode}
	if captureStdout {
		res.Stdout = capturedOut.Bytes()
	}
	if captureStderr {
		res.Stderr = capturedErr.Bytes()
	}
	return res, nil
}

// runnerExecer adapts *Host onto runner.Execer for a single
// container ID. The runner package owns marker gating and phase
// dispatch; this type translates one ExecRequest into a docker exec.
type runnerExecer struct {
	host        *Host
	containerID string
}

// Execer returns a runner.Execer for the given container ID. Use the
// primary service's container ID to drive lifecycle commands.
func (h *Host) Execer(containerID string) runner.Execer {
	return runnerExecer{host: h, containerID: containerID}
}

func (a runnerExecer) Exec(ctx context.Context, req runner.ExecRequest) (*runner.ExecResult, error) {
	res, err := a.host.Exec(ctx, ExecRequest{
		ContainerID:  a.containerID,
		TTY:          req.TerminalSize != nil,
		TerminalSize: req.TerminalSize,
		Cmd:          req.Cmd,
		User:         req.User,
		WorkDir:      req.WorkDir,
		Env:          req.Env,
		Stdin:        req.Stdin,
		Stdout:       req.Stdout,
		Stderr:       req.Stderr,
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

func orDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}
