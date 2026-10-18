// Package docker brings a devcontainer up against a Docker Engine by
// talking to the engine API directly — no `docker` CLI shell-out, so the
// package is safely embeddable in services and operators. Podman is not
// supported.
//
// Compatibility with the upstream devcontainer CLI is intentional. Container
// labels (devcontainer.local_folder, devcontainer.config_file), marker
// file paths, and lifecycle command semantics mirror
// microsoft/devcontainers-cli so a container created here can be picked
// up by upstream tooling (and vice versa).
//
// # Embedding
//
// Construct a Host with a *client.Client you already manage:
//
//	cli, _ := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
//	h := docker.New(cli, docker.Options{Logger: log.Printf})
//	res, err := h.Up(ctx, docker.UpRequest{...})
//
// The package never reads $DOCKER_HOST, never writes to stdout/stderr
// unless explicitly given writers in Options or per-call request,
// never consults global state. That makes it safe to drop into an HTTP
// handler, a controller-runtime reconcile loop, or a test.
package docker

import (
	"errors"
	"fmt"
	"io"
	"time"

	dockerclient "github.com/docker/docker/client"
)

// Labels written by Up so containers can be rediscovered later. Names
// match upstream's src/spec-node/singleContainer.ts.
const (
	LabelLocalFolder = "devcontainer.local_folder"
	LabelConfigFile  = "devcontainer.config_file"
)

// ErrContainerNotFound is returned by Inspect/Stop/Remove when no
// container matches the supplied selector.
var ErrContainerNotFound = errors.New("docker host: container not found")

// Options tunes a Host. All fields are optional.
type Options struct {
	// Logger receives informational messages. If nil, messages are
	// discarded — appropriate for a service or operator that handles
	// its own structured logging via context.
	Logger func(format string, args ...any)
	// Stdout / Stderr default to nil (silent). Lifecycle command output
	// is written to these unless the per-call request overrides.
	Stdout io.Writer
	Stderr io.Writer
	// StopTimeout bounds how long Stop waits for graceful shutdown
	// before SIGKILL. Defaults to 10s.
	StopTimeout time.Duration
}

// Host is the per-daemon connection. Safe for concurrent use.
type Host struct {
	client *dockerclient.Client
	opts   Options
}

// New wraps an existing docker client. The client lifetime is the
// caller's responsibility; New does not Close it.
func New(c *dockerclient.Client, opts Options) *Host {
	if opts.StopTimeout == 0 {
		opts.StopTimeout = 10 * time.Second
	}
	return &Host{client: c, opts: opts}
}

// Client returns the underlying docker client. Provided so embedders
// can issue ad-hoc calls (e.g. an operator that wants to attach a
// custom network) without re-dialing the daemon.
func (h *Host) Client() *dockerclient.Client { return h.client }

// logf is the package's internal logger. Never panics on nil.
func (h *Host) logf(format string, args ...any) {
	if h.opts.Logger != nil {
		h.opts.Logger(format, args...)
	}
}

// stdoutOr returns the request-supplied writer or, failing that, the
// Host's default; nil when both are unset (output is discarded).
func (h *Host) stdoutOr(w io.Writer) io.Writer {
	if w != nil {
		return w
	}
	return h.opts.Stdout
}

func (h *Host) stderrOr(w io.Writer) io.Writer {
	if w != nil {
		return w
	}
	return h.opts.Stderr
}

// errorf wraps fmt.Errorf with a package prefix so users see consistent
// error origins in logs.
func errorf(format string, args ...any) error {
	return fmt.Errorf("docker host: "+format, args...)
}
