// relationships:
//
//	references: [build, cli_test]
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildFailureContextResult(t *testing.T) {
	for _, command := range []string{"build", "up"} {
		for _, mode := range []string{"image", "compose"} {
			for _, phase := range []string{"prepare", "buildkit"} {
				for _, keep := range []bool{false, true} {
					name := command + "/" + mode + "/" + phase + "/remove"
					if keep {
						name = command + "/" + mode + "/" + phase + "/keep"
					}
					t.Run(name, func(t *testing.T) {
						t.Setenv("DOCKER_HOST", "tcp://127.0.0.1:1")
						t.Setenv("BUILDKIT_HOST", "")
						t.Setenv("DOCKER_CONFIG", t.TempDir())
						ws, root := t.TempDir(), t.TempDir()
						config := `{"image":"scratch"}`
						if phase == "prepare" {
							config = `{"image":"scratch","features":{"./missing":{}}}`
						}
						if mode == "compose" {
							compose := "services:\n  app:\n    image: scratch\n"
							if err := os.WriteFile(filepath.Join(ws, "compose.yaml"), []byte(compose), 0o600); err != nil {
								t.Fatal(err)
							}
							config = `{"dockerComposeFile":"compose.yaml","service":"app","workspaceFolder":"/workspace"}`
							if phase == "prepare" {
								config = `{"dockerComposeFile":"compose.yaml","service":"app","workspaceFolder":"/workspace","features":{"./missing":{}}}`
							}
						}
						path := filepath.Join(ws, "devcontainer.json")
						if err := os.WriteFile(path, []byte(config), 0o600); err != nil {
							t.Fatal(err)
						}
						args := []string{command, "--image-name", "example:staging", "--workspace-folder", ws, "--config", path, "--user-data-folder", root, "--buildkit-addr", "tcp://127.0.0.1:1"}
						if keep {
							args = append(args, "--keep")
						}
						result, _ := invoke(t, 1, args...)
						wantError := "failed to list workers"
						if phase == "prepare" {
							wantError = "features:"
						}
						if !strings.Contains(result["message"].(string), wantError) {
							t.Fatalf("wrong failure: %v", result)
						}
						dir, present := result["contextDir"]
						if present != keep {
							t.Fatalf("contextDir presence = %v, keep = %v: %v", present, keep, result)
						}
						if keep {
							if info, err := os.Stat(dir.(string)); err != nil || !info.IsDir() {
								t.Fatalf("contextDir missing: %v", err)
							}
						} else {
							entries, err := os.ReadDir(root)
							if err != nil || len(entries) != 0 {
								t.Fatalf("staging root not empty: %v %v", entries, err)
							}
						}
					})
				}
			}
		}
	}
}

func TestBuildFailureWithoutContextResult(t *testing.T) {
	ws, work := t.TempDir(), t.TempDir()
	config := filepath.Join(ws, "devcontainer.json")
	if err := os.WriteFile(config, []byte(`{"image":"scratch"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Allocation succeeds, but a caller's existing file prevents context creation.
	if err := os.WriteFile(filepath.Join(work, "context"), []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, _ := invoke(t, 1, "build", "--workspace-folder", ws, "--config", config, "--work-dir", work, "--keep")
	if _, present := result["contextDir"]; present {
		t.Fatalf("reported a context directory that was never created: %v", result)
	}
}
