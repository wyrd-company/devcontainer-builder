//go:build docker_integration

// relationships:
//
//	references: [conflicts, up, manage_integration, ../../cli/host]
package dockercompose_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	command "github.com/wyrd-company/devcontainer-builder/pkg/cli"
	"github.com/wyrd-company/devcontainer-builder/pkg/host/dockercompose"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// TestComposeProjectConflictUpIntegration exercises the strict host contract and
// the CLI's text/JSON report and explicit override against real owned containers.
func TestComposeProjectConflictUpIntegration(t *testing.T) {
	t.Setenv("COMPOSE_PROJECT_NAME", "")
	ctx := context.Background()
	cli := metadataDocker(t, ctx)
	h := dockercompose.New(cli, dockercompose.Options{StopTimeout: time.Second})
	root := metadataWorkspace(t)
	leaf := "sample-" + strings.TrimPrefix(filepath.Base(root), ".tmp-")
	project := leaf + "_devcontainer"
	objectDir := identityObjectDir(project)
	t.Cleanup(func() { cleanupIdentityProject(t, cli, project, objectDir) })
	workspaces := []string{filepath.Join(root, "first", leaf), filepath.Join(root, "second", leaf), filepath.Join(root, "third", leaf)}
	configs := []string{}
	for i, workspace := range workspaces {
		configs = append(configs, writeIdentityWorkspace(t, workspace, []string{"alpha", "beta", "gamma"}[i]))
	}
	secondCompose := filepath.Join(filepath.Dir(configs[1]), "compose.yml")
	body, err := os.ReadFile(secondCompose)
	if err != nil {
		t.Fatal(err)
	}
	metadataWrite(t, secondCompose, strings.ReplaceAll(string(body), "sample-content", "second-content"))
	dc, err := spec.Load(configs[0])
	if err != nil {
		t.Fatal(err)
	}
	up, err := h.Up(ctx, dockercompose.UpRequest{Spec: dc, SpecPath: configs[0], LocalWorkspace: workspaces[0], LocalEnv: map[string]string{}, SkipLifecycle: true})
	if err != nil {
		t.Fatal(err)
	}
	// A stopped conflicting container is still owned by its workspace.
	if err := cli.ContainerStop(ctx, up.PrimaryContainerID, container.StopOptions{Timeout: new(int)}); err != nil {
		t.Fatal(err)
	}
	first, err := cli.ContainerInspect(ctx, up.PrimaryContainerID)
	if err != nil {
		t.Fatal(err)
	}
	expected := []dockercompose.ProjectConflict{{Name: strings.TrimPrefix(first.Name, "/"), ID: first.ID, LocalFolder: workspaces[0]}}
	args := filters.NewArgs(filters.Arg("label", dockercompose.LabelProject+"="+project))
	initial, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil {
		t.Fatal(err)
	}
	configFile := ""
	for _, m := range first.Mounts {
		if m.Destination == "/sample" {
			configFile = m.Source
		}
	}
	before, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	secondSpec, err := spec.Load(configs[1])
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.Up(ctx, dockercompose.UpRequest{Spec: secondSpec, SpecPath: configs[1], LocalWorkspace: workspaces[1], LocalEnv: map[string]string{}, SkipLifecycle: true})
	var conflict *dockercompose.ProjectConflictError
	if !errors.As(err, &conflict) || conflict.Project != project || len(conflict.Conflicts) != 1 || conflict.Conflicts[0] != expected[0] {
		t.Fatalf("strict host up conflict = %+v, %v", conflict, err)
	}
	for _, format := range []string{"text", "json"} {
		result, logs, code := runIdentityUp(ctx, workspaces[1], format, false)
		if code != 1 || result["composeProjectName"] != project {
			t.Fatalf("strict CLI up = %v, code %d, logs %s", result, code, logs)
		}
		assertConflictReport(t, result, logs, expected)
	}
	if data, err := os.ReadFile(configFile); err != nil || !bytes.Equal(data, before) {
		t.Fatalf("refused up changed materialized config: %q, %v", data, err)
	}
	if info, err := cli.ContainerInspect(ctx, first.ID); err != nil || info.State.Running {
		t.Fatalf("refused up changed first container: %+v, %v", info.State, err)
	}
	unchanged, err := cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil || len(unchanged) != len(initial) {
		t.Fatalf("refused up added/removed containers: %v, %v", unchanged, err)
	}
	result, logs, code := runIdentityUp(ctx, workspaces[1], "json", true)
	if code != 0 || result["outcome"] != "success" || result["composeProjectName"] != project {
		t.Fatalf("override up = %v, code %d, logs %s", result, code, logs)
	}
	warned := false
	for _, line := range strings.Split(strings.TrimSpace(logs), "\n") {
		var diagnostic map[string]any
		if json.Unmarshal([]byte(line), &diagnostic) == nil && diagnostic["level"] == "warn" {
			text, _ := diagnostic["text"].(string)
			if strings.Contains(text, "--allow-project-conflict") && strings.Contains(text, first.ID) && strings.Contains(text, expected[0].Name) && strings.Contains(text, workspaces[0]) {
				warned = true
			}
		}
	}
	if !warned {
		t.Fatalf("override did not log conflicts as a warning: %s", logs)
	}
	second, err := cli.ContainerInspect(ctx, result["containerId"].(string))
	if err != nil || second.Config.Labels[dockercompose.LabelLocalFolder] != workspaces[1] {
		t.Fatalf("override did not create second workspace container: %+v, %v", second.Config, err)
	}
	for _, m := range second.Mounts {
		if m.Destination == "/sample" {
			if data, err := os.ReadFile(m.Source); err != nil || string(data) != "second-content" {
				t.Fatalf("replacement config not materialized: %v", err)
			}
		}
	}
	expected = append(expected, dockercompose.ProjectConflict{Name: strings.TrimPrefix(second.Name, "/"), ID: second.ID, LocalFolder: workspaces[1]})
	result, logs, code = runIdentityUp(ctx, workspaces[2], "text", false)
	if code != 1 {
		t.Fatalf("third workspace up code = %d: %v %s", code, result, logs)
	}
	assertConflictReport(t, result, logs, expected)
	unchanged, err = cli.ContainerList(ctx, container.ListOptions{All: true, Filters: args})
	if err != nil || len(unchanged) != len(initial)+1 {
		t.Fatalf("third refused up changed project containers: %v, %v", unchanged, err)
	}
	if data, err := os.ReadFile(configFile); err != nil || !bytes.Equal(data, before) {
		t.Fatalf("third refused up changed materialized config: %q, %v", data, err)
	}
}

