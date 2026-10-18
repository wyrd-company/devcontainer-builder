//go:build docker_integration

// relationships:
//
//	references: [manage, projectname, metadata, ../../cli/host]
package dockercompose_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	dockerclient "github.com/docker/docker/client"

	command "github.com/wyrd-company/devcontainer-builder/pkg/cli"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/dockercompose"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestComposeSameBasenameTeardownIntegration(t *testing.T) {
	t.Setenv("COMPOSE_PROJECT_NAME", "")
	ctx := context.Background()
	cli := metadataDocker(t, ctx)
	for _, topology := range []string{"replaced-primary", "both-primaries"} {
		for _, operation := range []string{"stop", "remove", "down"} {
			t.Run(topology+"/"+operation, func(t *testing.T) {
				h := dockercompose.New(cli, dockercompose.Options{StopTimeout: time.Second})
				root := metadataWorkspace(t)
				leaf := "sample-" + strings.TrimPrefix(filepath.Base(root), ".tmp-")
				first := filepath.Join(root, "first", leaf)
				second := filepath.Join(root, "second", leaf)
				project := leaf + "_devcontainer"
				objectDir := identityObjectDir(project)
				t.Cleanup(func() { cleanupIdentityProject(t, cli, project, objectDir) })
				for i, workspace := range []string{first, second} {
					service := "app"
					if topology == "both-primaries" {
						service = []string{"alpha", "beta"}[i]
					}
					config := writeIdentityWorkspace(t, workspace, service)
					dc, err := spec.Load(config)
					if err != nil {
						t.Fatal(err)
					}
					up, err := h.Up(ctx, dockercompose.UpRequest{Spec: dc, SpecPath: config, LocalWorkspace: workspace, LocalEnv: map[string]string{}, SkipLifecycle: true, AllowProjectConflict: i > 0})
					if err != nil || up.ProjectName != project {
						t.Fatalf("up = %+v, %v", up, err)
					}
				}
				args := filters.NewArgs(filters.Arg("label", dockercompose.LabelProject+"="+project))
				before, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
				if err != nil {
					t.Fatal(err)
				}
				want := 2
				if topology == "both-primaries" {
					want = 3
				}
				if len(before) != want {
					t.Fatalf("project containers = %d, want %d", len(before), want)
				}
				// Include an unlabelled sidecar and a stopped foreign primary:
				// every project container must be checked before stopping anything.
				other, err := h.InspectPrimary(ctx, second)
				if err != nil {
					t.Fatal(err)
				}
				if err := cli.ContainerStop(ctx, other.ID, container.StopOptions{Timeout: new(int)}); err != nil {
					t.Fatal(err)
				}
				states := map[string]bool{}
				for _, c := range before {
					info, err := cli.ContainerInspect(ctx, c.ID)
					if err != nil {
						t.Fatal(err)
					}
					states[c.ID] = info.State.Running
				}
				// Shared project data must survive the refusal too.
				marker := filepath.Join(objectDir, "preserved")
				metadataWrite(t, marker, "sample-data")
				requested := []string{first}
				if topology == "both-primaries" {
					requested = append(requested, second)
				}
				for _, workspace := range requested {
					var refusal error
					switch operation {
					case "stop":
						refusal = h.Stop(ctx, workspace)
					case "remove":
						refusal = h.Remove(ctx, workspace)
					case "down":
						var out, logs bytes.Buffer
						code := command.Run(ctx, []string{"down", "--workspace-folder", workspace}, "test", strings.NewReader(""), &out, &logs)
						if code != 1 {
							t.Fatalf("down exit = %d, want 1: %s %s", code, &out, &logs)
						}
						refusal = errors.New(out.String() + logs.String())
					}
					if refusal == nil || !strings.Contains(refusal.Error(), "belongs to workspace") {
						t.Fatalf("%s must refuse another workspace's project: %v", operation, refusal)
					}
				}
				for id, running := range states {
					info, err := cli.ContainerInspect(ctx, id)
					if err != nil || info.State.Running != running {
						t.Fatalf("%s changed project container %s: %+v, %v", operation, id, info.State, err)
					}
				}
				if _, err := cli.NetworkInspect(ctx, project+"_default", network.InspectOptions{}); err != nil {
					t.Fatalf("network did not survive refusal: %v", err)
				}
				if _, err := cli.VolumeInspect(ctx, project+"_sample-cache"); err != nil {
					t.Fatalf("volume did not survive refusal: %v", err)
				}
				if body, err := os.ReadFile(marker); err != nil || string(body) != "sample-data" {
					t.Fatalf("materialized directory changed: %q, %v", body, err)
				}
			})
		}
	}
}

