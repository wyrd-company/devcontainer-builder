// relationships:
//
//	references: [initialize, ../../../reference/cli/src/spec-node/utils]
package local

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

func TestInitializeForms(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command fixtures")
	}
	for _, tc := range []struct{ name, raw, want string }{
		{"shell", `"printf '%s' \"$SAMPLE_VALUE\""`, "host-value"},
		{"argv", `["printf","%s","$SAMPLE_VALUE"]`, "$SAMPLE_VALUE"},
		{"substitution", `["printf","%s","${localEnv:SAMPLE_VALUE}"]`, "host-value"},
		{"empty", `[]`, ""}, {"whitespace", `"  "`, ""}, {"parallel-empty", `{"a":[],"b":""}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var command spec.Command
			if err := json.Unmarshal([]byte(tc.raw), &command); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			err := Initialize(context.Background(), command, InitializeOptions{Workspace: t.TempDir(), Env: []string{"SAMPLE_VALUE=host-value"}, Output: &output, Substitution: substitute.Context{LocalEnv: map[string]string{"SAMPLE_VALUE": "host-value"}}})
			if err != nil || output.String() != tc.want {
				t.Fatalf("output=%q err=%v want=%q", output.String(), err, tc.want)
			}
		})
	}
}

func TestInitializeWorkspaceAndRepeatedInvocation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command fixtures")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "script"), []byte("#!/bin/sh\nprintf x >> marker\nprintf '%s' \"$PWD\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		var output bytes.Buffer
		err := Initialize(context.Background(), spec.Command{Args: []string{"./script"}}, InitializeOptions{Workspace: dir, Output: &output})
		if err != nil || output.String() != dir {
			t.Fatalf("cwd=%q err=%v", output.String(), err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "marker"))
	if err != nil || string(data) != "xx" {
		t.Fatalf("marker=%q err=%v", data, err)
	}
}

func TestInitializeParallel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command fixtures")
	}
	// Each child requires a marker written by the other. Serial execution fails.
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	command := spec.Command{Parallel: map[string]spec.Command{
		"first":  {Shell: "touch first; while [ ! -f second ]; do sleep 0.01; done; printf first"},
		"second": {Shell: "touch second; while [ ! -f first ]; do sleep 0.01; done; printf second"},
	}}
	var output bytes.Buffer
	if err := Initialize(ctx, command, InitializeOptions{Workspace: dir, Output: &output}); err != nil {
		t.Fatal(err)
	}
	if output.String() != "firstsecond" && output.String() != "secondfirst" {
		t.Fatalf("parallel output=%q", output.String())
	}
}

func TestInitializeErrorsAndInterrupt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command fixtures")
	}
	for _, tc := range []struct {
		shell string
		fail  bool
	}{{"exit 7", true}, {"kill -TERM $$", true}, {"exit 130", false}, {"kill -INT $$", false}} {
		err := Initialize(context.Background(), spec.Command{Shell: tc.shell}, InitializeOptions{Workspace: t.TempDir()})
		if (err != nil) != tc.fail {
			t.Fatalf("%s: %v", tc.shell, err)
		}
	}
	err := Initialize(context.Background(), spec.Command{Args: []string{"/nonexistent/sample-program"}}, InitializeOptions{Workspace: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "initializeCommand") {
		t.Fatal(err)
	}
}

// Output can outlive the failing sibling, matching upstream Promise.all.
func TestInitializeParallelFailureDoesNotCancelSibling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command fixtures")
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var output lockedBuffer
	err := Initialize(ctx, spec.Command{Parallel: map[string]spec.Command{"fail": {Shell: "exit 7"}, "continue": {Shell: "while [ ! -f release ]; do sleep 0.01; done; printf completed"}}}, InitializeOptions{Workspace: dir, Output: &output})
	if err == nil {
		t.Fatal("expected failure")
	}
	if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for !strings.Contains(output.String(), "completed") {
		select {
		case <-ctx.Done():
			t.Fatal("sibling cancelled")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

type lockedBuffer struct {
	sync.Mutex
	b bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) { b.Lock(); defer b.Unlock(); return b.b.Write(p) }
func (b *lockedBuffer) String() string              { b.Lock(); defer b.Unlock(); return b.b.String() }
