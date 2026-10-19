//go:build docker_integration

// relationships:
//
//	references: [object_state, up, object_state_integration_test]
package dockercompose

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	dockerclient "github.com/docker/docker/client"
	"github.com/wyrd-company/devcontainer-builder/internal/dockertest"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestComposeObjectRecreateCleanupIntegration(t *testing.T) {
	ctx := context.Background()
	cli := requireComposeDocker(t, ctx)
	defer cli.Close()
	dockertest.EnsureImage(t, ctx, cli, "alpine:3.20")
	for _, scenario := range []string{"recreated", "reused-service", "external-directory", "external-file-stopped", "prefix-neighbor", "ownership", "unmounted-producer", "cleanup-error", "consumer-error"} {
		t.Run(scenario, func(t *testing.T) {
			workspace := prepareComposeWorkspace(t)
			parent := t.TempDir()
			h := New(cli, Options{UserDataFolder: parent})
			project := objectTestProject(workspace)
			config := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
			writeNameFile(t, config, `{"dockerComposeFile":"compose.yml","service":"app"}`)
			writeNameFile(t, filepath.Join(parent, "keep"), "sentinel")
			t.Cleanup(func() { _ = h.Remove(ctx, workspace) })
			var preserved []string
			extraMounts := ""
			if scenario == "ownership" || scenario == "unmounted-producer" {
				for _, owner := range []struct{ workspace, project string }{{workspace + "-other", project}, {workspace, project + "-other"}, {workspace, project}} {
					root, err := h.allocateObjects(ctx, owner.project, owner.workspace)
					if err != nil {
						t.Fatal(err)
					}
					writeNameFile(t, filepath.Join(root, "source"), "retained-synthetic")
					preserved = append(preserved, root)
					ownerWorkspace := owner.workspace
					t.Cleanup(func() { _ = h.Remove(ctx, ownerWorkspace) })
					if scenario == "ownership" && owner != (struct{ workspace, project string }{workspace, project}) {
						extraMounts += fmt.Sprintf("      - %q\n", filepath.Join(root, "source")+":/original-"+filepath.Base(root)+":ro")
					}
				}
			}
			writeCompose := func(app, worker string) {
				volumes := ""
				if extraMounts != "" {
					volumes = "    volumes:\n" + extraMounts
				}
				body := fmt.Sprintf("name: %s\nservices:\n  app:\n    image: alpine:3.20\n    environment: {VERSION: %q}\n    configs: [sample]\n    secrets: [token]\n%s  worker:\n    image: alpine:3.20\n    command: [sleep, infinity]\n    environment: {VERSION: %q}\n    configs: [sample]\n    secrets: [token]\nconfigs:\n  sample:\n    content: synthetic-config\nsecrets:\n  token:\n    environment: SAMPLE_TOKEN\n", project, app, volumes, worker)
				writeNameFile(t, filepath.Join(filepath.Dir(config), "compose.yml"), body)
			}
			writeCompose("one", "one")
			dc, err := spec.Load(config)
			if err != nil {
				t.Fatal(err)
			}
			req := UpRequest{Spec: dc, SpecPath: config, LocalWorkspace: workspace, LocalEnv: map[string]string{"SAMPLE_TOKEN": "synthetic-token"}, SkipLifecycle: true}
			first, err := h.Up(ctx, req)
			if err != nil {
				t.Fatal(err)
			}
			rootFor := func(id string) string {
				t.Helper()
				info, err := cli.ContainerInspect(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				for _, m := range info.Mounts {
					if m.Destination == "/sample" {
						return filepath.Dir(filepath.Dir(m.Source))
					}
				}
				t.Fatal("no object mount")
				return ""
			}
			oldRoot := rootFor(first.PrimaryContainerID)
			records, err := h.objectRecords(ctx, workspace)
			if err != nil {
				t.Fatal(err)
			}
			record := "dcb-" + filepath.Base(oldRoot)
			holder := ""
			if strings.HasPrefix(scenario, "external-") || scenario == "prefix-neighbor" || scenario == "cleanup-error" {
				mounts := []mount.Mount{{Type: mount.TypeBind, Source: oldRoot, Target: "/held", ReadOnly: true}}
				if scenario == "prefix-neighbor" {
					neighbor := oldRoot + "-other"
					if err := os.Mkdir(neighbor, 0700); err != nil {
						t.Fatal(err)
					}
					writeNameFile(t, filepath.Join(neighbor, "keep"), "sentinel")
					mounts[0].Source = neighbor
				}
				if scenario == "external-file-stopped" {
					mounts[0].Source = filepath.Join(oldRoot, "secrets", "token")
				}
				if scenario == "cleanup-error" {
					mounts = []mount.Mount{{Type: mount.TypeVolume, Source: record, Target: "/ledger"}}
				}
				created, err := cli.ContainerCreate(ctx, &container.Config{Image: "alpine:3.20", Cmd: []string{"sleep", "infinity"}}, &container.HostConfig{Mounts: mounts}, nil, nil, "")
				if err != nil {
					t.Fatal(err)
				}
				holder = created.ID
				t.Cleanup(func() { _ = cli.ContainerRemove(ctx, holder, container.RemoveOptions{Force: true}) })
				if err := cli.ContainerStart(ctx, holder, container.StartOptions{}); err != nil {
					t.Fatal(err)
				}
				if scenario == "external-file-stopped" {
					if err := cli.ContainerStop(ctx, holder, container.StopOptions{}); err != nil {
						t.Fatal(err)
					}
				}
			}
			if scenario == "consumer-error" {
				transport := objectRoundTripper(func(r *http.Request) (*http.Response, error) {
					if strings.HasSuffix(r.URL.Path, "/containers/json") && r.URL.Query().Get("filters") == "" {
						return nil, errors.New("synthetic consumer lookup failure")
					}
					return cli.HTTPClient().Transport.RoundTrip(r)
				})
				wrapped, err := dockerclient.NewClientWithOpts(dockerclient.WithHost(cli.DaemonHost()), dockerclient.WithVersion(cli.ClientVersion()), dockerclient.WithHTTPClient(&http.Client{Transport: transport}))
				if err != nil {
					t.Fatal(err)
				}
				defer wrapped.Close()
				failing := New(wrapped, Options{})
				if err := failing.removeRetiredObjects(ctx, records); err == nil {
					t.Fatal("consumer lookup failure claimed successful cleanup")
				}
				if _, err := os.Stat(filepath.Join(oldRoot, "secrets", "token")); err != nil {
					t.Fatal("consumer lookup failure removed mounted secret")
				}
				return
			}
			workerVersion := "two"
			if scenario == "reused-service" {
				workerVersion = "one"
			}
			writeCompose("two", workerVersion)
			next, err := h.Up(ctx, req)
			if scenario == "cleanup-error" {
				if err == nil {
					t.Fatal("retired ownership deletion failure claimed successful Up")
				}
				assertObjectAbsent(t, oldRoot)
				if _, err := cli.VolumeInspect(ctx, record); err != nil {
					t.Fatal("failed retirement lost retry record")
				}
				if err := cli.ContainerRemove(ctx, holder, container.RemoveOptions{Force: true}); err != nil {
					t.Fatal(err)
				}
				if err := h.Remove(ctx, workspace); err != nil {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if next.PrimaryContainerID == first.PrimaryContainerID {
				t.Fatal("expected primary recreation")
			}
			nextRoot := rootFor(next.PrimaryContainerID)
			if nextRoot == oldRoot {
				t.Fatal("recreation reused old root")
			}
			for _, root := range preserved {
				if data, err := os.ReadFile(filepath.Join(root, "source")); err != nil || string(data) != "retained-synthetic" {
					t.Fatal("retirement removed another owner or in-flight producer")
				}
			}
			if strings.HasPrefix(scenario, "external-") || scenario == "reused-service" {
				if _, err := os.Stat(filepath.Join(oldRoot, "secrets", "token")); err != nil {
					t.Fatal("retirement removed a root still mounted by a container")
				}
				if scenario == "reused-service" {
					if next.ContainerIDs["worker"] != first.ContainerIDs["worker"] {
						t.Fatal("worker unexpectedly recreated")
					}
					writeCompose("two", "two")
					if _, err := h.Up(ctx, req); err != nil {
						t.Fatal(err)
					}
					assertObjectAbsent(t, oldRoot)
				}
			} else {
				assertObjectAbsent(t, oldRoot)
				if _, err := cli.VolumeInspect(ctx, record); !dockerclient.IsErrNotFound(err) {
					t.Fatalf("retired ownership residue: %v", err)
				}
			}

			if data, err := os.ReadFile(filepath.Join(nextRoot, "secrets", "token")); err != nil || string(data) != "synthetic-token" {
				t.Fatal("retirement removed replacement secret")
			}
			if data, err := os.ReadFile(filepath.Join(parent, "keep")); err != nil || string(data) != "sentinel" {
				t.Fatal("retirement removed parent sentinel")
			}
		})
	}
}
