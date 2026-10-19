package runner

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/wyrd-company/devcontainer-builder/pkg/lifecycle"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// shouldRunPhase checks and updates the phase's hook marker. The marker
// payload is the createdAt timestamp for WhenOnce, the startedAt
// timestamp for WhenStart, and unused for WhenAttach (no marker, always
// re-run).
func shouldRunPhase(ctx context.Context, execer Execer, p lifecycle.Phase, rt Runtime) (bool, error) {
	expected := ""
	switch p.When {
	case lifecycle.WhenOnce:
		expected = rt.CreatedAt
	case lifecycle.WhenStart:
		expected = rt.StartedAt
	}
	doRun, err := shouldRunMarker(ctx, execer, rt.User, p.MarkerPath(rt.UserDataFolder), expected)
	if err != nil {
		return false, fmt.Errorf("marker check (%s): %w", p.Hook, err)
	}
	return doRun || (rt.Lifecycle.Prebuild && p.Hook == lifecycle.HookUpdateContent), nil
}

// runPhase executes one Phase when its hook's marker check said to run.
func runPhase(ctx context.Context, execer Execer, p lifecycle.Phase, rt Runtime, opts Options, doRun bool) (bool, error) {
	if !doRun {
		logf(opts, "skipping %s (marker up to date) [%s]", p.Hook, p.Origin.Source)
		return false, nil
	}
	logf(opts, "running %s [%s]", p.Hook, p.Origin.Source)
	return true, execCommand(ctx, execer, p.Command, rt, opts)
}

// execCommand dispatches on the Command form: shell string, args
// array, or named-parallel object.
func execCommand(ctx context.Context, execer Execer, cmd spec.Command, rt Runtime, opts Options) error {
	switch {
	case cmd.Shell != "":
		return execOne(ctx, execer, []string{"/bin/sh", "-c", cmd.Shell}, rt, opts, "")
	case len(cmd.Args) > 0:
		return execOne(ctx, execer, cmd.Args, rt, opts, "")
	case len(cmd.Parallel) > 0:
		return execParallel(ctx, execer, cmd.Parallel, rt, opts)
	}
	return nil
}

func execOne(ctx context.Context, execer Execer, argv []string, rt Runtime, opts Options, name string) error {
	res, err := execer.Exec(ctx, ExecRequest{
		Cmd:     argv,
		User:    rt.User,
		WorkDir: rt.Workdir,
		Env:     rt.Env,
		Stdout:  opts.Stdout,
		Stderr:  opts.Stderr,
	})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		label := "lifecycle command"
		if name != "" {
			label = "parallel command " + name
		}
		return formatExecError(label, res.ExitCode, "")
	}
	return nil
}

// execParallel runs each named command concurrently. Output is
// preserved per-command (no interleaving) by capturing stdout/stderr
// per goroutine and printing under a name banner once each finishes —
// same shape as upstream's "if name then printMode='off'" branch.
func execParallel(ctx context.Context, execer Execer, cmds map[string]spec.Command, rt Runtime, opts Options) error {
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		outputMu sync.Mutex
		firstErr error
	)
	for name, c := range cmds {
		name, c := name, c
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := execNamed(ctx, execer, name, c, rt, opts, &outputMu)
			if err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return firstErr
}

func execNamed(ctx context.Context, execer Execer, name string, cmd spec.Command, rt Runtime, opts Options, outputMu *sync.Mutex) error {
	var argv []string
	switch {
	case cmd.Shell != "":
		argv = []string{"/bin/sh", "-c", cmd.Shell}
	case len(cmd.Args) > 0:
		argv = cmd.Args
	default:
		return nil
	}
	// Capture output per-command to avoid interleaving, then flush
	// under a banner. Mirrors upstream's printMode='off' behavior.
	res, err := execer.Exec(ctx, ExecRequest{
		Cmd:     argv,
		User:    rt.User,
		WorkDir: rt.Workdir,
		Env:     rt.Env,
	})
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	outputMu.Lock()
	defer outputMu.Unlock()
	if opts.Stdout != nil {
		fmt.Fprintf(opts.Stdout, "\n--- %s ---\n%s", name, res.Stdout)
	}
	if opts.Stderr != nil && len(res.Stderr) > 0 {
		fmt.Fprintf(opts.Stderr, "\n--- %s (stderr) ---\n%s", name, res.Stderr)
	}
	if res.ExitCode != 0 {
		return formatExecError("parallel command "+name, res.ExitCode, string(res.Stderr))
	}
	return nil
}

// formatExecError shapes a non-zero exit code into a Go error with the
// captured stderr inline.
func formatExecError(label string, exitCode int, stderr string) error {
	if stderr == "" {
		return fmt.Errorf("%s: exit %d", label, exitCode)
	}
	return fmt.Errorf("%s: exit %d: %s", label, exitCode, strings.TrimSpace(stderr))
}
