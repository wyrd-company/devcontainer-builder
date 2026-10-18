package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/lifecycle"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// fakeExecer records every Exec call and dispatches to a per-test
// handler. The default handler returns exit 0 with empty output —
// suitable for "phase ran successfully" cases.
type fakeExecer struct {
	mu      sync.Mutex
	calls   []ExecRequest
	handler func(ExecRequest) (*ExecResult, error)
}

func (f *fakeExecer) Exec(_ context.Context, req ExecRequest) (*ExecResult, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	f.mu.Unlock()
	if f.handler != nil {
		return f.handler(req)
	}
	return &ExecResult{ExitCode: 0}, nil
}

func (f *fakeExecer) callsCopy() []ExecRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]ExecRequest, len(f.calls))
	copy(out, f.calls)
	return out
}

// classifyCalls returns the marker checks (one per phase that has a
// marker) and the lifecycle execs (everything else).
func classifyCalls(calls []ExecRequest) (markers, lifecycle []ExecRequest) {
	for _, c := range calls {
		if len(c.Cmd) == 3 && c.Cmd[0] == "/bin/sh" && c.Cmd[1] == "-c" && strings.Contains(c.Cmd[2], "ENOENT") {
			markers = append(markers, c)
			continue
		}
		lifecycle = append(lifecycle, c)
	}
	return markers, lifecycle
}

func TestRun_EmptyPlan(t *testing.T) {
	f := &fakeExecer{}
	if err := Run(context.Background(), f, nil, Runtime{}, Options{}); err != nil {
		t.Errorf("Run(empty) = %v, want nil", err)
	}
	if len(f.calls) != 0 {
		t.Errorf("calls = %d, want 0", len(f.calls))
	}
}

func TestRun_NilExecer(t *testing.T) {
	if err := Run(context.Background(), nil, nil, Runtime{}, Options{}); err == nil {
		t.Error("Run(nil execer) = nil, want error")
	}
}

