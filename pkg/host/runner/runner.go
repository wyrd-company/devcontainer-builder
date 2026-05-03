package runner

import (
	"context"
	"fmt"
	"io"

	"github.com/wyrd-company/devcontainer-builder/pkg/lifecycle"
)

// Execer is the per-container transport. Implementations bind a
// "where to run" (a docker container ID, a pod+container name, an SSH
// host) and translate ExecRequest into the appropriate underlying
// call.
//
// Implementations must:
//
//   - return ExecResult.ExitCode = 0 only when the command succeeded;
//   - capture stdout/stderr into the ExecResult buffers when the
//     corresponding Stdout/Stderr writer in the request is nil
//     (the runner relies on captured stdout for the home-directory
//     and container-env probes);
//   - stream to Stdout/Stderr when the writers are non-nil (the
//     runner uses this for lifecycle command output, where the user
//     wants to see progress live).
//
// The runner does not retry, time out, or cancel beyond ctx — those
// are the embedder's responsibility, applied through the supplied
// context.
type Execer interface {
	Exec(ctx context.Context, req ExecRequest) (*ExecResult, error)
}

// ExecRequest is the transport-agnostic exec call.
type ExecRequest struct {
	// Cmd is the argv passed to the container. Lifecycle string
	// commands are wrapped with /bin/sh -c by the runner before they
	// reach the Execer.
	Cmd []string
	// User is the in-container user to run as ("uid[:gid]" or a
	// username). Empty means the container's default.
	User string
	// WorkDir is the cwd inside the container. Empty means the
	// container's default.
	WorkDir string
	// Env is layered on top of the container's existing environment
	// for this exec.
	Env map[string]string
	// Stdin / Stdout / Stderr are forwarded to/from the exec session.
	// nil stdin sends EOF immediately. nil stdout/stderr signals the
	// Execer that the runner wants captured output in the result.
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// ExecResult reports the outcome.
type ExecResult struct {
	ExitCode int
	// Stdout / Stderr are populated only when the corresponding writer
	// in the request was nil.
	Stdout []byte
	Stderr []byte
}

// Runtime is the per-container state the runner needs to gate phases
// idempotently. The host package fills it in once after start; the
// runner reads it.
type Runtime struct {
	// User is the lifecycle command user (typically dc.RemoteUser).
	// May be empty: the Execer falls back to the container's default.
	User string
	// Workdir is the cwd lifecycle commands run in.
	Workdir string
	// Env is layered on top of the container env for every lifecycle
	// exec.
	Env map[string]string
	// CreatedAt is the ISO-8601 timestamp marker for WhenOnce phases.
	// On docker, this is ContainerJSON.Created; on kubernetes,
	// pod.CreationTimestamp serialized to RFC3339Nano. Any stable
	// per-creation string works as long as it changes when the
	// container is recreated.
	CreatedAt string
	// StartedAt is the marker for WhenStart phases. Docker uses
	// State.StartedAt; kubernetes uses pod.Status.StartTime. Must
	// change every time the container restarts so postStart re-runs.
	StartedAt string
	// UserDataFolder is the in-container directory that holds the
	// per-hook marker files. Use lifecycle.UserDataFolder(homeDir, "")
	// to get the default.
	UserDataFolder string
}

// Options configure logging and default output streams.
type Options struct {
	// Logger receives informational messages ("running postCreate
	// [from devcontainer.json]", "skipping ... marker up to date").
	// nil discards.
	Logger func(format string, args ...any)
	// Stdout / Stderr are the default sinks for lifecycle command
	// output. Per-parallel-command output is captured and replayed
	// here under a banner — no interleaving — matching upstream's
	// printMode='off' behavior.
	Stdout io.Writer
	Stderr io.Writer
}

// PhaseResult reports whether a planned phase executed or was skipped by
// marker checks.
type PhaseResult struct {
	Phase lifecycle.Phase
	Ran   bool
}

// RunResult is the structured result of a lifecycle run.
type RunResult struct {
	Phases []PhaseResult
}

// RanPhases returns only phases whose lifecycle command actually executed.
func (r RunResult) RanPhases() []lifecycle.Phase {
	out := make([]lifecycle.Phase, 0, len(r.Phases))
	for _, p := range r.Phases {
		if p.Ran {
			out = append(out, p.Phase)
		}
	}
	return out
}

// Run executes plan against execer in order, observing markers to skip
// phases that have already run. The first phase that returns a
// non-nil error short-circuits with a wrapped error identifying the
// hook and origin.
func Run(ctx context.Context, execer Execer, plan []lifecycle.Phase, rt Runtime, opts Options) error {
	_, err := RunWithResult(ctx, execer, plan, rt, opts)
	return err
}

// RunWithResult is Run plus executed/skipped phase reporting.
func RunWithResult(ctx context.Context, execer Execer, plan []lifecycle.Phase, rt Runtime, opts Options) (*RunResult, error) {
	if execer == nil {
		return nil, fmt.Errorf("runner: Execer is required")
	}
	result := &RunResult{Phases: make([]PhaseResult, 0, len(plan))}
	for _, p := range plan {
		ran, err := runPhase(ctx, execer, p, rt, opts)
		if err != nil {
			return result, fmt.Errorf("%s [%s]: %w", p.Hook, p.Origin.Source, err)
		}
		result.Phases = append(result.Phases, PhaseResult{Phase: p, Ran: ran})
	}
	return result, nil
}

func logf(opts Options, format string, args ...any) {
	if opts.Logger != nil {
		opts.Logger(format, args...)
	}
}
