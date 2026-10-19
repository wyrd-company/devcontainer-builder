// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package cli

import (
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestOverrideConfigurationRetainsSourcePaths(t *testing.T) {
	generateImageFixture(t)
	workspace := t.TempDir()
	original := filepath.Join(workspace, ".devcontainer.json")
	if err := os.WriteFile(original, []byte(`invalid original content`), 0o644); err != nil {
		t.Fatal(err)
	}
	feature := filepath.Join(workspace, "feature")
	if err := os.Mkdir(feature, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(feature, "devcontainer-feature.json"), []byte(`{"id":"example","version":"1.0.0","name":"Example"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(feature, "install.sh"), []byte("#!/bin/sh\ntrue\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	override := filepath.Join(t.TempDir(), "override.json")
	if err := os.WriteFile(override, []byte(`{"image":"busybox:latest","features":{"./feature":{}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	result, _ := invoke(t, 0, "read-configuration", "--workspace-folder", workspace, "--override-config", override)
	dc := result["configuration"].(map[string]any)
	if dc["image"] != "busybox:latest" || dc["configFilePath"] != original {
		t.Fatalf("override=%v", dc)
	}
	result, _ = invoke(t, 0, "generate", "--workspace-folder", workspace, "--override-config", override, "--work-dir", t.TempDir())
	dockerfile, err := os.ReadFile(result["dockerfile"].(string))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(dockerfile), "FROM busybox:latest") || !strings.Contains(string(dockerfile), "_features/") {
		t.Fatalf("override generate=%s", dockerfile)
	}
	// An override can also supply content when the original path is absent.
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	result, _ = invoke(t, 0, "read-configuration", "--workspace-folder", workspace, "--config", original, "--override-config", override)
	if result["configuration"].(map[string]any)["configFilePath"] != original {
		t.Fatalf("override source=%v", result)
	}
}

func TestRemoteWorkspaceFolder(t *testing.T) {
	single := &spec.DevContainer{Path: "/host/sample/.devcontainer.json", Image: "alpine:3.20"}
	compose := &spec.DevContainer{Path: "/host/sample/.devcontainer.json", DockerComposeFile: spec.StringOrSlice{"compose.yml"}, Service: "app"}
	inspected := func(workingDir string, mounts ...container.MountPoint) container.InspectResponse {
		return container.InspectResponse{Config: &container.Config{WorkingDir: workingDir, Labels: map[string]string{"devcontainer.local_folder": "/host/sample"}}, Mounts: mounts}
	}
	workspaceMount := container.MountPoint{Type: mount.TypeBind, Source: "/host/sample", Destination: "/mounted"}
	otherMount := container.MountPoint{Type: mount.TypeBind, Source: "/host/other", Destination: "/other"}
	for _, tc := range []struct {
		name string
		dc   *spec.DevContainer
		info container.InspectResponse
		want string
	}{
		{"single ignores image workdir", single, inspected("/srv", workspaceMount), "/workspaces/sample"},
		{"single configured folder", &spec.DevContainer{Path: single.Path, WorkspaceFolder: "/sample"}, inspected("/srv"), "/sample"},
		{"compose ignores service workdir", compose, inspected("/srv", workspaceMount), "/"},
		{"compose configured folder", &spec.DevContainer{Path: compose.Path, DockerComposeFile: compose.DockerComposeFile, Service: "app", WorkspaceFolder: "/sample"}, inspected("/srv"), "/sample"},
		{"no configuration uses workspace mount", &spec.DevContainer{}, inspected("/srv", otherMount, workspaceMount), "/mounted"},
		{"no configuration without mount", &spec.DevContainer{}, inspected("/srv", otherMount), "/srv"},
		{"compose without workdir", compose, inspected(""), "/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := (&application{}).remoteWorkspaceFolder(tc.dc, "/host/sample", tc.info); got != tc.want {
				t.Fatalf("remoteWorkspaceFolder = %q, want %q", got, tc.want)
			}
		})
	}
}

// The parity harness removes derived images by this name (test/parity dcbImage).
func TestDefaultImageName(t *testing.T) {
	if got := defaultImage("/workspace"); got != "dcb-c52ddf65534b7b46:latest" {
		t.Fatalf("default image name changed: %s; update test/parity dcbImage", got)
	}
}
