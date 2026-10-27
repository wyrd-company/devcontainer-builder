// relationships:
//
// references: [features_documentation, options_boolean, testdata/documentation_flags]
package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Golden bytes and invocation outcomes were captured from the pinned command.
// dcb retains its established exit 2 for invocation errors.
func TestFeaturesDocumentationOptionalStringValues(t *testing.T) {
	data, err := os.ReadFile("testdata/documentation_flags.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Cases []struct {
			Name, Readme string
			Args         []string
			Exit         int
		}
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, row := range fixture.Cases {
		t.Run(row.Name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			if err := os.Mkdir("sample", 0o755); err != nil {
				t.Fatal(err)
			}
			metadata := filepath.Join("sample", "devcontainer-feature.json")
			if err := os.WriteFile(metadata, []byte(`{"id":"sample"}`), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile("sample/README.md", []byte("old readme"), 0o644); err != nil {
				t.Fatal(err)
			}
			invoke(t, row.Exit, append([]string{"features", "generate-docs"}, row.Args...)...)
			got, err := os.ReadFile("sample/README.md")
			if err != nil || string(got) != row.Readme {
				t.Fatalf("README differs from pinned CLI: %v\ngot %q\nwant %q", err, got, row.Readme)
			}
			got, err = os.ReadFile(metadata)
			if err != nil || string(got) != `{"id":"sample"}` {
				t.Fatal("metadata changed", err)
			}
		})
	}
}
