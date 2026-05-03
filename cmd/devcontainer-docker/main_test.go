package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

func TestCLI_Version(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "version")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run . version: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "devcontainer-docker dev" {
		t.Fatalf("version output = %q, want devcontainer-docker version", out)
	}
}

func TestResolveRemoteEnv(t *testing.T) {
	got := resolveRemoteEnv(map[string]any{
		"STATIC":         "value",
		"FROM_LOCAL":     "${localEnv:HOST_VALUE}",
		"FROM_CONTAINER": "${containerEnv:BASE_VALUE}",
		"FROM_ID":        "${devcontainerId}",
		"JSON":           map[string]any{"enabled": true},
	}, substitute.Context{
		LocalEnv: map[string]string{"HOST_VALUE": "host"},
		IDLabels: map[string]string{
			"devcontainer.local_folder": "/workspace",
			"devcontainer.config_file":  "/workspace/.devcontainer/devcontainer.json",
		},
	}, map[string]string{"BASE_VALUE": "container"})

	for _, tc := range []struct {
		key  string
		want string
	}{
		{"STATIC", "value"},
		{"FROM_LOCAL", "host"},
		{"FROM_CONTAINER", "container"},
		{"JSON", `{"enabled":true}`},
	} {
		if got[tc.key] != tc.want {
			t.Fatalf("%s = %q, want %q", tc.key, got[tc.key], tc.want)
		}
	}
	if got["FROM_ID"] == "" || strings.Contains(got["FROM_ID"], "devcontainerId") {
		t.Fatalf("FROM_ID was not resolved: %q", got["FROM_ID"])
	}
}
