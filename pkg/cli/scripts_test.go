// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package cli

import (
	"encoding/json"
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
	stub := `#!/usr/bin/env python3
import json, os, sys
with open(os.environ["DCB_INVOCATION_LOG"], "a") as log:
    log.write(json.dumps(sys.argv[1:]) + "\n")
if "--work-dir" in sys.argv:
    os.makedirs(sys.argv[sys.argv.index("--work-dir") + 1], exist_ok=True)
print('{"outcome":"success"}')
`
	if err := os.WriteFile(filepath.Join(tools, "go"), []byte(stub), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, script := range []string{"docker-build-integration.sh", "docker-host-integration.sh"} {
		t.Run(script, func(t *testing.T) {
			dir := t.TempDir()
			log := filepath.Join(dir, "invocations.jsonl")
			cmd := exec.Command("bash", filepath.Join(root, "scripts", script))
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
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				var args []string
				if err := json.Unmarshal([]byte(line), &args); err != nil {
					t.Fatal(err)
				}
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
					if script == "docker-host-integration.sh" {
						run := slices.Index(args, "-run")
						if run < 0 || run+1 >= len(args) {
							t.Fatalf("missing test selection: %q", args)
						}
						for _, name := range []string{"TestDockerHost", "TestDockerComposeCLI", "TestDockerLifecycleCLI"} {
							if !strings.Contains(args[run+1], name) {
								t.Fatalf("missing %s: %q", name, args)
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
