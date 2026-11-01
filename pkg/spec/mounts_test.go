// relationships:
//
//	references: [mounts]
package spec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateMounts(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"bind", `{"type":"bind","source":"/host/data","target":"/data"}`, ""},
		{"volume", `{"type":"volume","target":"/data"}`, ""},
		{"object_readonly_target", `{"type":"bind","source":"/host/data","target":"/data,readonly"}`, ""},
		{"object_named_volume", `{"type":"volume","source":"sample-cache","target":"/cache"}`, ""},
		{"readonly_true", `{"type":"bind","target":"/data","readonly":true}`, `unsupported object property "readonly"`},
		{"readOnly_false", `{"type":"bind","target":"/data","readOnly":false}`, `unsupported object property "readOnly"`},
		{"empty-strings", `{"type":"bind","source":"","target":""}`, ""},
		{"readonly-string", `"type=bind,source=/host/data,target=/data,readonly"`, ""},
		{"readOnly", `{"type":"bind","target":"/data","readOnly":true}`, `unsupported object property "readOnly"`},
		{"readonly", `{"type":"bind","target":"/data","readonly":false}`, `unsupported object property "readonly"`},
		{"ro", `{"type":"bind","target":"/data","ro":true}`, `unsupported object property "ro"`},
		{"external", `{"type":"volume","target":"/data","external":true}`, `unsupported object property "external"`},
		{"alias", `{"type":"bind","target":"/data","src":"/host/data"}`, `unsupported object property "src"`},
		{"unknown", `{"type":"bind","target":"/data","typo":"value"}`, `unsupported object property "typo"`},
		{"type-missing", `{"target":"/data"}`, ".type: required"},
		{"type-invalid", `{"type":"tmpfs","target":"/data"}`, ".type: required"},
		{"type-null", `{"type":null,"target":"/data"}`, ".type: expected a string"},
		{"target-missing", `{"type":"bind"}`, ".target: required"},
		{"target-number", `{"type":"bind","target":42}`, ".target: expected a string"},
		{"source-null", `{"type":"bind","target":"/data","source":null}`, ".source: expected a string"},
		{"null", `null`, "expected a mount string or object"},
		{"number", `42`, "expected a mount string or object"},
		{"array", `[]`, "expected a mount string or object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "devcontainer.json")
			data := []byte(`{"image":"alpine","mounts":[` + tc.input + `]}`)
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			var dc DevContainer
			if err := json.Unmarshal(data, &dc); err != nil {
				t.Fatal(err)
			}
			for _, err := range []error{dc.Validate(), func() error { _, err := Load(path); return err }()} {
				if tc.want == "" {
					if err != nil {
						t.Fatal(err)
					}
				} else if err == nil || !strings.Contains(err.Error(), tc.want) {
					t.Fatalf("error=%v, want %q", err, tc.want)
				}
				if strings.Contains(tc.want, "unsupported object property") && (err == nil || !strings.Contains(err.Error(), ",readonly")) {
					t.Fatalf("missing portable syntax guidance: %v", err)
				}
			}
		})
	}
}
