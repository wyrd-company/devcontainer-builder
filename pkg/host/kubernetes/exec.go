package kubernetes

import (
	"bytes"
	"context"
	"io"
	"net/url"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// ExecRequest captures the inputs to a single apiserver pod-exec call.
// Mirrors pkg/host/docker.ExecRequest so embedders that already use
// the docker host don't need a separate vocabulary for kubernetes.
type ExecRequest struct {
	Namespace string
	PodName   string
	// ContainerName picks a container in the pod. Empty means the
	// pod's first container — matches `kubectl exec`'s default.
	ContainerName string
	// Cmd is the argv passed to the container. Caller is responsible
	// for shell wrapping.
	Cmd []string
	// User and WorkDir don't have a clean apiserver-exec equivalent
	// (kubectl exec doesn't take -u or --workdir), so they're
	// translated into a `cd <workdir>; exec setpriv ... or su -c
	// ...` shell wrapper. Empty values are no-ops.
	User    string
	WorkDir string
	// Env is layered on top of the container's environment for this
	// exec via a leading `env K=V K=V <cmd>`. Empty is a no-op.
	Env map[string]string
	// TTY allocates a pseudo-terminal.
	TTY bool
	// Stdin / Stdout / Stderr are forwarded to/from the exec session.
	// Any of them may be nil; nil stdin sends EOF immediately, nil
	// stdout/stderr captures into the returned ExecResult.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// ExecResult reports the outcome.
type ExecResult struct {
	// ExitCode is best-effort: the apiserver's exec stream surfaces
	// non-zero exits as a wrapped error rather than a structured
	// status, so a non-nil err from Exec means ExitCode is "unknown
	// non-zero". For matching docker semantics we return ExitCode = 1
	// in that case.
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}

// Exec runs a command inside an existing pod and waits for it to
// finish. When Stdin/Stdout/Stderr are nil, output is captured into
// the returned ExecResult; when supplied, output streams to the
// caller's writers and the buffers are empty.
func (h *Host) Exec(ctx context.Context, req ExecRequest) (*ExecResult, error) {
	if h.cfg == nil {
		return nil, errorf("Exec requires a non-nil rest.Config (pass one to New)")
	}
	if req.PodName == "" {
		return nil, errorf("ExecRequest.PodName is required")
	}
	if len(req.Cmd) == 0 {
		return nil, errorf("ExecRequest.Cmd is empty")
	}

	ns := h.namespaceOr(req.Namespace)

	// Build the command we'll send. The apiserver's exec subresource
	// takes the argv verbatim; user / workdir / env are translated
	// into a /bin/sh wrapper because pod-exec doesn't carry those out
	// of band the way docker exec does.
	cmd := wrapExecCommand(req.Cmd, req.User, req.WorkDir, req.Env)

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

	restClient := h.cs.CoreV1().RESTClient()
	u := restClient.Post().
		Resource("pods").
		Name(req.PodName).
		Namespace(ns).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: req.ContainerName,
			Command:   cmd,
			Stdin:     req.Stdin != nil,
			Stdout:    true,
			Stderr:    !req.TTY, // when TTY, stdout/stderr come over the same channel
			TTY:       req.TTY,
		}, scheme.ParameterCodec).
		URL()

	exec, err := newSPDYExecutor(h.cfg, "POST", u)
	if err != nil {
		return nil, errorf("exec dial: %w", err)
	}

	streamErr := exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  req.Stdin,
		Stdout: orDiscard(stdoutW),
		Stderr: orDiscard(stderrW),
		Tty:    req.TTY,
	})

	res := &ExecResult{}
	if captureStdout {
		res.Stdout = capturedOut.Bytes()
	}
	if captureStderr {
		res.Stderr = capturedErr.Bytes()
	}
	if streamErr != nil {
		// remotecommand surfaces non-zero exits as exec.CodeExitError;
		// extract the code if we can, otherwise treat as exit 1 so
		// downstream code that switches on ExitCode != 0 still works.
		if code, ok := exitCodeFromError(streamErr); ok {
			res.ExitCode = code
			return res, nil
		}
		return res, errorf("exec stream: %w", streamErr)
	}
	return res, nil
}

// wrapExecCommand turns argv + user/workdir/env into the actual argv
// the apiserver runs. If none of the optional fields are set we send
// the argv verbatim so a caller that wants a literal `["/bin/ls"]`
// gets it without extra shell wrapping.
func wrapExecCommand(cmd []string, user, workdir string, env map[string]string) []string {
	if user == "" && workdir == "" && len(env) == 0 {
		return cmd
	}
	// Build a shell preamble. We deliberately use /bin/sh for parity
	// with how the docker host wraps lifecycle commands (and how the
	// runner's marker shell expects /bin/sh). The command itself is
	// passed via argv to "sh -c -- <script> -- <argv...>" so we don't
	// have to re-quote the argv.
	var script bytes.Buffer
	if workdir != "" {
		script.WriteString("cd ")
		script.WriteString(shellSQ(workdir))
		script.WriteString(" && ")
	}
	if len(env) > 0 {
		script.WriteString("env")
		for k, v := range env {
			script.WriteString(" ")
			script.WriteString(shellSQ(k + "=" + v))
		}
		script.WriteString(" ")
	}
	if user != "" {
		// `su` is the lowest common denominator across distros; -c
		// runs a single command. We quote-escape the original argv
		// once so it survives su's own shell.
		script.WriteString("su -s /bin/sh -c ")
		script.WriteString(shellSQ(joinArgvShell(cmd)))
		script.WriteString(" ")
		script.WriteString(shellSQ(user))
		return []string{"/bin/sh", "-c", script.String()}
	}
	// No user wrap: exec the argv directly through the shell.
	script.WriteString("exec ")
	script.WriteString(joinArgvShell(cmd))
	return []string{"/bin/sh", "-c", script.String()}
}

// joinArgvShell quotes each argv element for safe re-shelling. It's
// used both for the su -c wrapper and the simple workdir+env wrapper.
func joinArgvShell(argv []string) string {
	var b bytes.Buffer
	for i, a := range argv {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(shellSQ(a))
	}
	return b.String()
}

// shellSQ wraps a string in single quotes, escaping any embedded
// single-quote with the standard '\” construction.
func shellSQ(s string) string {
	out := make([]byte, 0, len(s)+2)
	out = append(out, '\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'', '\\', '\'', '\'')
			continue
		}
		out = append(out, s[i])
	}
	out = append(out, '\'')
	return string(out)
}

func orDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}

// newSPDYExecutor is a small seam so tests can stub out the SPDY
// transport (which needs a real TCP connection to the apiserver and
// can't run against the fake clientset). Default implementation
// dials the apiserver via the supplied rest.Config.
var newSPDYExecutor = func(cfg *rest.Config, method string, u *url.URL) (remotecommand.Executor, error) {
	return remotecommand.NewSPDYExecutor(cfg, method, u)
}

// exitCodeFromError unwraps the exec.CodeExitError that
// client-go/util/exec returns when the exec command exits non-zero.
// Defined here as an interface check so tests can supply a fake error
// type with the same shape.
func exitCodeFromError(err error) (int, bool) {
	type codeExit interface {
		ExitStatus() int
	}
	if ce, ok := err.(codeExit); ok {
		return ce.ExitStatus(), true
	}
	return 0, false
}
