// relationships:
//
//	references: [merge, ../spec/spec]
package metadata

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestMergedComposeFilePresence(t *testing.T) {
	for _, tc := range []struct {
		name    string
		files   spec.StringOrSlice
		present bool
	}{
		{"omitted", nil, false}, {"discovery", spec.StringOrSlice{}, true}, {"explicit", spec.StringOrSlice{"compose.yml"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dc := &spec.DevContainer{DockerComposeFile: tc.files}
			if tc.present {
				dc.Service = "app"
				dc.WorkspaceFolder = "/workspace"
			}
			for name, value := range map[string]any{"config": dc, "merged": Merge(dc, Collect(nil, nil, dc))} {
				data, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(data, &fields); err != nil {
					t.Fatal(err)
				}
				raw, exists := fields["dockerComposeFile"]
				if exists != tc.present {
					t.Fatalf("%s presence=%v want %v: %s", name, exists, tc.present, data)
				}
				if tc.files != nil && len(tc.files) == 0 && string(raw) != "[]" {
					t.Fatalf("%s empty array lost: %s", name, data)
				}
				path := filepath.Join(t.TempDir(), "devcontainer.json")
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				loaded, err := spec.Load(path)
				if err != nil {
					t.Fatal(err)
				}
				if tc.present && loaded.Mode() != spec.ModeCompose {
					t.Fatalf("%s lost Compose mode", name)
				}
				if len(loaded.DockerComposeFile) != len(tc.files) {
					t.Fatalf("%s lost explicit files", name)
				}
			}
		})
	}
}
