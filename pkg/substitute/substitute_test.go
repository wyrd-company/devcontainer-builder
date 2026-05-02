package substitute

import (
	"testing"
)

func TestSubstitute_LocalEnv(t *testing.T) {
	ctx := Context{LocalEnv: map[string]string{"FOO": "bar"}}
	cases := []struct {
		in, want string
	}{
		{"${env:FOO}", "bar"},
		{"${localEnv:FOO}", "bar"},
		{"${env:MISSING}", ""},
		{"${env:MISSING:fallback}", "fallback"},
		{"${env:MISSING:with:colons}", "with"}, // mirrors upstream: takes first arg-after-name
		{"plain", "plain"},
		{"prefix-${env:FOO}-suffix", "prefix-bar-suffix"},
	}
	for _, c := range cases {
		got := Substitute(ctx, c.in)
		if got != c.want {
			t.Errorf("Substitute(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSubstitute_Workspace(t *testing.T) {
	ctx := Context{
		Platform:                 "linux",
		LocalWorkspaceFolder:     "/home/u/proj",
		ContainerWorkspaceFolder: "/workspaces/proj",
	}
	cases := []struct {
		in, want string
	}{
		{"${localWorkspaceFolder}", "/home/u/proj"},
		{"${localWorkspaceFolderBasename}", "proj"},
		{"${containerWorkspaceFolder}", "/workspaces/proj"},
		{"${containerWorkspaceFolderBasename}", "proj"},
	}
	for _, c := range cases {
		if got := Substitute(ctx, c.in); got != c.want {
			t.Errorf("Substitute(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSubstitute_UnresolvedLeftIntact(t *testing.T) {
	// Empty context — no values to substitute. Variables should pass
	// through unchanged so the caller can surface a clear error.
	ctx := Context{}
	in := "${localWorkspaceFolder}/${containerWorkspaceFolder}"
	if got := Substitute(ctx, in); got != in {
		t.Errorf("expected unresolved match to pass through, got %q", got)
	}
}

func TestSubstitute_WindowsEnvCaseInsensitive(t *testing.T) {
	ctx := Context{Platform: "windows", LocalEnv: map[string]string{"PATH": "C:\\Windows"}}
	if got := Substitute(ctx, "${env:path}"); got != "C:\\Windows" {
		t.Errorf("windows env lookup should be case-insensitive, got %q", got)
	}
}

func TestContainerSubstitute(t *testing.T) {
	ctx := ContainerContext{ContainerEnv: map[string]string{"NODE_VERSION": "20"}}
	if got := ContainerSubstitute(ctx, "node-${containerEnv:NODE_VERSION}"); got != "node-20" {
		t.Errorf("got %q", got)
	}
	// Pre-container variables must NOT be touched in this phase.
	if got := ContainerSubstitute(ctx, "${localWorkspaceFolder}"); got != "${localWorkspaceFolder}" {
		t.Errorf("pre-container variable should be left alone in container phase, got %q", got)
	}
}

func TestDevcontainerID_DeterministicAndStableAcrossKeyOrder(t *testing.T) {
	a := devcontainerIDForLabels(map[string]string{"a": "1", "b": "2"})
	b := devcontainerIDForLabels(map[string]string{"b": "2", "a": "1"})
	if a != b {
		t.Fatalf("id depends on key insertion order: %q vs %q", a, b)
	}
	if len(a) != 52 {
		t.Errorf("expected 52-char id, got %d (%q)", len(a), a)
	}
}

func TestApplyMap_SubstitutesValuesNotKeys(t *testing.T) {
	ctx := Context{LocalEnv: map[string]string{"V": "x"}}
	got := ApplyMap(ctx, map[string]string{"${env:V}": "${env:V}"})
	if got["${env:V}"] != "x" {
		t.Errorf("keys must not be substituted, values must: got %#v", got)
	}
}