func runIdentityUp(ctx context.Context, workspace, format string, allow bool) (map[string]any, string, int) {
	args := []string{"up", "--workspace-folder", workspace, "--skip-post-create", "--log-format", format}
	if allow {
		args = append(args, "--allow-project-conflict")
	}
	var out, logs bytes.Buffer
	code := command.Run(ctx, args, "test", strings.NewReader(""), &out, &logs)
	result := map[string]any{}
	_ = json.Unmarshal(out.Bytes(), &result)
	return result, logs.String(), code
}

func assertConflictReport(t *testing.T, result map[string]any, logs string, expected []dockercompose.ProjectConflict) {
	t.Helper()
	rows, ok := result["projectConflicts"].([]any)
	if !ok || len(rows) != len(expected) {
		t.Fatalf("structured conflict report = %v, want %d containers", result, len(expected))
	}
	for _, want := range expected {
		found := false
		for _, row := range rows {
			fields, _ := row.(map[string]any)
			if fields["id"] == want.ID && fields["name"] == want.Name && fields["localFolder"] == want.LocalFolder {
				found = true
			}
		}
		message, _ := result["message"].(string)
		if !found || !strings.Contains(message, want.ID) || !strings.Contains(message, want.Name) || !strings.Contains(message, want.LocalFolder) || !strings.Contains(logs, want.ID) || !strings.Contains(logs, want.Name) || !strings.Contains(logs, want.LocalFolder) {
			t.Fatalf("conflict omitted name/ID/owner: want %+v; result %v; logs %s", want, result, logs)
		}
	}
}
