// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestIntegrationScriptsInvokeUnifiedCLI(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	tools := t.TempDir()

	stub := `#!/bin/sh
printf '%s\n' "$@" >> "$DCB_INVOCATION_LOG"
printf '\n' >> "$DCB_INVOCATION_LOG"
previous=
for arg do
    if [ "$previous" = "--work-dir" ]; then mkdir -p "$arg"; fi
    previous=$arg
done
printf '{"outcome":"success"}\n'
`
	if err := os.WriteFile(filepath.Join(tools, "go"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, script := range []string{"docker-build-integration.sh", "docker-host-integration.sh"} {
		t.Run(script, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "invocations.jsonl")
			scriptPath := filepath.Join(root, "scripts", script)
			// Register this external executable as a Go test-cache dependency. The
			// assertions still observe its real invocations through the recording stub.
			if _, err := os.ReadFile(scriptPath); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", scriptPath)
			cmd.Dir = root
			cmd.Env = append(os.Environ(), "PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"), "DCB_INVOCATION_LOG="+log, "DCB_ARTIFACT_DIR="+filepath.Join(dir, "artifacts"))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("script: %v\n%s", err, out)
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			sawTest, sawGenerate := false, false
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n\n") {
				args := strings.Split(line, "\n")
				if !slices.Contains(args, "./cmd/dcb") || slices.Contains(args, "./cmd/devcontainer-docker") {
					t.Fatalf("wrong CLI target: %q", args)
				}
				switch args[0] {
				case "run":
					sawGenerate = true
					if !slices.Contains(args, "generate") || !slices.Contains(args, "--workspace-folder") || slices.Contains(args, "--workspace") {
						t.Fatalf("wrong generation invocation: %q", args)
					}
				case "test":
					sawTest = true
					if !slices.Contains(args, "docker_integration") {
						t.Fatalf("missing integration tag: %q", args)
					}
					if slices.Contains(args, "-run") {
						t.Fatalf("integration suite must not narrow test coverage: %q", args)
					}
					if script == "docker-build-integration.sh" {
						for _, target := range []string{"./pkg/builder", "./pkg/buildkit"} {
							if !slices.Contains(args, target) {
								t.Fatalf("missing integration target %s: %q", target, args)
							}
						}
					}
				default:
					t.Fatalf("unexpected go invocation: %q", args)
				}
			}
			if !sawTest || (script == "docker-build-integration.sh" && !sawGenerate) {
				t.Fatalf("missing invocations: %s", data)
			}
		})
	}
}
