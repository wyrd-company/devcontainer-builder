// relationships:
//
//	references: [phase, runner, probes]
package runner

import (
	"context"
	"io"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/lifecycle"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestTerminalLifecycleRequests(t *testing.T) {
	for _, command := range []spec.Command{
		{Shell: "printf sample"},
		{Parallel: map[string]spec.Command{"sample": {Args: []string{"printf", "sample"}}}},
	} {
		size := &[2]uint{37, 91}
		f := &fakeExecer{}
		plan := []lifecycle.Phase{{Hook: lifecycle.HookPostAttach, When: lifecycle.WhenAttach, Command: command}}
		if err := Run(context.Background(), f, plan, Runtime{}, Options{TerminalSize: size, Stdout: io.Discard}); err != nil {
			t.Fatal(err)
		}
		if len(f.calls) != 1 || f.calls[0].TerminalSize == nil || *f.calls[0].TerminalSize != *size {
			t.Fatalf("lifecycle requests=%+v", f.calls)
		}
		f.calls = nil
		_, _ = DetectContainerEnv(context.Background(), f)
		_, _ = DetectHome(context.Background(), f, "")
		if len(f.calls) == 0 {
			t.Fatal("no probes executed")
		}
		for _, call := range f.calls {
			if call.TerminalSize != nil {
				t.Fatalf("probe has PTY dimensions: %+v", call)
			}
		}
	}
}
