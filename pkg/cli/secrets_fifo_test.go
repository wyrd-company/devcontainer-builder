//go:build unix

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
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A pipe can be read only once. The command reads --secrets-file from a
// FIFO with a single writer: a second read would block, so the command would
// not reach the configuration error.
func TestSecretsFileFromPipe(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "missing.sock"))
	fifo := filepath.Join(t.TempDir(), "secrets")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skipf("mkfifo: %v", err)
	}
	go func() {
		f, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		_, _ = f.WriteString(`{"SAMPLE": "sample-piped-secret"}`)
		_ = f.Close()
	}()
	type outcome struct {
		code   int
		stdout string
	}
	done := make(chan outcome, 1)
	go func() {
		code, stdout, _ := runCLI("up", "--secrets-file", fifo, "--config", filepath.Join(t.TempDir(), "missing.json"))
		done <- outcome{code, stdout}
	}()
	select {
	case got := <-done:
		var result map[string]any
		if err := json.Unmarshal([]byte(got.stdout), &result); err != nil {
			t.Fatal(err)
		}
		message, _ := result["message"].(string)
		if got.code != 1 || message == "" || strings.HasPrefix(message, "read secrets file") {
			t.Fatalf("exit %d, message %q; want the configuration error after one read", got.code, message)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the command read the secrets pipe twice and blocked")
	}
}
