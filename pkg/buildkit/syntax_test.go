// relationships:
//
//	references: [syntax, dockerfile]
package buildkit

import (
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/buildkit/frontend/dockerfile/parser"
	"github.com/tonistiigi/fsutil"
)

func TestDockerfileSyntaxOmission(t *testing.T) {
	for _, input := range []string{"# syntax=registry.example/frontend:1\n# escape=`\nFROM alpine\n", "# SyNtAx = registry.example/frontend:1\r\nFROM alpine\r\n", "#!/bin/sh\n# syntax=registry.example/frontend:1\nFROM alpine\n", "# ordinary comment\n# syntax=leave-this-comment\nFROM alpine\n", "FROM alpine\n"} {
		t.Run(strings.Split(input, "\n")[0], func(t *testing.T) {
			dir := t.TempDir()
			files := map[string]string{"Customfile": input, "Customfile.dockerignore": "excluded\n", "payload": "context-content"}
			for name, data := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0644); err != nil {
					t.Fatal(err)
				}
			}
			for _, omit := range []bool{false, true, false} {
				mounts := map[string]fsutil.FS{}
				if err := addDockerfileMounts(mounts, &DockerfileBuild{ContextDir: dir, Filename: "Customfile", OmitSyntaxDirective: omit}); err != nil {
					t.Fatal(err)
				}
				read := func(mount, name string) string {
					f, err := mounts[mount].Open(name)
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					data, err := io.ReadAll(f)
					if err != nil {
						t.Fatal(err)
					}
					return string(data)
				}
				got := read(dockerfileDockerfileLocal, "Customfile")
				if !omit {
					if got != input {
						t.Fatalf("default=%q", got)
					}
				}
				directive := strings.HasPrefix(input, "# syntax=") || strings.HasPrefix(input, "# SyNtAx") || strings.HasPrefix(input, "#!")
				if omit && directive && strings.Contains(strings.ToLower(got), "syntax") {
					t.Fatalf("syntax retained: %q", got)
				}
				if omit && !directive && got != input {
					t.Fatalf("ordinary comment changed: %q", got)
				}
				if strings.Contains(input, "# escape=`") && !strings.Contains(got, "# escape=`") {
					t.Fatal("escape directive removed")
				}
				if strings.Contains(input, "# escape=`") {
					parsed, err := parser.Parse(strings.NewReader(got))
					if err != nil {
						t.Fatal(err)
					}
					if parsed.EscapeToken != '`' {
						t.Fatalf("escape directive no longer applies: %q", got)
					}
				}
				count := 0
				err := mounts[dockerfileDockerfileLocal].Walk(context.Background(), "", func(name string, d fs.DirEntry, err error) error {
					if err != nil {
						return err
					}
					if name != "Customfile" {
						return nil
					}
					count++
					info, err := d.Info()
					if err != nil {
						return err
					}
					if info.Size() != int64(len(got)) {
						t.Errorf("stat size %d != served bytes %d", info.Size(), len(got))
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if count != 1 {
					t.Fatalf("walk entries=%d", count)
				}
				if read(dockerfileDockerfileLocal, "Customfile.dockerignore") != files["Customfile.dockerignore"] || read(dockerfileContextLocal, "Customfile") != input || read(dockerfileContextLocal, "payload") != "context-content" {
					t.Fatal("context or ignore file changed")
				}
			}
			original, err := os.ReadFile(filepath.Join(dir, "Customfile"))
			if err != nil {
				t.Fatal(err)
			}
			if string(original) != input {
				t.Fatal("authored file changed")
			}
		})
	}
}

func TestDockerfileSyntaxOmissionLowercaseDefault(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dockerfile"), []byte("# syntax=invalid[\nFROM alpine\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mounts := map[string]fsutil.FS{}
	if err := addDockerfileMounts(mounts, &DockerfileBuild{ContextDir: dir, OmitSyntaxDirective: true}); err != nil {
		t.Fatal(err)
	}
	file, err := mounts[dockerfileDockerfileLocal].Open("dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "FROM alpine\n" {
		t.Fatalf("lowercase default=%q", data)
	}
}