func TestRun_WhenAttachAlwaysRunsNoMarker(t *testing.T) {
	plan := []lifecycle.Phase{{
		Hook:    lifecycle.HookPostAttach,
		When:    lifecycle.WhenAttach,
		Origin:  lifecycle.Origin{Source: "devcontainer.json"},
		Command: spec.Command{Shell: "echo attached"},
	}}
	f := &fakeExecer{}
	if err := Run(context.Background(), f, plan, Runtime{UserDataFolder: "/root/.devcontainer"}, Options{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	markers, runs := classifyCalls(f.callsCopy())
	if len(markers) != 0 {
		t.Errorf("attach phase should not perform a marker check, got %d", len(markers))
	}
	if len(runs) != 1 || runs[0].Cmd[2] != "echo attached" {
		t.Errorf("expected one shell invocation, got %+v", runs)
	}
}

func TestRun_WhenOnce_MarkerMissing_RunsAndWritesMarker(t *testing.T) {
	plan := []lifecycle.Phase{{
		Hook:    lifecycle.HookPostCreate,
		When:    lifecycle.WhenOnce,
		Origin:  lifecycle.Origin{Source: "devcontainer.json"},
		Command: spec.Command{Shell: "echo create"},
	}}
	f := &fakeExecer{}
	rt := Runtime{
		User:           "vscode",
		UserDataFolder: "/home/vscode/.devcontainer",
		CreatedAt:      "2026-01-01T00:00:00Z",
	}
	if err := Run(context.Background(), f, plan, rt, Options{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	markers, runs := classifyCalls(f.callsCopy())
	if len(markers) != 1 {
		t.Fatalf("expected 1 marker check, got %d", len(markers))
	}
	if !strings.Contains(markers[0].Cmd[2], "/home/vscode/.devcontainer/.postCreateCommandMarker") {
		t.Errorf("marker path missing from script: %q", markers[0].Cmd[2])
	}
	if !strings.Contains(markers[0].Cmd[2], "2026-01-01T00:00:00Z") {
		t.Errorf("createdAt missing from marker payload: %q", markers[0].Cmd[2])
	}
	if len(runs) != 1 {
		t.Errorf("expected lifecycle run, got %+v", runs)
	}
}

func TestRun_WhenOnce_MarkerUpToDate_Skips(t *testing.T) {
	plan := []lifecycle.Phase{{
		Hook:    lifecycle.HookPostCreate,
		When:    lifecycle.WhenOnce,
		Origin:  lifecycle.Origin{Source: "devcontainer.json"},
		Command: spec.Command{Shell: "echo create"},
	}}
	// Marker check exits non-zero (matches), so the phase should skip.
	f := &fakeExecer{
		handler: func(req ExecRequest) (*ExecResult, error) {
			if strings.Contains(req.Cmd[2], "ENOENT") {
				// "marker matches expected" => mkdir succeeds, the [ != ]
				// check is false, the script exits 1.
				return &ExecResult{ExitCode: 1}, nil
			}
			return &ExecResult{ExitCode: 0}, nil
		},
	}
	logs := []string{}
	if err := Run(context.Background(), f, plan, Runtime{UserDataFolder: "/root/.devcontainer", CreatedAt: "x"}, Options{
		Logger: func(format string, args ...any) {
			logs = append(logs, fmt.Sprintf(format, args...))
		},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	_, runs := classifyCalls(f.callsCopy())
	if len(runs) != 0 {
		t.Errorf("expected no lifecycle run when marker is up to date, got %+v", runs)
	}
	var sawSkip bool
	for _, l := range logs {
		if strings.Contains(l, "skipping") {
			sawSkip = true
		}
	}
	if !sawSkip {
		t.Errorf("expected a skip log line, got %v", logs)
	}
}

func TestRunWithResult_ReportsOnlyExecutedPhases(t *testing.T) {
	plan := []lifecycle.Phase{
		{
			Hook:    lifecycle.HookPostCreate,
			When:    lifecycle.WhenOnce,
			Origin:  lifecycle.Origin{Source: "devcontainer.json"},
			Command: spec.Command{Shell: "echo skipped"},
		},
		{
			Hook:    lifecycle.HookPostAttach,
			When:    lifecycle.WhenAttach,
			Origin:  lifecycle.Origin{Source: "devcontainer.json"},
			Command: spec.Command{Shell: "echo ran"},
		},
	}
	f := &fakeExecer{
		handler: func(req ExecRequest) (*ExecResult, error) {
			if strings.Contains(req.Cmd[2], "ENOENT") {
				return &ExecResult{ExitCode: 1}, nil
			}
			return &ExecResult{ExitCode: 0}, nil
		},
	}
	got, err := RunWithResult(context.Background(), f, plan, Runtime{UserDataFolder: "/root/.devcontainer", CreatedAt: "x"}, Options{})
	if err != nil {
		t.Fatalf("RunWithResult: %v", err)
	}
	ran := got.RanPhases()
	if len(ran) != 1 || ran[0].Hook != lifecycle.HookPostAttach {
		t.Fatalf("RanPhases = %+v, want only postAttach", ran)
	}
}

func TestRun_ShellVsArgsDispatch(t *testing.T) {
	plan := []lifecycle.Phase{
		{
			Hook:    lifecycle.HookOnCreate,
			When:    lifecycle.WhenOnce,
			Command: spec.Command{Shell: "npm install"},
		},
		{
			Hook:    lifecycle.HookOnCreate,
			When:    lifecycle.WhenOnce,
			Command: spec.Command{Args: []string{"npm", "audit"}},
		},
	}
	f := &fakeExecer{}
	if err := Run(context.Background(), f, plan, Runtime{UserDataFolder: "/root/.devcontainer", CreatedAt: "x"}, Options{}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	_, runs := classifyCalls(f.callsCopy())
	if len(runs) != 2 {
		t.Fatalf("expected 2 lifecycle runs, got %+v", runs)
	}
	// Shell form goes via /bin/sh -c.
	if got := runs[0].Cmd; len(got) != 3 || got[0] != "/bin/sh" || got[2] != "npm install" {
		t.Errorf("shell dispatch = %v", got)
	}
	// Args form is exec'd directly.
	if got := runs[1].Cmd; len(got) != 2 || got[0] != "npm" || got[1] != "audit" {
		t.Errorf("args dispatch = %v", got)
	}
}

func TestRun_Parallel(t *testing.T) {
	plan := []lifecycle.Phase{{
		Hook: lifecycle.HookPostStart,
		When: lifecycle.WhenStart,
		Command: spec.Command{
			Parallel: map[string]spec.Command{
				"server": {Shell: "npm start"},
				"watch":  {Args: []string{"npm", "watch"}},
			},
		},
	}}
	f := &fakeExecer{
		handler: func(req ExecRequest) (*ExecResult, error) {
			// Parallel commands run with captured stdout (no Stdout
			// writer on the request); echo a name into Stdout so the
			// runner's banner has something to print.
			if len(req.Cmd) >= 1 && req.Cmd[0] == "/bin/sh" && strings.Contains(req.Cmd[2], "ENOENT") {
				return &ExecResult{ExitCode: 0}, nil
			}
			return &ExecResult{ExitCode: 0, Stdout: []byte("ok\n")}, nil
		},
	}
	var stdout bytes.Buffer
	if err := Run(context.Background(), f, plan, Runtime{UserDataFolder: "/root/.devcontainer", StartedAt: "y"}, Options{
		Stdout: &stdout,
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	_, runs := classifyCalls(f.callsCopy())
	if len(runs) != 2 {
		t.Errorf("expected 2 parallel runs, got %d", len(runs))
	}
	out := stdout.String()
	if !strings.Contains(out, "--- server ---") || !strings.Contains(out, "--- watch ---") {
		t.Errorf("expected per-command banners in stdout, got %q", out)
	}
}

func TestRun_ParallelFirstErrorSurfaces(t *testing.T) {
	plan := []lifecycle.Phase{{
		Hook: lifecycle.HookPostStart,
		When: lifecycle.WhenStart,
		Command: spec.Command{
			Parallel: map[string]spec.Command{
				"good": {Shell: "true"},
				"bad":  {Shell: "false"},
			},
		},
	}}
	f := &fakeExecer{
		handler: func(req ExecRequest) (*ExecResult, error) {
			if len(req.Cmd) == 3 && strings.Contains(req.Cmd[2], "ENOENT") {
				return &ExecResult{ExitCode: 0}, nil
			}
			if strings.Contains(req.Cmd[2], "false") {
				return &ExecResult{ExitCode: 7, Stderr: []byte("nope")}, nil
			}
			return &ExecResult{ExitCode: 0}, nil
		},
	}
	err := Run(context.Background(), f, plan, Runtime{UserDataFolder: "/root/.devcontainer", StartedAt: "y"}, Options{})
	if err == nil {
		t.Fatal("expected error from parallel command")
	}
	if !strings.Contains(err.Error(), "postStartCommand") || !strings.Contains(err.Error(), "exit 7") {
		t.Errorf("error = %q, want hook + exit code", err)
	}
}

func TestRun_WrapsErrorWithHookAndOrigin(t *testing.T) {
	plan := []lifecycle.Phase{{
		Hook:    lifecycle.HookOnCreate,
		When:    lifecycle.WhenOnce,
		Origin:  lifecycle.Origin{Source: "ghcr.io/example/node:1"},
		Command: spec.Command{Shell: "exit 5"},
	}}
	f := &fakeExecer{
		handler: func(req ExecRequest) (*ExecResult, error) {
			if strings.Contains(req.Cmd[2], "ENOENT") {
				return &ExecResult{ExitCode: 0}, nil
			}
			return &ExecResult{ExitCode: 5}, nil
		},
	}
	err := Run(context.Background(), f, plan, Runtime{UserDataFolder: "/root/.devcontainer", CreatedAt: "x"}, Options{})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "onCreateCommand") {
		t.Errorf("error %q should contain hook name", err)
	}
	if !strings.Contains(err.Error(), "ghcr.io/example/node:1") {
		t.Errorf("error %q should contain origin", err)
	}
}

func TestRun_TransportErrorPropagates(t *testing.T) {
	plan := []lifecycle.Phase{{
		Hook:    lifecycle.HookOnCreate,
		When:    lifecycle.WhenAttach,
		Command: spec.Command{Shell: "echo ok"},
	}}
	want := errors.New("transport boom")
	f := &fakeExecer{handler: func(ExecRequest) (*ExecResult, error) { return nil, want }}
	err := Run(context.Background(), f, plan, Runtime{}, Options{})
	if err == nil || !errors.Is(err, want) {
		t.Errorf("Run = %v, want wrapping of transport error", err)
	}
}

func TestDetectHome_RootShortCircuits(t *testing.T) {
	f := &fakeExecer{}
	got, err := DetectHome(context.Background(), f, "root")
	if err != nil {
		t.Fatalf("DetectHome: %v", err)
	}
	if got != "/root" {
		t.Errorf("got %q, want /root", got)
	}
	if len(f.calls) != 0 {
		t.Errorf("expected no exec calls for root user, got %d", len(f.calls))
	}
}

func TestDetectHome_FallbackOnMissingUser(t *testing.T) {
	f := &fakeExecer{
		handler: func(ExecRequest) (*ExecResult, error) {
			return &ExecResult{ExitCode: 0, Stdout: []byte("")}, nil
		},
	}
	got, err := DetectHome(context.Background(), f, "alice")
	if err != nil {
		t.Fatalf("DetectHome: %v", err)
	}
	if got != "/home/alice" {
		t.Errorf("got %q, want /home/alice", got)
	}
}

func TestDetectHome_ParsesGetentOutput(t *testing.T) {
	f := &fakeExecer{
		handler: func(ExecRequest) (*ExecResult, error) {
			return &ExecResult{ExitCode: 0, Stdout: []byte("/srv/alice\n")}, nil
		},
	}
	got, err := DetectHome(context.Background(), f, "alice")
	if err != nil {
		t.Fatalf("DetectHome: %v", err)
	}
	if got != "/srv/alice" {
		t.Errorf("got %q, want /srv/alice", got)
	}
}

func TestDetectContainerEnv(t *testing.T) {
	f := &fakeExecer{
		handler: func(ExecRequest) (*ExecResult, error) {
			// /proc/<pid>/environ is NUL-separated KEY=VALUE pairs.
			payload := []byte("FOO=bar\x00BAZ=qux\x00")
			return &ExecResult{ExitCode: 0, Stdout: payload}, nil
		},
	}
	env, err := DetectContainerEnv(context.Background(), f)
	if err != nil {
		t.Fatalf("DetectContainerEnv: %v", err)
	}
	if env["FOO"] != "bar" || env["BAZ"] != "qux" {
		t.Errorf("env = %+v, want FOO=bar BAZ=qux", env)
	}
}

// markerFileExecer emulates the marker script against an in-memory
// marker store, so a marker written by one check is seen by the next.
func markerFileExecer() *fakeExecer {
	var mu sync.Mutex
	written := map[string]bool{}
	return &fakeExecer{handler: func(req ExecRequest) (*ExecResult, error) {
		if len(req.Cmd) == 3 && strings.Contains(req.Cmd[2], "ENOENT") {
			mu.Lock()
			defer mu.Unlock()
			if written[req.Cmd[2]] {
				return &ExecResult{ExitCode: 1}, nil
			}
			written[req.Cmd[2]] = true
		}
		return &ExecResult{ExitCode: 0}, nil
	}}
}

// Upstream checks a hook's marker once and then runs every command of
// that hook (features in install order, then devcontainer.json).
func TestRun_HookMarkerCoversAllCommandsOfTheHook(t *testing.T) {
	plan := []lifecycle.Phase{
		{Hook: lifecycle.HookPostCreate, When: lifecycle.WhenOnce, Origin: lifecycle.Origin{Source: "./feature"}, Command: spec.Command{Shell: "echo feature"}},
		{Hook: lifecycle.HookPostCreate, When: lifecycle.WhenOnce, Origin: lifecycle.Origin{Source: "devcontainer.json"}, Command: spec.Command{Shell: "echo spec"}},
		{Hook: lifecycle.HookPostStart, When: lifecycle.WhenStart, Origin: lifecycle.Origin{Source: "devcontainer.json"}, Command: spec.Command{Shell: "echo start"}},
	}
	rt := Runtime{UserDataFolder: "/home/user/.devcontainer", CreatedAt: "c1", StartedAt: "s1"}
	f := markerFileExecer()
	res, err := RunWithResult(context.Background(), f, plan, rt, Options{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	markers, cmds := classifyCalls(f.callsCopy())
	if len(markers) != 2 {
		t.Errorf("marker checks = %d, want one per hook (2)", len(markers))
	}
	var shells []string
	for _, c := range cmds {
		shells = append(shells, c.Cmd[2])
	}
	if got := strings.Join(shells, ","); got != "echo feature,echo spec,echo start" {
		t.Fatalf("ran %s, want every command of each hook", got)
	}
	if len(res.RanPhases()) != 3 {
		t.Fatalf("ran phases = %d, want 3", len(res.RanPhases()))
	}

	// A second run in the same container lifetime skips the whole hook.
	res, err = RunWithResult(context.Background(), f, plan, rt, Options{})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if len(res.RanPhases()) != 0 {
		t.Fatalf("second run ran %d phases, want 0", len(res.RanPhases()))
	}
}
