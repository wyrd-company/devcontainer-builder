//go:build docker_integration

// relationships:
//
//	references: [options_configuration, docker_host_integration_test]
package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDockerConfigurationResultCLI(t *testing.T) {
	isolateDockerConfig(t)
	cli := hostRequireDocker(t, context.Background())
	t.Cleanup(func() { _ = cli.Close() })
	hostEnsureImage(t, context.Background(), cli, "alpine:3.20")
	workspace := hostPrepareWorkspace(t, "alpine:3.20")
	body := `{"image":"alpine:3.20","updateRemoteUserUID":false,"containerEnv":{"SAMPLE_VALUE":"container-value"},"remoteEnv":{"VALUE":"${containerEnv:SAMPLE_VALUE}"},"extension":{"enabled":true}}`
	if err := os.WriteFile(filepath.Join(workspace, ".devcontainer", "devcontainer.json"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	bin := hostBuildBinary(t)
	t.Cleanup(func() { _ = hostRunCommandAllowError(bin, "down", "--workspace-folder", workspace) })
	id := ""
	for _, command := range []string{"up", "set-up"} {
		for _, mode := range []string{"default", "configuration", "merged", "both", "false"} {
			t.Run(command+"/"+mode, func(t *testing.T) {
				args := []string{command, "--workspace-folder", workspace, "--skip-post-create"}
				if command == "set-up" {
					args = append(args, "--container-id", id)
				}
				include, merged := mode == "configuration" || mode == "both", mode == "merged" || mode == "both"
				if include {
					args = append(args, "--include-configuration")
				}
				if merged {
					args = append(args, "--include-merged-configuration")
				}
				if mode == "false" {
					args = append(args, "--include-configuration=false", "--include-merged-configuration=false")
				}
				output := hostRunCommand(t, os.Environ(), bin, args...)
				var result map[string]any
				if err := json.Unmarshal(output, &result); err != nil {
					t.Fatal(err)
				}
				if command == "up" {
					id = result["containerId"].(string)
				}
				for key, want := range map[string]bool{"configuration": include, "mergedConfiguration": merged} {
					v, present := result[key]
					if present != want {
						t.Fatalf("%s present=%v want=%v: %s", key, present, want, output)
					}
					if present {
						config := v.(map[string]any)
						if config["remoteEnv"].(map[string]any)["VALUE"] != "container-value" || config["extension"].(map[string]any)["enabled"] != true {
							t.Fatalf("config=%v", config)
						}
					}
				}
			})
		}
	}
}
