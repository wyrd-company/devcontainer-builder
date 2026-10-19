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

	"github.com/wyrd-company/devcontainer-builder/internal/testgit"
)

func TestIntegrationScriptsInvokeUnifiedCLI(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"docker-cli-integration.sh", "integration-timeout.sh"} {
		if _, err := os.ReadFile(filepath.Join(root, "scripts", name)); err != nil {
			t.Fatal(err)
		}
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
			cmd.Env = append(testgit.Env(), "PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"), "DCB_INVOCATION_LOG="+log, "DCB_ARTIFACT_DIR="+filepath.Join(dir, "artifacts"))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("script: %v\n%s", err, out)
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := os.ReadFile(filepath.Join(root, "scripts", "cli-integration-shards.tsv"))
			if err != nil {
				t.Fatal(err)
			}
			var wantShards, gotShards []string
			for _, line := range strings.Split(string(manifest), "\n") {
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				_, pattern, ok := strings.Cut(line, "\t")
				if !ok {
					t.Fatalf("invalid shard row: %q", line)
				}
				wantShards = append(wantShards, pattern)
			}
			if len(wantShards) == 0 {
				t.Fatal("no CLI shards")
			}
			sawTest, sawGenerate, sawEngine := false, false, false
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n\n") {
				args := strings.Split(line, "\n")
				if slices.Contains(args, "./cmd/devcontainer-docker") {
					t.Fatalf("wrong CLI target: %q", args)
				}
				switch args[0] {
				case "run":
					sawGenerate = true
					if !slices.Contains(args, "./cmd/dcb") || !slices.Contains(args, "generate") || !slices.Contains(args, "--workspace-folder") || slices.Contains(args, "--workspace") {
						t.Fatalf("wrong generation invocation: %q", args)
					}
				case "test":
					sawTest = true
					if !slices.Contains(args, "docker_integration") {
						t.Fatalf("missing integration tag: %q", args)
					}
					if slices.Contains(args, "./cmd/dcb") {
						index := slices.Index(args, "-run")
						if index < 0 || index+1 >= len(args) {
							t.Fatalf("CLI shard missing selector: %q", args)
						}
						gotShards = append(gotShards, args[index+1])
					} else if script == "docker-build-integration.sh" {
						sawEngine = true
						if slices.Contains(args, "-run") {
							t.Fatalf("engine suite must not narrow test coverage: %q", args)
						}
						for _, target := range []string{"./pkg/builder", "./pkg/buildkit"} {
							if !slices.Contains(args, target) {
								t.Fatalf("missing integration target %s: %q", target, args)
							}
						}
					} else {
						t.Fatalf("wrong CLI target: %q", args)
					}
				default:
					t.Fatalf("unexpected go invocation: %q", args)
				}
			}
			if !slices.Equal(gotShards, wantShards) {
				t.Fatalf("CLI shards = %q, want all shards %q", gotShards, wantShards)
			}
			if !sawTest || (script == "docker-build-integration.sh" && (!sawGenerate || !sawEngine)) {
				t.Fatalf("missing invocations: %s", data)
			}
		})
	}
}
