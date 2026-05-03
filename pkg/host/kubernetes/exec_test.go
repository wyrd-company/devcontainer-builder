package kubernetes

import (
	"strings"
	"testing"
)

func TestWrapExecCommand_PassThroughWhenNoExtras(t *testing.T) {
	in := []string{"/bin/ls", "-la"}
	got := wrapExecCommand(in, "", "", nil)
	if len(got) != len(in) || got[0] != in[0] || got[1] != in[1] {
		t.Errorf("pass-through expected, got %q", got)
	}
}

func TestWrapExecCommand_WorkdirOnly(t *testing.T) {
	got := wrapExecCommand([]string{"/bin/sh", "-c", "echo hi"}, "", "/workspaces/proj", nil)
	if len(got) != 3 || got[0] != "/bin/sh" || got[1] != "-c" {
		t.Fatalf("expected [/bin/sh -c <script>], got %q", got)
	}
	if !strings.Contains(got[2], "cd '/workspaces/proj' &&") {
		t.Errorf("script missing cd prefix: %q", got[2])
	}
	if !strings.Contains(got[2], "exec '/bin/sh' '-c' 'echo hi'") {
		t.Errorf("script missing exec call: %q", got[2])
	}
}

func TestWrapExecCommand_EnvOnly(t *testing.T) {
	got := wrapExecCommand([]string{"node", "app.js"}, "", "", map[string]string{"FOO": "bar"})
	if !strings.Contains(got[2], "env 'FOO=bar' exec 'node' 'app.js'") {
		t.Errorf("script missing env preamble: %q", got[2])
	}
}

func TestWrapExecCommand_UserWrapsWithSu(t *testing.T) {
	got := wrapExecCommand([]string{"/bin/echo", "hi"}, "vscode", "", nil)
	if !strings.Contains(got[2], "su -s /bin/sh -c") {
		t.Errorf("expected su wrapper, got %q", got[2])
	}
	if !strings.Contains(got[2], "'vscode'") {
		t.Errorf("expected user as literal, got %q", got[2])
	}
}

// Single quotes in user-supplied strings must survive the
// '\''-style escape unchanged.
func TestWrapExecCommand_QuotesEscape(t *testing.T) {
	got := wrapExecCommand([]string{"echo", "it's fine"}, "", "/tmp", nil)
	if !strings.Contains(got[2], `'it'\''s fine'`) {
		t.Errorf("escape mishandled: %q", got[2])
	}
}

func TestExitCodeFromError(t *testing.T) {
	if _, ok := exitCodeFromError(errStringT("plain")); ok {
		t.Error("plain error should not yield a code")
	}
	if code, ok := exitCodeFromError(codeErr{code: 42}); !ok || code != 42 {
		t.Errorf("codeErr -> (%d, %v), want (42, true)", code, ok)
	}
}

type errStringT string

func (e errStringT) Error() string { return string(e) }

type codeErr struct{ code int }

func (c codeErr) Error() string    { return "exit code" }
func (c codeErr) ExitStatus() int  { return c.code }
