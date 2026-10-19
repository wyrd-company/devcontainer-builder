//go:build darwin || freebsd || linux || netbsd || openbsd

// relationships:
//
//	references: [options_exporters, cli]
package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/containerd/console"
)

func exporterPTY(t *testing.T) (console.Console, *os.File) {
	t.Helper()
	master, path, err := console.NewPty()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { master.Close() })
	slave, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { slave.Close() })
	return master, slave
}

func TestBuildExporterTerminalRefusal(t *testing.T) {
	for _, raw := range []string{"-", "type=tar", "type=tar,dest=-", "type=oci", "type=oci,dest=-", "type=docker,dest=-"} {
		t.Run(raw, func(t *testing.T) {
			master, slave := exporterPTY(t)
			dir := t.TempDir()
			config := filepath.Join(dir, "devcontainer.json")
			if err := os.WriteFile(config, []byte(`{"build":{"dockerfile":"Dockerfile"}}`), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0600); err != nil {
				t.Fatal(err)
			}
			var stderr bytes.Buffer
			code := Run(context.Background(), []string{"build", "--workspace-folder", dir, "--config", config, "--skip-features", "--output", raw}, "test", bytes.NewReader(nil), slave, &stderr)
			if code != 2 {
				t.Fatalf("terminal archive must be invalid invocation: exit=%d stderr=%s", code, &stderr)
			}
			slave.Close() // EOF also makes a missing JSON result fail without waiting.
			line, err := bufio.NewReader(master).ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			var result map[string]any
			if err := json.Unmarshal([]byte(line), &result); err != nil {
				t.Fatalf("terminal must receive JSON without archive bytes: %q: %v", line, err)
			}
			if result["outcome"] != "error" || !strings.Contains(result["message"].(string), "refusing to write to console") {
				t.Fatalf("terminal refusal result: %s", line)
			}
			if stderr.Len() != 0 {
				t.Fatalf("invalid invocation stderr: %s", &stderr)
			}
		})
	}
}

func TestBuildExporterTerminalAllowsOtherDestinations(t *testing.T) {
	_, slave := exporterPTY(t)
	for _, raw := range []string{"type=tar,dest=archive.tar", "type=oci,dest=archive.tar", "type=docker", "type=docker,dest=archive.tar", "type=oci,tar=false,dest=layout", "type=local,dest=result", "type=image", "type=cacheonly"} {
		a := &application{stdout: slave}
		if err := a.prepareExporter(raw); err != nil || a.exportStdout {
			t.Errorf("%s: stdout=%v err=%v", raw, a.exportStdout, err)
		}
	}
}
