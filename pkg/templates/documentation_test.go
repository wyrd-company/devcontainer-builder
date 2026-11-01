// relationships:
//
//	references: [documentation, testdata/documentation-pinned]
package templates

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTemplateDocumentationPinned(t *testing.T) {
	data, err := os.ReadFile("testdata/documentation-pinned.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name            string
			Metadata, Notes *string
			Args            []string
			Project         string
			Exit            int
			Readme          string
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			folder := filepath.Join(row.Project, "sample")
			if err := os.MkdirAll(folder, 0755); err != nil {
				t.Fatal(err)
			}
			old := "old readme"
			for name, value := range map[string]*string{"devcontainer-template.json": row.Metadata, "NOTES.md": row.Notes, "README.md": &old} {
				if value != nil {
					if err := os.WriteFile(filepath.Join(folder, name), []byte(*value), 0644); err != nil {
						t.Fatal(err)
					}
				}
			}
			opts := DocumentationOptions{ProjectFolder: row.Project}
			for i := 0; i < len(row.Args); i++ {
				switch row.Args[i] {
				case "--github-owner":
					i++
					if opts.GitHubOwner == "" {
						opts.GitHubOwner = row.Args[i]
					} else {
						opts.GitHubOwnerValue = &DocumentationValue{Elements: []DocumentationValue{{Text: opts.GitHubOwner}, {Text: row.Args[i]}}}
					}
				case "--github-repo":
					i++
					opts.GitHubRepo = row.Args[i]
				case "--github-owner.toString":
					i++
					opts.GitHubOwnerValue = &DocumentationValue{Properties: map[string]DocumentationValue{"toString": {Text: row.Args[i]}}}
				}
			}
			err := GenerateDocumentation(context.Background(), opts)
			if (err != nil) != (row.Exit != 0) {
				t.Fatalf("error %v, upstream exit %d", err, row.Exit)
			}
			got, err := os.ReadFile(filepath.Join(folder, "README.md"))
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != row.Readme {
				t.Fatalf("README differs\ngot %q\nwant %q", got, row.Readme)
			}
		})
	}
}

func TestTemplateDocumentationFilesystem(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, name := range []string{"visible", ".hidden"} {
		if err := os.MkdirAll(name, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(name, "devcontainer-template.json"), []byte(`{"id":"sample"}`), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile("outside", []byte("preserved"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../outside", "visible/README.md"); err != nil {
		t.Fatal(err)
	}
	if err := GenerateDocumentation(context.Background(), DocumentationOptions{ProjectFolder: "."}); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile("outside"); string(data) != "preserved" {
		t.Fatal("symlink target changed")
	}
	if _, err := os.Stat(".hidden/README.md"); !os.IsNotExist(err) {
		t.Fatal("hidden entry was rendered")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := GenerateDocumentation(ctx, DocumentationOptions{ProjectFolder: "."}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}
