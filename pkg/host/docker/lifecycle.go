package docker

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/wyrd-company/devcontainer-builder/pkg/lifecycle"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// markerCheckScript is a shell one-liner that mirrors upstream's
// updateMarkerFile in injectHeadless.ts. It atomically:
//   - ensures the marker's parent directory exists,
//   - reads the current marker contents (or "ENOENT"),
//   - if they differ from the expected content, writes the expected
//     content and exits 0 (=> doRun=true),
//   - otherwise exits non-zero (=> doRun=false).
//
// The shell quoting follows upstream verbatim — both create the marker
// and decide the rerun bit in a single call so there's no TOCTOU window
// between probe and write.
const markerCheckScript = `mkdir -p '%s' && CONTENT="$(cat '%s' 2>/dev/null || echo ENOENT)" && [ "${CONTENT:-%s}" != '%s' ] && echo '%s' > '%s'`

// shouldRunMarker returns true if the phase needs to run, having
// updated the marker file as a side-effect when so. WhenAttach phases
// short-circuit to true (no marker, always run).
func (h *Host) shouldRunMarker(
	ctx context.Context, containerID, user, markerPath, expected string,
) (bool, error) {
	if markerPath == "" {
		return true, nil
	}
	dir := markerPath[:strings.LastIndexByte(markerPath, '/')]
	if dir == "" {
		dir = "/"
	}
	expectedQ := shellSQ(expected)
	script := fmt.Sprintf(markerCheckScript,
		shellSQRaw(dir), shellSQRaw(markerPath),
		expectedQ,
		expectedQ,
		expectedQ, shellSQRaw(markerPath))
	exit, _, err := h.execShellQuiet(ctx, containerID, user, script)
	if err != nil {
		return false, err
	}
	// exit 0 means "marker was stale / missing, now updated" => run.
	return exit == 0, nil
}

// shellSQ wraps a string in single quotes, escaping any embedded
// single-quote with the standard '\'' construction. Use shellSQRaw for
// values that go between literal single quotes in the script template
// (no extra outer quotes).
func shellSQ(s string) string {
	return "'" + shellSQRaw(s) + "'"
}

func shellSQRaw(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}

// runPhase executes one Phase against the container, observing markers.
// onceContent is the marker payload for WhenOnce phases (createdAt);
// startContent is the payload for WhenStart (startedAt).
type phaseRuntime struct {
	containerID    string
	user           string
	workdir        string
	env            map[string]string
	createdAt      string
	startedAt      string
	userDataFolder string
}

func (h *Host) runPhase(ctx context.Context, p lifecycle.Phase, rt phaseRuntime) error {
	expected := ""
	switch p.When {
	case lifecycle.WhenOnce:
		expected = rt.createdAt
	case lifecycle.WhenStart:
		expected = rt.startedAt
	}
	markerPath := p.MarkerPath(rt.userDataFolder)
	doRun, err := h.shouldRunMarker(ctx, rt.containerID, rt.user, markerPath, expected)
	if err != nil {
		return errorf("marker check (%s): %w", p.Hook, err)
	}
	if !doRun {
		h.logf("skipping %s (marker up to date) [%s]", p.Hook, p.Origin.Source)
		return nil
	}
	h.logf("running %s [%s]", p.Hook, p.Origin.Source)
	return h.execCommand(ctx, p.Command, rt)
}

// execCommand executes a spec.Command in a container, dispatching on
// the command form: shell, args, or parallel-named.
func (h *Host) execCommand(ctx context.Context, cmd spec.Command, rt phaseRuntime) error {
	switch {
	case cmd.Shell != "":
		return h.execOne(ctx, []string{"/bin/sh", "-c", cmd.Shell}, rt, "")
	case len(cmd.Args) > 0:
		return h.execOne(ctx, cmd.Args, rt, "")
	case len(cmd.Parallel) > 0:
		return h.execParallel(ctx, cmd.Parallel, rt)
	}
	return nil
}

func (h *Host) execOne(ctx context.Context, argv []string, rt phaseRuntime, name string) error {
	res, err := h.Exec(ctx, ExecRequest{
		ContainerID: rt.containerID,
		Cmd:         argv,
		User:        rt.user,
		WorkDir:     rt.workdir,
		Env:         rt.env,
		Stdout:      h.opts.Stdout,
		Stderr:      h.opts.Stderr,
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
func (h *Host) execParallel(ctx context.Context, cmds map[string]spec.Command, rt phaseRuntime) error {
	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		firstErr error
	)
	for name, c := range cmds {
		name, c := name, c
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := h.execNamed(ctx, name, c, rt)
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

func (h *Host) execNamed(ctx context.Context, name string, cmd spec.Command, rt phaseRuntime) error {
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
	res, err := h.Exec(ctx, ExecRequest{
		ContainerID: rt.containerID,
		Cmd:         argv,
		User:        rt.user,
		WorkDir:     rt.workdir,
		Env:         rt.env,
	})
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	if h.opts.Stdout != nil {
		fmt.Fprintf(h.opts.Stdout, "\n--- %s ---\n%s", name, res.Stdout)
	}
	if h.opts.Stderr != nil && len(res.Stderr) > 0 {
		fmt.Fprintf(h.opts.Stderr, "\n--- %s (stderr) ---\n%s", name, res.Stderr)
	}
	if res.ExitCode != 0 {
		return formatExecError("parallel command "+name, res.ExitCode, string(res.Stderr))
	}
	return nil
}
