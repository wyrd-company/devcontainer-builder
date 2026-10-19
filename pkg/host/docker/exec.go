package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/pkg/stdcopy"
)

// ExecRequest captures the inputs to a single docker exec call.
type ExecRequest struct {
	// TerminalSize sets the initial PTY size [rows, columns] when TTY is true.
	TerminalSize *[2]uint
	ContainerID  string
	// Cmd is the argv passed to the container. Caller is responsible
	// for shell wrapping (string lifecycle commands wrap with /bin/sh -c
	// before reaching here).
	Cmd []string
	// User is "<uid>[:<gid>]" or a username — same as docker exec -u.
	User string
	// WorkDir is the cwd inside the container.
	WorkDir string
	// Env is appended to the container's environment for this exec.
	Env map[string]string
	// TTY allocates a pseudo-terminal. Required for interactive shells.
	TTY bool
	// Stdin / Stdout / Stderr are forwarded to/from the exec session.
	// Any of them may be nil; nil stdin sends EOF immediately, nil
	// stdout/stderr discards that stream.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// ExecResult reports the outcome of a docker exec.
type ExecResult struct {
	ExitCode int
	// Stdout / Stderr capture output IF the request did not provide
	// writers — captured-mode is the default for "did the marker check
	// succeed?" probes. When the request supplies writers, these are
	// empty.
	Stdout []byte
	Stderr []byte
}

// Exec runs a command inside an existing container and waits for it
// to finish. Returns the exit code and any captured output.
//
// When Stdin/Stdout/Stderr are nil, output is captured into the
// returned ExecResult. When supplied, output streams to the caller's
// writers and the returned ExecResult buffers are empty.
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

	// Pump streams. With TTY, both stdout and stderr come over the same
	// channel as a raw byte stream; without TTY, frames are multiplexed
	// and we need stdcopy to demux.
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

// execShellQuiet runs /bin/sh -c <script> as the given user and returns
// (exitCode, stdout). Used for marker file checks and similar internal
// probes where we don't want to surface output to the user.
func (h *Host) execShellQuiet(ctx context.Context, containerID, user, script string) (int, string, error) {
	res, err := h.Exec(ctx, ExecRequest{
		ContainerID: containerID,
		Cmd:         []string{"/bin/sh", "-c", script},
		User:        user,
	})
	if err != nil {
		return 0, "", err
	}
	return res.ExitCode, strings.TrimRight(string(res.Stdout), "\n"), nil
}

func orDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

// formatExecError shapes a non-zero exit code into a Go error with the
// captured stderr inline. Callers can errors.As to access the
// underlying docker SDK error if they need more.
func formatExecError(label string, exitCode int, stderr string) error {
	if stderr == "" {
		return fmt.Errorf("%s: exit %d", label, exitCode)
	}
	return fmt.Errorf("%s: exit %d: %s", label, exitCode, strings.TrimSpace(stderr))
}
