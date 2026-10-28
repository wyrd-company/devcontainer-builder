// relationships:
//
// references: [features_documentation, options_boolean, ../features/testdata/documentation/deferred-pinned]
package cli

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Publication values must fail only where the actual pinned command uses them.
// The same pinned filesystem oracle also covers empty collections, skips,
// metadata errors, unused link fields, and completed or preserved siblings.
func TestFeaturesDocumentationDeferredValues(t *testing.T) {
	data, err := os.ReadFile("../features/testdata/documentation/deferred-pinned.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name, Stage, ErrorClass string
			Args                    []string
			Before, Files           map[string]string
			Exit                    int
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			for path, raw := range row.Before {
				if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
					t.Fatal(err)
				}
				data, err := hex.DecodeString(raw)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0644); err != nil {
					t.Fatal(err)
				}
			}
			switch row.Stage {
			case "metadata-directory":
				for _, name := range []string{"sample", "zz-sibling"} {
					if err := os.MkdirAll(filepath.Join(name, "devcontainer-feature.json"), 0755); err != nil {
						t.Fatal(err)
					}
				}
			case "notes-directory":
				if err := os.MkdirAll("sample/NOTES.md", 0755); err != nil {
					t.Fatal(err)
				}
			}
			result, _ := invoke(t, row.Exit, append([]string{"features", "generate-docs"}, row.Args...)...)
			if row.Exit != 0 {
				want := row.ErrorClass
				switch want {
				case "parsedVersion.split is not a function":
					want = "version.split is not a function"
				case "Cannot read properties of null":
					want = "cannot read option choice"
				case "extensionsList.map is not a function":
					want = "extensions.map is not a function"
				case "EISDIR":
					want = "is a directory"
				}
				message, _ := result["message"].(string)
				if !strings.Contains(message, want) {
					t.Fatalf("error stage/class %q, pinned %s", message, row.ErrorClass)
				}
			}
			got := map[string]string{}
			if err := filepath.WalkDir(".", func(path string, entry os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() {
					return nil
				}
				data, err := os.ReadFile(path)
				if err == nil {
					got[path] = hex.EncodeToString(data)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if len(got) != len(row.Files) {
				t.Fatalf("filesystem file count %d, pinned %d: %v", len(got), len(row.Files), got)
			}
			for path, want := range row.Files {
				if got[path] != want {
					t.Fatalf("%s differs from pinned command: got %q want %q", path, got[path], want)
				}
			}
		})
	}
}
