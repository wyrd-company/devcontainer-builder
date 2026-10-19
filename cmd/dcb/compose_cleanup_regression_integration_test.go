//go:build docker_integration

// relationships:
//
//	references: [../../pkg/cli/host, docker_compose_integration_test]
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	dockerclient "github.com/docker/docker/client"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/dockercompose"
)

func TestDockerComposeObjectSelectedConfigCLI(t *testing.T) {
	ctx := context.Background()
	cli := hostRequireDocker(t, ctx)
	defer cli.Close()
	hostEnsureImage(t, ctx, cli, "alpine:3.20")
	bin := hostBuildBinary(t)
	for _, scenario := range []string{"config", "override", "continue", "id-label"} {
		t.Run(scenario, func(t *testing.T) {
			workspace := hostPrepareWorkspace(t, "alpine:3.20")
			config := filepath.Join(workspace, ".devcontainer", "devcontainer.json")
			alternate := filepath.Join(workspace, ".devcontainer", "alternate.json")
			project := strings.TrimPrefix(filepath.Base(workspace), ".")
			for path, value := range map[string]string{
				config:    `{"dockerComposeFile":"compose.yml","service":"app"}`,
				alternate: `{"image":"alpine:3.20"}`,
				filepath.Join(filepath.Dir(config), "compose.yml"): "name: " + project + "\nservices:\n  app:\n    image: alpine:3.20\n",
			} {
				if err := os.WriteFile(path, []byte(value), 0600); err != nil {
					t.Fatal(err)
				}
			}
			flags := []string{"--workspace-folder", workspace, "--config", alternate}
			if scenario == "override" {
				flags = []string{"--workspace-folder", workspace, "--override-config", alternate}
			}
			if scenario == "id-label" {
				flags = append(flags, "--id-label", "example.selection="+filepath.Base(workspace))
			}
			output := hostRunCommand(t, os.Environ(), bin, append([]string{"up", "--skip-post-create"}, flags...)...)
			var result struct {
				ContainerID string `json:"containerId"`
			}
			if err := json.Unmarshal(output, &result); err != nil || result.ContainerID == "" {
				t.Fatalf("up result: %s %v", output, err)
			}
			t.Cleanup(func() { _ = cli.ContainerRemove(ctx, result.ContainerID, container.RemoveOptions{Force: true}) })
			extra := ""
			{
				labels := map[string]string{dockercompose.LabelProject: project, dockercompose.LabelService: "app"}
				if scenario == "continue" || scenario == "id-label" {
					labels[dockercompose.LabelLocalFolder] = workspace
				}
				created, err := cli.ContainerCreate(ctx, &container.Config{Image: "alpine:3.20", Cmd: []string{"sleep", "infinity"}, Labels: labels}, nil, nil, nil, "")
				if err != nil {
					t.Fatal(err)
				}
				extra = created.ID
				t.Cleanup(func() { _ = cli.ContainerRemove(ctx, extra, container.RemoveOptions{Force: true}) })
				if err := cli.ContainerStart(ctx, extra, container.StartOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			hostRunCommand(t, os.Environ(), bin, append([]string{"down"}, flags...)...)
			if _, err := cli.ContainerInspect(ctx, result.ContainerID); !dockerclient.IsErrNotFound(err) {
				t.Fatalf("selected non-Compose container survived down: %v", err)
			}
			if extra != "" {
				_, err := cli.ContainerInspect(ctx, extra)
				if scenario != "continue" {
					if err != nil {
						t.Fatalf("down removed unselected default Compose container: %v", err)
					}
				} else if !dockerclient.IsErrNotFound(err) {
					t.Fatalf("runtime Compose container survived down: %v", err)
				}
			}
		})
	}
}
