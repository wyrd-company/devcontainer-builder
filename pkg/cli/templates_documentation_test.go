// relationships:
//
//	references: [templates, features_documentation, ../templates/testdata/documentation-pinned]
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTemplatesDocumentationPinnedCommand(t *testing.T) {
	data, err := os.ReadFile("../templates/testdata/documentation-pinned.json")
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
			var out, logs bytes.Buffer
			code := Run(context.Background(), append([]string{"templates", "generate-docs", "-p", row.Project}, row.Args...), "test", nil, &out, &logs)
			if code != row.Exit {
				t.Fatalf("exit %d want %d: %s", code, row.Exit, logs.String())
			}
			if out.Len() != 0 {
				t.Fatalf("unexpected stdout %q", out.String())
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

func TestTemplatesDocumentationInvocation(t *testing.T) {
	t.Chdir(t.TempDir())
	for _, row := range []struct {
		args []string
		exit int
	}{{nil, 0}, {[]string{"-p"}, 0}, {[]string{"--projectFolder", "."}, 0}, {[]string{"extra"}, 2}, {[]string{"--registry", "example"}, 2}, {[]string{"--log-level", "invalid"}, 2}, {[]string{"-p", "missing"}, 1}, {[]string{"-p", ".", "-p", "."}, 1}, {[]string{"--", "ignored"}, 0}} {
		var out, logs bytes.Buffer
		code := Run(context.Background(), append([]string{"templates", "generate-docs"}, row.args...), "test", nil, &out, &logs)
		if code != row.exit {
			t.Fatalf("%v exit %d want %d: %s", row.args, code, row.exit, logs.String())
		}
	}
}

func TestTemplatesDocumentationValuelessLogLevel(t *testing.T) {
	for _, args := range [][]string{{"--log-level"}, {"--log-level", "--github-owner", "example", "--github-repo", "collection"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			t.Chdir(t.TempDir())
			if err := os.Mkdir("sample", 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile("sample/devcontainer-template.json", []byte(`{"id":"sample"}`), 0644); err != nil {
				t.Fatal(err)
			}
			var out, logs bytes.Buffer
			if code := Run(context.Background(), append([]string{"templates", "generate-docs"}, args...), "test", nil, &out, &logs); code != 0 {
				t.Fatalf("exit %d: %s %s", code, out.String(), logs.String())
			}
			if out.Len() != 0 {
				t.Fatalf("stdout %q", out.String())
			}
			data, err := os.ReadFile("sample/README.md")
			if err != nil {
				t.Fatal(err)
			}
			url := "devcontainer-template.json"
			if len(args) > 1 {
				url = "https://github.com/example/collection/blob/main/./sample/devcontainer-template.json"
			}
			want := "\n# sample\n\n\n\n\n\n\n\n---\n\n_Note: This file was auto-generated from the [devcontainer-template.json](" + url + ").  Add additional notes to a `NOTES.md`._\n"
			if string(data) != want {
				t.Fatalf("README %q want %q", data, want)
			}
			if !strings.Contains(logs.String(), "Generating") {
				t.Fatalf("info diagnostics missing: %s", logs.String())
			}
		})
	}
}
