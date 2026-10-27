// relationships:
//
// references: [documentation, documentation_jsonc, testdata/documentation/remediation-pinned]
package features

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// These bytes come from the actual pinned CLI, including malformed UTF-8 and
// JSONC. Operation failures must preserve the old README and still run siblings.
func TestGenerateDocumentationConversionReadmes(t *testing.T) {
	data, err := os.ReadFile("testdata/documentation/remediation-pinned.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name, Metadata, Readme, Sibling string
			Notes                           *string
			Exit                            int
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	decode := func(s string) string {
		t.Helper()
		data, err := hex.DecodeString(s)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			root := t.TempDir()
			folder := filepath.Join(root, "sample")
			sibling := filepath.Join(root, "zz-sibling")
			for _, dir := range []string{folder, sibling} {
				if err := os.Mkdir(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			}
			metadata := decode(row.Metadata)
			documentationWrite(t, filepath.Join(folder, "devcontainer-feature.json"), metadata)
			documentationWrite(t, filepath.Join(folder, "README.md"), "old readme")
			documentationWrite(t, filepath.Join(sibling, "devcontainer-feature.json"), `{"id":"sibling"}`)
			if row.Notes != nil {
				documentationWrite(t, filepath.Join(folder, "NOTES.md"), decode(*row.Notes))
			}
			err := GenerateDocumentation(context.Background(), DocumentationOptions{ProjectFolder: root, Registry: "ghcr.io", Namespace: "example/collection"})
			if (err != nil) != (row.Exit != 0) {
				t.Fatalf("operation error %v, pinned exit %d", err, row.Exit)
			}
			for path, want := range map[string]string{
				filepath.Join(folder, "README.md"):                 decode(row.Readme),
				filepath.Join(sibling, "README.md"):                decode(row.Sibling),
				filepath.Join(folder, "devcontainer-feature.json"): metadata,
			} {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != want {
					t.Fatalf("%s differs from pinned CLI: error %v\ngot %q\nwant %q", filepath.Base(path), err, got, want)
				}
			}
			if row.Notes != nil {
				got, err := os.ReadFile(filepath.Join(folder, "NOTES.md"))
				if err != nil || string(got) != decode(*row.Notes) {
					t.Fatal("notes changed", err)
				}
			}
		})
	}
}
