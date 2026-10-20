// relationships:
//
//	references: [system_env]
package runner

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestSystemEnvironmentPatchOrder(t *testing.T) {
	var calls []ExecRequest
	var input string
	f := probeExecer(func(_ context.Context, r ExecRequest) (*ExecResult, error) {
		calls = append(calls, r)
		if r.Stdin != nil {
			b, err := io.ReadAll(r.Stdin)
			if err != nil {
				t.Fatal(err)
			}
			input = string(b)
		}
		return &ExecResult{}, nil
	})
	if err := PatchSystemEnv(context.Background(), f, "/tmp/sample system", map[string]string{"SAMPLE": "value", "PATH": "/bin"}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("patches=%d", len(calls))
	}
	for i, name := range []string{"patchEtcEnvironment", "patchEtcProfile"} {
		if calls[i].User != "root" || !strings.Contains(calls[i].Cmd[2], "/tmp/sample system/."+name+"Marker") {
			t.Fatalf("patch request: %+v", calls[i])
		}
	}
	if input != "\nPATH=\"/bin\"\nSAMPLE=\"value\"\n" {
		t.Fatalf("environment data=%q", input)
	}
}

func TestSystemEnvironmentPatchError(t *testing.T) {
	for _, transport := range []bool{false, true} {
		calls := 0
		f := probeExecer(func(context.Context, ExecRequest) (*ExecResult, error) {
			calls++
			if transport {
				return nil, errors.New("sample error")
			}
			return &ExecResult{ExitCode: 23}, nil
		})
		if err := PatchSystemEnv(context.Background(), f, "", nil); err == nil || !strings.Contains(err.Error(), "patchEtcEnvironment") {
			t.Fatalf("error=%v", err)
		}
		if calls != 1 {
			t.Fatalf("continued after failed environment edit: %d", calls)
		}
	}
}
