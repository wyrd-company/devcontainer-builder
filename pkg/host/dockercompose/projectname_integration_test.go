//go:build docker_integration

// relationships:
//
//	references: [projectname, manage, metadata, objects]
package dockercompose

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/wyrd-company/devcontainer-builder/internal/dockertest"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// Exercise run-unique name sources through workspace-only management operations.
// The literal devcontainer is covered by unit tests; live names are run-unique
// so this suite cannot join an operator's Compose project.
func TestComposeProjectIdentityIntegration(t *testing.T) {
	t.Setenv("COMPOSE_PROJECT_NAME", "")
	ctx := context.Background()
	cli := requireComposeDocker(t, ctx)
	t.Cleanup(func() { _ = cli.Close() })
	dockertest.EnsureImage(t, ctx, cli, "alpine:3.20")
	for _, source := range []string{"environment", "dotenv", "compose", "default", "other-directory", "partial"} {
		t.Run(source, func(t *testing.T) {
			workspace := prepareComposeWorkspace(t)
			dir := filepath.Join(workspace, ".devcontainer")
			name := toProjectName(filepath.Base(workspace)) + "_" + source
			env := map[string]string{}
			composeName := ""
			switch source {
			case "environment":
				env["COMPOSE_PROJECT_NAME"] = name
				composeName = name + "-ignored"
			case "dotenv":
				writeNameFile(t, filepath.Join(workspace, ".env"), "COMPOSE_PROJECT_NAME="+name+"\n")
				composeName = name + "-ignored"
			case "compose", "partial":
				composeName = name
			case "default":
				name = toProjectName(filepath.Base(workspace)) + "_devcontainer"
			case "other-directory":
				dir = filepath.Join(workspace, name)
			}
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			body := "services:\n  app:\n    image: alpine:3.20\n    configs: [sample]\n  sidecar:\n    image: alpine:3.20\n    command: [sleep, infinity]\nconfigs:\n  sample:\n    content: sample-content\n"
			if composeName != "" {
				body = "name: " + composeName + "\n" + body
			}
			writeNameFile(t, filepath.Join(dir, "compose.yml"), body)
			config := filepath.Join(dir, "devcontainer.json")
			writeNameFile(t, config, `{"dockerComposeFile":"compose.yml","service":"app","mounts":["type=volume,source=sample-cache,target=/sample-cache"]}`)
			dc, err := spec.Load(config)
			if err != nil {
				t.Fatal(err)
			}
			h := New(cli, Options{Logger: t.Logf, StopTimeout: time.Second})
			// Cleanup addresses only this scenario's run-unique project label,
			// independently of the workspace discovery under test.
			t.Cleanup(func() {
				args := filters.NewArgs(filters.Arg("label", LabelProject+"="+name))
				list, _ := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
				for _, c := range list {
					if c.Labels[LabelProject] == name {
						_ = cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true})
					}
				}
				nets, _ := cli.NetworkList(ctx, network.ListOptions{Filters: args})
				for _, n := range nets {
					if n.Labels[LabelProject] == name {
						_ = cli.NetworkRemove(ctx, n.ID)
					}
				}
				vols, err := cli.VolumeList(ctx, volume.ListOptions{Filters: args})
				if err == nil {
					for _, v := range vols.Volumes {
						if v.Labels[LabelProject] == name {
							_ = cli.VolumeRemove(ctx, v.Name, true)
						}
					}
				}
				_ = removeMaterialized(name)
			})
			up, err := h.Up(ctx, UpRequest{Spec: dc, SpecPath: config, LocalWorkspace: workspace, LocalEnv: env, SkipLifecycle: true})
			if err != nil {
				t.Fatal(err)
			}
			if up.ProjectName != name {
				t.Fatalf("up name = %q, want %q", up.ProjectName, name)
			}
			info, err := h.Inspect(ctx, workspace)
			if err != nil || info.Name != name || len(info.Containers) != 2 {
				t.Fatalf("Inspect = %+v, %v", info, err)
			}
			primary, err := h.InspectPrimary(ctx, workspace)
			if err != nil || primary.ID != up.PrimaryContainerID {
				t.Fatalf("InspectPrimary = %+v, %v", primary, err)
			}
			assertNetworkExists(t, ctx, cli, name+"_default", name)
			assertVolumeExists(t, ctx, cli, name+"_sample-cache", name)
			inspect, err := cli.ContainerInspect(ctx, up.PrimaryContainerID)
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256([]byte(name))
			objectDir := filepath.Join(os.TempDir(), "dcb-compose", hex.EncodeToString(hash[:8]))
			found := false
			for _, m := range inspect.Mounts {
				if m.Destination == "/sample" {
					found = filepath.Dir(m.Source) == filepath.Join(objectDir, "configs")
				}
			}
			if !found {
				t.Fatalf("config mount does not use named project's temp dir: %+v", inspect.Mounts)
			}
			// Resolution must use captured runtime identity, not the current
			// environment, .env, or Compose file.
			if source == "partial" {
				if err := cli.ContainerRemove(ctx, up.PrimaryContainerID, container.RemoveOptions{Force: true}); err != nil {
					t.Fatal(err)
				}
				if _, err := h.InspectPrimary(ctx, workspace); !errors.Is(err, ErrPrimaryNotFound) {
					t.Fatalf("partial project primary error = %v", err)
				}
			} else {
				writeNameFile(t, filepath.Join(workspace, ".env"), "COMPOSE_PROJECT_NAME="+name+"-changed\n")
				if err := os.Remove(config); err != nil {
					t.Fatal(err)
				}
			}
			if err := h.Stop(ctx, workspace); err != nil {
				t.Fatal(err)
			}
			info, err = h.Inspect(ctx, workspace)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range info.Containers {
				if c.Running {
					t.Fatalf("container %s still running after Stop", c.ID)
				}
			}
			if err := h.Remove(ctx, workspace); err != nil {
				t.Fatal(err)
			}
			assertProjectRemoved(t, ctx, cli, name)
			if _, err := os.Stat(objectDir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("materialized directory survived Remove: %v", err)
			}
			// No primary container remains discoverable by this workspace.
			left, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: filters.NewArgs(filters.Arg("label", LabelLocalFolder+"="+workspace))})
			if err != nil || len(left) != 0 {
				t.Fatalf("workspace containers after Remove = %v, %v", left, err)
			}
		})
	}
}
