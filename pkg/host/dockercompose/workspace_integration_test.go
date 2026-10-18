//go:build docker_integration

// relationships:
//
//	references: [up, override, docker_integration_test]
package dockercompose

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/docker/docker/api/types/mount"
	"github.com/wyrd-company/devcontainer-builder/internal/dockertest"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestComposeWorkspaceMountIntegration(t *testing.T) {
	ctx := context.Background()
	cli := requireComposeDocker(t, ctx)
	t.Cleanup(func() { _ = cli.Close() })
	dockertest.EnsureImage(t, ctx, cli, "alpine:3.20")
	for _, tc := range []struct {
		name, folder, wantDir string
		mounted               bool
	}{
		{"explicit-root", "/", "/", true},
		{"default-root", "", "/", true},
		{"compose-owned-folder", "/workspace", "/workspace", true},
		{"no-workspace-mount", "", "/", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := prepareComposeWorkspace(t)
			// A different source at the same target exposes an injected bind
			// that would replace the Compose file's workspace mount.
			source := filepath.Join(workspace, "host-bind")
			writeFile(t, filepath.Join(source, "sample.txt"), "compose-owned")
			requireBindableComposePath(t, ctx, cli, "alpine:3.20", source)
			body := "services:\n  app:\n    image: alpine:3.20\n    command: [sleep, infinity]\n"
			if tc.mounted {
				body += "    volumes:\n      - type: bind\n        source: ../host-bind\n        target: /workspace\n        read_only: true\n"
			}
			writeFile(t, filepath.Join(workspace, ".devcontainer", "compose.yml"), body)
			config := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
			writeFile(t, config, fmt.Sprintf(`{"dockerComposeFile":"compose.yml","service":"app","workspaceFolder":%q,"postCreateCommand":"pwd > /tmp/sample-workdir"}`, tc.folder))
			dc, err := spec.Load(config)
			if err != nil {
				t.Fatal(err)
			}
			h := New(cli, Options{Logger: t.Logf})
			t.Cleanup(func() {
				if err := h.Remove(ctx, workspace); err != nil {
					t.Errorf("remove owned project: %v", err)
				}
			})
			up, err := h.Up(ctx, UpRequest{Spec: dc, SpecPath: config, LocalWorkspace: workspace, LocalEnv: map[string]string{}})
			if err != nil {
				t.Fatal(err)
			}
			inspected, err := cli.ContainerInspect(ctx, up.PrimaryContainerID)
			if err != nil {
				t.Fatal(err)
			}
			wantMounts := 0
			if tc.mounted {
				wantMounts = 1
			}
			if len(inspected.Mounts) != wantMounts {
				t.Fatalf("mounts = %+v, want %d Compose mounts", inspected.Mounts, wantMounts)
			}
			if tc.mounted {
				m := inspected.Mounts[0]
				if m.Type != mount.TypeBind || m.Source != source || m.Destination != "/workspace" || m.RW {
					t.Fatalf("Compose mount changed: %+v", m)
				}
				result := execOK(t, ctx, h, ExecRequest{ContainerID: up.PrimaryContainerID, Cmd: []string{"cat", "/workspace/sample.txt"}})
				if string(result.Stdout) != "compose-owned" {
					t.Fatalf("Compose source contents = %q", result.Stdout)
				}
			}
			result := execOK(t, ctx, h, ExecRequest{ContainerID: up.PrimaryContainerID, Cmd: []string{"cat", "/tmp/sample-workdir"}})
			if string(result.Stdout) != tc.wantDir+"\n" {
				t.Fatalf("lifecycle working directory = %q, want %q", result.Stdout, tc.wantDir)
			}
		})
	}
}
