//go:build docker_integration

// relationships:
//
//	references: [options_terminal, ../host/docker/exec, ../host/dockercompose/exec]
package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerminalDimensionsDockerIntegration(t *testing.T) {
	for _, mode := range []string{"image", "compose"} {
		t.Run(mode, func(t *testing.T) {
			workspace := t.TempDir()
			base := `"image":"alpine:3.20"`
			if mode == "compose" {
				project := sha256.Sum256([]byte(workspace))
				compose := fmt.Sprintf("name: sample-%x\nservices:\n  app:\n    image: alpine:3.20\n", project[:6])
				if err := os.WriteFile(filepath.Join(workspace, "compose.yml"), []byte(compose), 0600); err != nil {
					t.Fatal(err)
				}
				base = `"dockerComposeFile":"compose.yml","service":"app","workspaceFolder":"/"`
			}
			// Cover sequential and captured parallel lifecycle output with the same PTY size.
			config := `{` + base + `,"updateRemoteUserUID":false,"postAttachCommand":{"first":"stty size","second":["sh","-c","stty size"]}}`
			if err := os.WriteFile(filepath.Join(workspace, ".devcontainer.json"), []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			args := []string{"--workspace-folder", workspace}
			t.Cleanup(func() { invoke(t, 0, append([]string{"down"}, args...)...) })
			size := []string{"--terminal-columns", "91", "--terminal-rows", "37"}
			result, logs := invoke(t, 0, append(append([]string{"up"}, args...), size...)...)
			if strings.Count(logs, "37 91") != 2 {
				t.Fatalf("up dimensions: %q", logs)
			}
			id := result["containerId"].(string)
			for _, command := range []string{"run-user-commands", "set-up"} {
				_, logs := invoke(t, 0, append(append([]string{command, "--container-id", id}, args...), size...)...)
				if strings.Count(logs, "37 91") != 2 {
					t.Fatalf("%s dimensions: %q", command, logs)
				}
			}
			// Dimensions size a PTY; they do not opt piped exec into a PTY.
			for _, tty := range []bool{false, true} {
				var stdout, stderr bytes.Buffer
				execArgs := append(append([]string{"exec", "--container-id", id}, args...), size...)
				script := "if [ -t 1 ]; then exit 9; fi; printf pipe"
				want := "pipe"
				if tty {
					execArgs = append(execArgs, "--tty")
					script = "stty size"
					want = "37 91"
				}
				execArgs = append(execArgs, "--", "sh", "-c", script)
				if code := Run(context.Background(), execArgs, "test", nil, &stdout, &stderr); code != 0 || strings.TrimSpace(stdout.String()) != want {
					t.Fatalf("tty=%v code=%d stdout=%q stderr=%q", tty, code, &stdout, &stderr)
				}
			}
			// Sequential lifecycle commands use the size as well.
			config = `{` + base + `,"updateRemoteUserUID":false,"postAttachCommand":"stty size"}`
			if err := os.WriteFile(filepath.Join(workspace, ".devcontainer.json"), []byte(config), 0600); err != nil {
				t.Fatal(err)
			}
			_, logs = invoke(t, 0, append(append([]string{"run-user-commands", "--container-id", id}, args...), size...)...)
			if !strings.Contains(logs, "37 91") {
				t.Fatalf("sequential dimensions: %q", logs)
			}
		})
	}
}
