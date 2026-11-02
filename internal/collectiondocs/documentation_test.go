// relationships:
//
// references: [documentation, documentation_jsonc, testdata/documentation/pinned]
package collectiondocs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Golden README bytes were captured from the actual pinned CLI 131882da,
// not from this renderer. See docs/audits/upstream-options.md.
func TestGenerateDocumentationPinnedReadmes(t *testing.T) {
	data, err := os.ReadFile("testdata/documentation/pinned.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name     string
			Metadata *string
			Notes    *string
			Args     []string
			Project  string
			Exit     int
			Readme   string
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			if err := os.MkdirAll("input/sample", 0o755); err != nil {
				t.Fatal(err)
			}
			if row.Metadata != nil {
				documentationWrite(t, "input/sample/devcontainer-feature.json", *row.Metadata)
			}
			if row.Notes != nil {
				documentationWrite(t, "input/sample/NOTES.md", *row.Notes)
			}
			documentationWrite(t, "input/sample/README.md", "old readme")
			opts := DocumentationOptions{ProjectFolder: row.Project, Registry: "ghcr.io", Namespace: "example/collection"}
			seenRegistry := false
			for i := 0; i < len(row.Args); i += 2 {
				switch row.Args[i] {
				case "-r", "--registry":
					if seenRegistry {
						opts.Registry += "," + row.Args[i+1]
					} else {
						opts.Registry = row.Args[i+1]
						seenRegistry = true
					}
				case "--namespace":
					opts.Namespace += "," + row.Args[i+1]
				case "--github-owner":
					opts.GitHubOwner = row.Args[i+1]
				case "--github-repo":
					opts.GitHubRepo = row.Args[i+1]
				}
			}
			err := GenerateDocumentation(context.Background(), opts)
			if (err != nil) != (row.Exit != 0) {
				t.Fatalf("error %v, pinned exit %d", err, row.Exit)
			}
			got, err := os.ReadFile("input/sample/README.md")
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != row.Readme {
				t.Fatalf("README differs from pinned CLI\ngot %q\nwant %q", got, row.Readme)
			}
			if row.Metadata != nil {
				got, err := os.ReadFile("input/sample/devcontainer-feature.json")
				if err != nil || string(got) != *row.Metadata {
					t.Fatal("metadata changed")
				}
			}
		})
	}
}
func documentationWrite(t *testing.T, path, text string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateDocumentationFilesystem(t *testing.T) {
	for _, kind := range []string{"symlink", "symlink-directory", "dangling", "mode", "directory", "empty-directory", "notes-directory", "metadata-directory", "missing", "file", "hidden", "read-error"} {
		t.Run(kind, func(t *testing.T) {
			if kind == "read-error" && runtime.GOOS != "linux" {
				t.Skip("Linux procfs read-error fixture")
			}
			root := t.TempDir()
			folder := filepath.Join(root, "sample")
			if err := os.Mkdir(folder, 0o755); err != nil {
				t.Fatal(err)
			}
			metadata := filepath.Join(folder, "devcontainer-feature.json")
			readme := filepath.Join(folder, "README.md")
			target := filepath.Join(root, "target")
			documentationWrite(t, metadata, `{"id":"sample"}`)
			documentationWrite(t, readme, "old")
			documentationWrite(t, target, "keep")
			opts := DocumentationOptions{ProjectFolder: root, Registry: "ghcr.io", Namespace: "example/collection"}
			var logs strings.Builder
			opts.Logger = func(f string, a ...any) { fmt.Fprintf(&logs, f+"\n", a...) }
			switch kind {
			case "symlink", "symlink-directory", "dangling":
				if err := os.Remove(readme); err != nil {
					t.Fatal(err)
				}
				link := target
				if kind == "symlink-directory" {
					link = filepath.Join(root, "target-directory")
					if err := os.Mkdir(link, 0o755); err != nil {
						t.Fatal(err)
					}
				}
				if kind == "dangling" {
					link += "-new"
				}
				if err := os.Symlink(link, readme); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(readme, 0o755); err != nil {
					t.Fatal(err)
				}
			case "directory", "empty-directory":
				if err := os.Remove(readme); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(readme, 0o755); err != nil {
					t.Fatal(err)
				}
				if kind == "directory" {
					documentationWrite(t, filepath.Join(readme, "keep"), "keep")
				}
			case "notes-directory":
				if err := os.Mkdir(filepath.Join(folder, "NOTES.md"), 0o755); err != nil {
					t.Fatal(err)
				}
			case "metadata-directory":
				if err := os.Remove(metadata); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(metadata, 0o755); err != nil {
					t.Fatal(err)
				}
			case "read-error":
				if err := os.Remove(metadata); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink("/proc/self/mem", metadata); err != nil {
					t.Fatal(err)
				}
			case "missing":
				opts.ProjectFolder = filepath.Join(root, "absent")
			case "file":
				opts.ProjectFolder = target
			case "hidden":
				hidden := filepath.Join(root, ".hidden")
				if err := os.Mkdir(hidden, 0o755); err != nil {
					t.Fatal(err)
				}
				documentationWrite(t, filepath.Join(hidden, "devcontainer-feature.json"), `{"id":"hidden"}`)
			}
			sibling := filepath.Join(root, "zz-sibling")
			if err := os.Mkdir(sibling, 0o755); err != nil {
				t.Fatal(err)
			}
			documentationWrite(t, filepath.Join(sibling, "devcontainer-feature.json"), `{"id":"sibling"}`)
			err := GenerateDocumentation(context.Background(), opts)
			wantError := kind == "directory" || kind == "empty-directory" || kind == "notes-directory" || kind == "missing" || kind == "file"
			if (err != nil) != wantError {
				t.Fatalf("error %v, want error %v", err, wantError)
			}
			if kind == "missing" || kind == "file" {
				return
			}
			if _, err := os.Stat(filepath.Join(sibling, "README.md")); err != nil {
				t.Fatal("sibling did not run after failure", err)
			}
			if kind == "hidden" {
				if _, err := os.Stat(filepath.Join(root, ".hidden/README.md")); !os.IsNotExist(err) {
					t.Fatal("hidden child was generated")
				}
			}
			if kind == "metadata-directory" || kind == "read-error" {
				data, _ := os.ReadFile(readme)
				if string(data) != "old" || !strings.Contains(logs.String(), "Failed to parse") {
					t.Fatal("read failure did not preserve README and diagnostic")
				}
				return
			}
			if wantError {
				return
			}
			info, err := os.Lstat(readme)
			if err != nil {
				t.Fatal(err)
			}
			if (kind == "symlink" || kind == "symlink-directory") && info.Mode()&os.ModeSymlink != 0 {
				t.Fatal("existing README symlink not unlinked")
			}
			if kind == "dangling" && info.Mode()&os.ModeSymlink == 0 {
				t.Fatal("dangling symlink was unlinked")
			}
			if kind != "dangling" && info.Mode().Perm()&0o111 != 0 {
				t.Fatal("old executable mode retained")
			}
			data, _ := os.ReadFile(target)
			if string(data) != "keep" {
				t.Fatal("symlink target changed")
			}
		})
	}
}
func TestGenerateDocumentationCancellation(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sample"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := GenerateDocumentation(ctx, DocumentationOptions{ProjectFolder: root}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
