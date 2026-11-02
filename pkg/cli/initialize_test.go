// relationships:
//
//	references: [initialize, host, ../../reference/cli/src/spec-node/configContainer]
package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitializeUpBeforeContainerWork(t *testing.T) {
	workspace := t.TempDir()
	config := filepath.Join(workspace, "devcontainer.json")
	raw := map[string]any{"image": "alpine", "initializeCommand": []string{"sh", "-c", "printf '%s' \"$PWD:$SAMPLE_VALUE:$1\" > marker; printf host-log; exit 7", "sh", "${localWorkspaceFolder}"}, "containerEnv": map[string]string{"SAMPLE_VALUE": "container-value"}, "remoteEnv": map[string]string{"SAMPLE_VALUE": "remote-value"}}
	data, _ := json.Marshal(raw)
	if err := os.WriteFile(config, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SAMPLE_VALUE", "host-value")
	t.Setenv("DOCKER_HOST", "unix:///nonexistent/initialize-test.sock")
	for range 2 {
		result, logs := invoke(t, 1, "up", "--workspace-folder", workspace, "--config", config, "--skip-post-create", "--mount-workspace-git-root=false")
		if !strings.Contains(result["message"].(string), "initializeCommand") || !strings.Contains(logs, "host-log") {
			t.Fatalf("result=%v logs=%s", result, logs)
		}
		marker, err := os.ReadFile(filepath.Join(workspace, "marker"))
		if err != nil || string(marker) != workspace+":host-value:"+workspace {
			t.Fatalf("marker=%q err=%v", marker, err)
		}
		if err := os.Remove(filepath.Join(workspace, "marker")); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInitializeDoesNotRunOnOtherCommands(t *testing.T) {
	workspace := t.TempDir()
	config := filepath.Join(workspace, "devcontainer.json")
	marker := filepath.Join(workspace, "marker")
	data, _ := json.Marshal(map[string]any{"image": "alpine", "initializeCommand": "touch marker; exit 7"})
	if err := os.WriteFile(config, data, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOCKER_HOST", "unix:///nonexistent/initialize-test.sock")
	invoke(t, 0, "read-configuration", "--workspace-folder", workspace, "--config", config, "--mount-workspace-git-root=false")
	invoke(t, 1, "build", "--workspace-folder", workspace, "--config", config)
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("initialization ran outside up: %v", err)
	}
}

func TestInitializeReaderRejectsNestedObjects(t *testing.T) {
	workspace := t.TempDir()
	config := filepath.Join(workspace, "devcontainer.json")
	if err := os.WriteFile(config, []byte(`{"image":"alpine","initializeCommand":{"outer":{"inner":"echo sample"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	result, _ := invoke(t, 1, "read-configuration", "--workspace-folder", workspace, "--config", config, "--mount-workspace-git-root=false")
	if !strings.Contains(result["message"].(string), "parallel command") {
		t.Fatal(result)
	}
}
