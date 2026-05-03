package kubernetes

import (
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

func TestSubstituteSpec_LocalAndContainerWorkspace(t *testing.T) {
	dc := &spec.DevContainer{
		WorkspaceFolder: "${containerWorkspaceFolder}/sub",
		ContainerEnv: map[string]any{
			"WS":   "${localWorkspaceFolder}",
			"BIN":  "${env:HOME}/bin",
			"KEEP": 42, // non-string passes through untouched
		},
		OnCreate: spec.Command{Shell: "echo ${localWorkspaceFolder}"},
	}
	out := substituteSpec(dc, substitute.Context{
		LocalWorkspaceFolder:     "/host/proj",
		ContainerWorkspaceFolder: "/workspaces/proj",
		LocalEnv:                 map[string]string{"HOME": "/root"},
	})

	if got := out.WorkspaceFolder; got != "/workspaces/proj/sub" {
		t.Errorf("WorkspaceFolder = %q", got)
	}
	if got, _ := out.ContainerEnv["WS"].(string); got != "/host/proj" {
		t.Errorf("ContainerEnv[WS] = %v", out.ContainerEnv["WS"])
	}
	if got, _ := out.ContainerEnv["BIN"].(string); got != "/root/bin" {
		t.Errorf("ContainerEnv[BIN] = %v", out.ContainerEnv["BIN"])
	}
	if got := out.ContainerEnv["KEEP"]; got != 42 {
		t.Errorf("non-string env value should pass through, got %v", got)
	}
	if got := out.OnCreate.Shell; got != "echo /host/proj" {
		t.Errorf("OnCreate.Shell = %q", got)
	}
	// substituteSpec must return a new spec — mutating the result
	// shouldn't affect the input.
	if dc.WorkspaceFolder == out.WorkspaceFolder {
		t.Errorf("substituteSpec mutated the input")
	}
}

func TestSubstituteContainerEnv(t *testing.T) {
	dc := &spec.DevContainer{
		PostCreate: spec.Command{Shell: "echo ${containerEnv:PATH}"},
	}
	out := substituteContainerEnv(dc, map[string]string{"PATH": "/usr/bin"})
	if got := out.PostCreate.Shell; got != "echo /usr/bin" {
		t.Errorf("PostCreate.Shell = %q", got)
	}
}

func TestStringMapFromAny(t *testing.T) {
	in := map[string]any{
		"a": "1",
		"b": nil,
		"c": 42, // dropped
	}
	got := stringMapFromAny(in)
	if got["a"] != "1" || got["b"] != "" {
		t.Errorf("got %v", got)
	}
	if _, ok := got["c"]; ok {
		t.Errorf("non-string value should be dropped, got %v", got)
	}
}

func TestEnsurePosixPath(t *testing.T) {
	cases := map[string]string{
		"workspaces/proj":         "/workspaces/proj",
		"/workspaces/proj/":       "/workspaces/proj",
		"\\workspaces\\proj":      "/workspaces/proj",
		"workspaces//proj/../sub": "/workspaces/sub",
	}
	for in, want := range cases {
		if got := ensurePosixPath(in); got != want {
			t.Errorf("ensurePosixPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBasename(t *testing.T) {
	cases := map[string]string{
		"/host/proj":  "proj",
		"/host/proj/": "proj",
		"proj":        "proj",
		"":            "",
	}
	for in, want := range cases {
		if got := basename(in); got != want {
			t.Errorf("basename(%q) = %q, want %q", in, got, want)
		}
	}
}