func TestComposeBrokenConfigNoProjectIntegration(t *testing.T) {
	ctx := context.Background()
	cli := metadataDocker(t, ctx)
	h := dockercompose.New(cli, dockercompose.Options{})
	for _, problem := range []string{"devcontainer", "compose", "missing-compose"} {
		t.Run(problem, func(t *testing.T) {
			workspace := metadataWorkspace(t)
			config := writeIdentityWorkspace(t, workspace, "app")
			switch problem {
			case "devcontainer":
				metadataWrite(t, config, "{")
			case "compose":
				metadataWrite(t, filepath.Join(filepath.Dir(config), "compose.yml"), "services: [")
			case "missing-compose":
				if err := os.Remove(filepath.Join(filepath.Dir(config), "compose.yml")); err != nil {
					t.Fatal(err)
				}
			}
			_, inspectErr := h.Inspect(ctx, workspace)
			_, primaryErr := h.InspectPrimary(ctx, workspace)
			for op, err := range map[string]error{"inspect": inspectErr, "primary": primaryErr, "stop": h.Stop(ctx, workspace), "remove": h.Remove(ctx, workspace)} {
				if !errors.Is(err, dockercompose.ErrProjectNotFound) {
					t.Errorf("%s error = %v, want ErrProjectNotFound", op, err)
				}
			}
			if problem != "devcontainer" {
				// CLI down treats a missing project as successful teardown.
				metadataCLI(t, ctx, "down", "--workspace-folder", workspace)
			}
		})
	}
}

func writeIdentityWorkspace(t *testing.T, workspace, service string) string {
	t.Helper()
	dir := filepath.Join(workspace, ".devcontainer")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	metadataWrite(t, filepath.Join(dir, "compose.yml"), fmt.Sprintf("services:\n  %s:\n    image: alpine:3.20\n    configs: [sample]\n  sidecar:\n    image: alpine:3.20\n    command: [sleep, infinity]\nconfigs:\n  sample:\n    content: sample-content\n", service))
	config := filepath.Join(dir, "devcontainer.json")
	metadataWrite(t, config, fmt.Sprintf(`{"dockerComposeFile":"compose.yml","service":%q,"mounts":["type=volume,source=sample-cache,target=/sample-cache"]}`, service))
	return config
}

func identityObjectDir(project string) string {
	hash := sha256.Sum256([]byte(project))
	return filepath.Join(os.TempDir(), "dcb-compose", hex.EncodeToString(hash[:8]))
}

// All objects carry this fixture's run-unique project label. Cleanup is separate
// from Stop/Remove so failing assertions and guard mutations cannot bypass it.
func cleanupIdentityProject(t *testing.T, cli *dockerclient.Client, project, objectDir string) {
	t.Helper()
	ctx := context.Background()
	args := filters.NewArgs(filters.Arg("label", dockercompose.LabelProject+"="+project))
	containers, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		t.Error(err)
	}
	for _, c := range containers {
		if c.Labels[dockercompose.LabelProject] == project {
			if err := cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true}); err != nil {
				t.Error(err)
			}
		}
	}
	nets, _ := cli.NetworkList(ctx, network.ListOptions{Filters: args})
	for _, n := range nets {
		if n.Labels[dockercompose.LabelProject] == project {
			if err := cli.NetworkRemove(ctx, n.ID); err != nil {
				t.Error(err)
			}
		}
	}
	vols, _ := cli.VolumeList(ctx, volume.ListOptions{Filters: args})
	for _, v := range vols.Volumes {
		if v.Labels[dockercompose.LabelProject] == project {
			if err := cli.VolumeRemove(ctx, v.Name, true); err != nil {
				t.Error(err)
			}
		}
	}
	if err := os.RemoveAll(objectDir); err != nil {
		t.Error(err)
	}
}
