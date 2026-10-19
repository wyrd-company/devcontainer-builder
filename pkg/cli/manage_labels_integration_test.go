//go:build docker_integration

// relationships:
//
//	references: [host, manage_labels_test, ../host/dockercompose/manage]
package cli

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/api/types/volume"
	"github.com/docker/docker/errdefs"
)

func TestManageIDLabelsComposeDockerIntegration(t *testing.T) {
	cli, err := newDockerClient()
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	ctx := context.Background()
	root := t.TempDir()
	sum := sha256.Sum256([]byte(root))
	unique := fmt.Sprintf("%x", sum[:6])
	projects := []string{"sample-manage-" + unique + "-selected", "sample-manage-" + unique + "-cwd"}
	projectFilter := func(project string) filters.Args {
		return filters.NewArgs(filters.Arg("label", "com.docker.compose.project="+project))
	}
	// The project names and owner labels are unique to this test. Cleanup
	// never selects a project from the current directory or from id labels.
	cleanup := func() {
		for _, project := range projects {
			list, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: projectFilter(project)})
			if err != nil {
				t.Error(err)
			}
			for _, c := range list {
				if err := cli.ContainerRemove(ctx, c.ID, container.RemoveOptions{Force: true, RemoveVolumes: true}); err != nil && !errdefs.IsNotFound(err) {
					t.Error(err)
				}
			}
			nets, err := cli.NetworkList(ctx, network.ListOptions{Filters: projectFilter(project)})
			if err != nil {
				t.Error(err)
			}
			for _, n := range nets {
				if err := cli.NetworkRemove(ctx, n.ID); err != nil && !errdefs.IsNotFound(err) {
					t.Error(err)
				}
			}
			vols, err := cli.VolumeList(ctx, volume.ListOptions{Filters: projectFilter(project)})
			if err != nil {
				t.Error(err)
			}
			for _, v := range vols.Volumes {
				if err := cli.VolumeRemove(ctx, v.Name, true); err != nil && !errdefs.IsNotFound(err) {
					t.Error(err)
				}
			}
		}
	}
	t.Cleanup(cleanup)
	var ids, workspaces []string
	for i, project := range projects {
		workspace := filepath.Join(root, fmt.Sprintf("workspace-%d", i))
		compose := fmt.Sprintf("name: %s\nservices:\n  app:\n    image: alpine:3.20\n    labels:\n      example.test/manage-owner: %s\n    volumes: [data:/data]\n  sidecar:\n    image: alpine:3.20\n    command: [sleep, '600']\n    labels:\n      example.test/manage-owner: %s\nvolumes:\n  data: {}\n", project, unique, unique)
		writeTestFile(t, filepath.Join(workspace, "compose.yml"), compose)
		writeTestFile(t, filepath.Join(workspace, ".devcontainer.json"), `{"dockerComposeFile":"compose.yml","service":"app","workspaceFolder":"/","updateRemoteUserUID":false}`)
		result, _ := invoke(t, 0, "up", "--workspace-folder", workspace, "--skip-post-create", "--id-label", mountsIDKey+"="+project, "--id-label", mountsRoleKey+"=managed")
		id, ok := result["containerId"].(string)
		if !ok {
			t.Fatalf("up result=%v", result)
		}
		ids = append(ids, id)
		workspaces = append(workspaces, workspace)
	}
	if err := os.Remove(filepath.Join(workspaces[0], ".devcontainer.json")); err != nil {
		t.Fatal(err)
	}
	// The selected project's config is gone; cwd has another live Compose
	// project. Neither inspection nor teardown may use cwd's configuration.
	t.Chdir(workspaces[1])
	labels := []string{"--id-label", mountsIDKey + "=" + projects[0], "--id-label", mountsRoleKey + "=managed"}
	inspected, _ := invoke(t, 0, append([]string{"inspect"}, labels...)...)
	if inspected["containerId"] != ids[0] {
		t.Fatalf("inspect=%v, want %s", inspected, ids[0])
	}
	invoke(t, 0, append([]string{"down"}, labels...)...)
	selected, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: projectFilter(projects[0])})
	if err != nil || len(selected) != 0 {
		t.Fatalf("selected project containers=%v err=%v", selected, err)
	}
	cwd, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: projectFilter(projects[1])})
	if err != nil || len(cwd) != 2 {
		t.Fatalf("cwd project changed: containers=%v err=%v", cwd, err)
	}
	for _, project := range projects {
		nets, err := cli.NetworkList(ctx, network.ListOptions{Filters: projectFilter(project)})
		if err != nil || (project == projects[0] && len(nets) != 0) || (project == projects[1] && len(nets) == 0) {
			t.Fatalf("project %s networks=%v err=%v", project, nets, err)
		}
		_, err = cli.VolumeInspect(ctx, project+"_data")
		if project == projects[0] && !errdefs.IsNotFound(err) || project == projects[1] && err != nil {
			t.Fatalf("project %s volume err=%v", project, err)
		}
	}
	cleanup()
	remaining, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: filters.NewArgs(filters.Arg("label", "example.test/manage-owner="+unique))})
	if err != nil || len(remaining) != 0 {
		t.Fatalf("residue=%v err=%v", remaining, err)
	}
}
