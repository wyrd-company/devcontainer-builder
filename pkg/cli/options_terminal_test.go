// relationships:
//
//	references: [options_terminal, docs_test]
package cli

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTerminalOptions(t *testing.T) {
	for _, command := range []string{"exec", "up", "set-up", "run-user-commands", "read-configuration"} {
		t.Run(command, func(t *testing.T) {
			root := (&application{}).command()
			cmd, _, err := root.Find([]string{command})
			if err != nil {
				t.Fatal(err)
			}
			for _, flag := range []string{"terminal-columns", "terminal-rows"} {
				f := cmd.Flags().Lookup(flag)
				if f == nil || f.Value.Type() != "float64" || f.DefValue != "0" || f.Hidden {
					t.Fatalf("%s: %+v", flag, f)
				}
				for _, value := range []string{"91", "0", "invalid"} {
					args := []string{command, "--" + flag, value}
					if command == "set-up" {
						args = append(args, "--container-id", "sample")
					}
					if command == "exec" {
						args = append(args, "--", "true")
					}
					var out bytes.Buffer
					if code := Run(context.Background(), args, "test", nil, &out, io.Discard); code != 2 {
						t.Fatalf("%v: code=%d output=%s", args, code, &out)
					}
					if value != "invalid" && !strings.Contains(out.String(), "missing [terminal-") {
						t.Fatalf("pair error missing: %s", &out)
					}
				}
			}
		})
	}
}

func TestTerminalReadConfiguration(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, ".devcontainer.json"), []byte(`{"image":"alpine:3.20"}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		flags []string
		want  *[2]uint
	}{
		{name: "default"},
		{name: "columns-rows", flags: []string{"--terminal-columns", "91", "--terminal-rows", "37"}, want: &[2]uint{37, 91}},
		{name: "numeric", flags: []string{"--terminal-columns", "91.5", "--terminal-rows", "3.7e1"}, want: &[2]uint{37, 91}},
		{name: "zero-columns", flags: []string{"--terminal-columns", "0", "--terminal-rows", "37"}},
		{name: "zero-rows", flags: []string{"--terminal-columns", "91", "--terminal-rows", "0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &application{stdout: io.Discard, stderr: io.Discard, result: map[string]any{}}
			root := a.command()
			root.SetArgs(append([]string{"read-configuration", "--workspace-folder", workspace}, tc.flags...))
			if err := root.Execute(); err != nil {
				t.Fatal(err)
			}
			if (a.terminalSize == nil) != (tc.want == nil) || tc.want != nil && *a.terminalSize != *tc.want {
				t.Fatalf("size=%v want=%v", a.terminalSize, tc.want)
			}
		})
	}
}
