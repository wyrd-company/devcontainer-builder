// relationships:
//
//	references: [feature_dependencies, inventory_test]
package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestFeatureDependenciesCommand(t *testing.T) {
	flags := RegisteredOptions()["features resolve-dependencies"]
	for _, name := range []string{"workspace-folder", "log-level", "help", "version"} {
		if !slices.Contains(flags, name) {
			t.Fatalf("missing %s: %v", name, flags)
		}
	}
	if slices.Contains(flags, "out") {
		t.Fatal("staging flag inherited")
	}
	w := t.TempDir()
	d := filepath.Join(w, ".devcontainer", "sample")
	if err := os.MkdirAll(d, 0755); err != nil {
		t.Fatal(err)
	}
	for p, body := range map[string]string{filepath.Join(w, ".devcontainer", "devcontainer.json"): `{"features":{"./sample":true}}`, filepath.Join(d, "devcontainer-feature.json"): `{"id":"sample"}`} {
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	for _, level := range []string{"error", "info", "debug", "trace"} {
		var out, stderr bytes.Buffer
		code := Run(context.Background(), []string{"features", "resolve-dependencies", "--workspace-folder", w, "--log-level", level}, "test", strings.NewReader(""), &out, &stderr)
		if code != 0 || !strings.HasPrefix(out.String(), "flowchart\n") || !strings.HasSuffix(out.String(), "\n}\n") || !strings.Contains(out.String(), `"options": true`) {
			t.Fatal(code, out.String(), stderr.String())
		}
	}
	for _, extra := range [][]string{{"unexpected"}, {"--log-level", "off"}, {"--out", "unused"}} {
		var out, stderr bytes.Buffer
		code := Run(context.Background(), append([]string{"features", "resolve-dependencies"}, extra...), "test", strings.NewReader(""), &out, &stderr)
		if code != 2 {
			t.Fatal(code, out.String(), stderr.String())
		}
	}
}
