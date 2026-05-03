package dockercompose

import (
	"errors"
	"fmt"
	"io"
	"time"

	dockerclient "github.com/docker/docker/client"
)

// Compose-standard labels written by Up so containers can be
// rediscovered by either this package or the upstream `docker compose`
// CLI. Keys match docker/compose's own constants.
const (
	LabelProject       = "com.docker.compose.project"
	LabelService       = "com.docker.compose.service"
	LabelOneoff        = "com.docker.compose.oneoff"
	LabelConfigHash    = "com.docker.compose.config-hash"
	LabelContainerN    = "com.docker.compose.container-number"
	LabelWorkingDir    = "com.docker.compose.project.working_dir"
	LabelConfigFiles   = "com.docker.compose.project.config_files"
	LabelDependencies  = "com.docker.compose.depends_on"
	LabelVersion       = "com.docker.compose.version"
	LabelComposeVer    = "3.13.0" // minimum compose version we claim
)

// Devcontainer-side labels mirrored from pkg/host/docker so a project
// is discoverable by workspace.
const (
	LabelLocalFolder = "devcontainer.local_folder"
	LabelConfigFile  = "devcontainer.config_file"
	LabelPrimary     = "devcontainer.compose.primary"
)

// ErrProjectNotFound is returned when no compose project exists for a
// supplied selector (workspace path or project name).
var ErrProjectNotFound = errors.New("dockercompose host: project not found")

// ErrPrimaryNotFound is returned when the spec's `service` is not
// present in the merged compose project.
var ErrPrimaryNotFound = errors.New("dockercompose host: primary service not found in compose project")

// Options tunes a Host. All fields are optional.
type Options struct {
	// Logger receives informational messages. nil discards.
	Logger func(format string, args ...any)
	// Stdout / Stderr default to nil. Lifecycle command output goes
	// here unless the per-call request supplies its own writers.
	Stdout io.Writer
	Stderr io.Writer
	// StopTimeout bounds graceful shutdown. Defaults to 10s.
	StopTimeout time.Duration
}

// Host is the per-daemon connection. Safe for concurrent use.
type Host struct {
	client *dockerclient.Client
	opts   Options
}

// New wraps an existing docker client. Lifetime of the client is the
// caller's responsibility.
func New(c *dockerclient.Client, opts Options) *Host {
	if opts.StopTimeout == 0 {
		opts.StopTimeout = 10 * time.Second
	}
	return &Host{client: c, opts: opts}
}

// Client returns the underlying docker client so embedders can issue
// ad-hoc calls.
func (h *Host) Client() *dockerclient.Client { return h.client }

func (h *Host) logf(format string, args ...any) {
	if h.opts.Logger != nil {
		h.opts.Logger(format, args...)
	}
}

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

func errorf(format string, args ...any) error {
	return fmt.Errorf("dockercompose host: "+format, args...)
}
