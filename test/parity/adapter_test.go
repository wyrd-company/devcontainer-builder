// relationships:
//
//	references: [adapter, runner, README]
package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/cli"
)

func TestAdapterUpOptionsSymmetric(t *testing.T) {
	for _, f := range fixtures {
		t.Run(f.Name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, f.sourceDir(), f.Config)
			if err := os.MkdirAll(filepath.Dir(source), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(source, []byte(`{}`), 0644); err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			record := filepath.Join(root, "args")
			// Record argv at the subprocess boundary; neither CLI nor Docker runs.
			script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$PARITY_ARGS\"\nprintf '%s\\n' '{\"outcome\":\"success\"}'\n"
			for name, body := range map[string]string{"dcb": script, "npx": script, "docker": "#!/bin/sh\nexit 0\n"} {
				if err := os.WriteFile(filepath.Join(bin, name), []byte(body), 0755); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("PATH", bin)
			t.Setenv("PARITY_ARGS", record)
			log, err := os.Create(filepath.Join(root, "run.log"))
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			r := &Runner{root: root, temp: t.TempDir(), runID: "sample-run", log: log, env: os.Environ()}
			var commands [][]string
			for _, side := range []struct {
				name    string
				adapter Adapter
			}{
				{"upstream", UpstreamAdapter{Version: "sample"}},
				{"go", DCBAdapter{Binary: filepath.Join(bin, "dcb")}},
			} {
				observed := r.observe(context.Background(), side.adapter, f, side.name)
				if observed["result"].Error != "" {
					t.Fatal(observed["result"].Error)
				}
				body, err := os.ReadFile(record)
				if err != nil {
					t.Fatal(err)
				}
				args := strings.Split(strings.TrimSuffix(string(body), "\n"), "\n")
				if side.name == "upstream" {
					if len(args) < 2 || args[0] != "--yes" || args[1] != "@devcontainers/cli@sample" {
						t.Fatalf("unexpected npx invocation: %q", args)
					}
					args = args[2:]
				}
				if side.name == "go" {
					// Exercise Cobra with the emitted argv. A missing config stops
					// execution before Docker while proving option parsing succeeded.
					missing := filepath.Join(root, "missing.json")
					var output bytes.Buffer
					code := cli.Run(context.Background(), append(append([]string{}, args...), "--config", missing), "sample", nil, &output, io.Discard)
					if code != 1 || !strings.Contains(output.String(), missing) {
						t.Fatalf("dcb rejected emitted up options: code %d; %s", code, &output)
					}
				}
				workspace := filepath.Join(r.temp, f.Name, side.name, r.workspaceName(f, side.name))
				for i, arg := range args {
					if arg == workspace {
						args[i] = "<workspace>"
					}
				}
				commands = append(commands, args)
			}
			if !reflect.DeepEqual(commands[0], commands[1]) {
				t.Fatalf("up options differ: upstream %q; dcb %q", commands[0], commands[1])
			}
			expected := append([]string{"up", "--workspace-folder", "<workspace>", "--mount-workspace-git-root=false"}, f.UpArgs...)
			if !reflect.DeepEqual(commands[0], expected) {
				t.Fatalf("up options = %q, want %q", commands[0], expected)
			}
		})
	}
}
