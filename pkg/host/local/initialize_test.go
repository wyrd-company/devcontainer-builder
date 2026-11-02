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
		{"argv-zero", `["sh","-c","printf %s \"$0\""]`, "sh"},
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
	if err := os.WriteFile(filepath.Join(dir, "script"), []byte("#!/bin/sh\nprintf x >> \"$0.marker\"\nprintf '%s' \"$PWD\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		var output bytes.Buffer
		err := Initialize(context.Background(), spec.Command{Args: []string{"./script"}}, InitializeOptions{Workspace: dir, Output: &output})
		if err != nil || output.String() != dir {
			t.Fatalf("cwd=%q err=%v", output.String(), err)
		}
	}
	data, err := os.ReadFile(filepath.Join(dir, "script.marker"))
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

func TestInitializeParallelInterruptReturnsBeforeSibling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX command fixtures")
	}
	dir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var output lockedBuffer
	err := Initialize(ctx, spec.Command{Parallel: map[string]spec.Command{"interrupt": {Shell: "exit 130"}, "continue": {Shell: "while [ ! -f release ]; do sleep 0.01; done; printf completed"}}}, InitializeOptions{Workspace: dir, Output: &output})
	if err != nil || ctx.Err() != nil {
		t.Fatalf("interrupt did not resolve promptly: %v %v", err, ctx.Err())
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

func TestInitializeWorkspacePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixtures")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "sample-command"), []byte("#!/bin/sh\nprintf workspace-command"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path string
		inherit    bool
	}{
		{"inherited-relative", "bin", true}, {"explicit-relative", "bin", false}, {"explicit-absolute", bin, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var env []string
			if tc.inherit {
				t.Setenv("PATH", tc.path)
			} else {
				env = []string{"PATH=" + tc.path}
			}
			var output bytes.Buffer
			err := Initialize(context.Background(), spec.Command{Args: []string{"sample-command"}}, InitializeOptions{Workspace: dir, Env: env, Output: &output})
			if err != nil || output.String() != "workspace-command" {
				t.Fatalf("output=%q err=%v", output.String(), err)
			}
		})
	}
	// An explicitly empty PATH searches the workspace, unlike an absent PATH.
	if err := os.WriteFile(filepath.Join(dir, "sample-command"), []byte("#!/bin/sh\nprintf cwd-command"), 0700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := Initialize(context.Background(), spec.Command{Args: []string{"sample-command"}}, InitializeOptions{Workspace: dir, Env: []string{"PATH="}, Output: &output}); err != nil || output.String() != "cwd-command" {
		t.Fatalf("empty PATH: %q %v", output.String(), err)
	}
}

func TestInitializeExecutableScriptFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX execvp fixtures")
	}
	for _, tc := range []struct {
		name, body string
		mode       os.FileMode
		want       string
		fail       bool
	}{
		{"text", "printf '%s|%s|%s' \"$0\" \"$1\" \"$SAMPLE_VALUE\"", 0700, "./sample-command|argument with spaces|host-value", false},
		{"shebang", "#!/bin/sh\nprintf '%s|%s|%s' \"$0\" \"$1\" \"$SAMPLE_VALUE\"", 0700, "./sample-command|argument with spaces|host-value", false},
		{"not-executable", "printf forbidden", 0600, "", true},
		{"text-error", "exit 7", 0700, "", true},
		{"text-interrupt", "exit 130", 0700, "initializeCommand interrupted.\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "sample-command"), []byte(tc.body), tc.mode); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			err := Initialize(context.Background(), spec.Command{Args: []string{"./sample-command", "argument with spaces"}}, InitializeOptions{Workspace: dir, Env: []string{"SAMPLE_VALUE=host-value"}, Output: &output})
			if (err != nil) != tc.fail || output.String() != tc.want {
				t.Fatalf("output=%q err=%v want=%q fail=%v", output.String(), err, tc.want, tc.fail)
			}
		})
	}
}

func TestInitializePathComponents(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink and script-name fixtures")
	}
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "actual", "child"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("actual/child", filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	for _, file := range []struct{ path, body string }{
		{filepath.Join(dir, "sample-command"), "printf wrong-program"},
		{filepath.Join(dir, "actual", "sample-command"), "printf right-program"},
	} {
		if err := os.WriteFile(file.path, []byte(file.body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"linked/..", dir + "/linked/.."} {
		var output bytes.Buffer
		err := Initialize(context.Background(), spec.Command{Args: []string{"sample-command"}}, InitializeOptions{Workspace: dir, Env: []string{"PATH=" + path}, Output: &output})
		if err != nil || output.String() != "right-program" {
			t.Fatalf("PATH=%q output=%q err=%v", path, output.String(), err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"sample-command", "bin/sample-command"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("printf '%s' \"$0\""), 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{".", "./bin", "./bin/"} {
		var output bytes.Buffer
		err := Initialize(context.Background(), spec.Command{Args: []string{"sample-command"}}, InitializeOptions{Workspace: dir, Env: []string{"PATH=" + path}, Output: &output})
		want := path + "/sample-command"
		if err != nil || output.String() != want {
			t.Fatalf("PATH=%q output=%q want=%q err=%v", path, output.String(), want, err)
		}
	}
}

func TestInitializePathSearchErrors(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX execvp fixtures")
	}
	for _, tc := range []struct {
		name, body string
		mode       os.FileMode
		want       string
		fail       bool
	}{
		{"missing-interpreter", "#!/nonexistent/sample-interpreter\n", 0700, "second", false},
		{"permission-denied", "printf forbidden", 0600, "second", false},
		{"text-first", "printf first", 0700, "first", false},
		{"child-failure", "exit 7", 0700, "", true},
		{"child-interrupt", "exit 130", 0700, "initializeCommand interrupted.\n", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, name := range []string{"first", "second"} {
				if err := os.Mkdir(filepath.Join(dir, name), 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(dir, "first/sample-command"), []byte(tc.body), tc.mode); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "second/sample-command"), []byte("#!/bin/sh\nprintf second"), 0700); err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			err := Initialize(context.Background(), spec.Command{Args: []string{"sample-command"}}, InitializeOptions{Workspace: dir, Env: []string{"PATH=missing:first:second"}, Output: &out})
			if (err != nil) != tc.fail || out.String() != tc.want {
				t.Fatalf("output=%q err=%v want=%q fail=%v", out.String(), err, tc.want, tc.fail)
			}
		})
	}
	// Explicit paths do not search PATH, even for a missing interpreter.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "sample-command"), []byte("#!/nonexistent/sample-interpreter\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := Initialize(context.Background(), spec.Command{Args: []string{"./sample-command"}}, InitializeOptions{Workspace: dir}); err == nil {
		t.Fatal("explicit missing interpreter accepted")
	}
	if err := Initialize(context.Background(), spec.Command{Args: []string{"sample-command"}}, InitializeOptions{Workspace: dir, Env: []string{"PATH=.:missing"}}); err == nil {
		t.Fatal("exhausted search accepted")
	}
}

func TestInitializePOSIXBackslashExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable filename")
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "bin"), 0700); err != nil {
		t.Fatal(err)
	}
	executable := `sample\command`
	if err := os.WriteFile(filepath.Join(dir, "bin", executable), []byte("#!/bin/sh\nprintf selected"), 0700); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	err := Initialize(context.Background(), spec.Command{Args: []string{executable}}, InitializeOptions{Workspace: dir, Env: []string{"PATH=bin"}, Output: &output})
	if err != nil || output.String() != "selected" {
		t.Fatalf("output=%q err=%v", output.String(), err)
	}
}
