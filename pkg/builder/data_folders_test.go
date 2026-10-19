// relationships:
//
// references: [builder, generate, ../../internal/datafolder/staging]
package builder

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestUserDataFolderBuildStaging(t *testing.T) {
	for _, mode := range []string{"generate", "build-context"} {
		t.Run(mode, func(t *testing.T) {
			parent := t.TempDir()
			sentinel := filepath.Join(parent, "existing")
			if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
				t.Fatal(err)
			}
			opts := Options{Spec: &spec.DevContainer{Image: "alpine:3.20"}, UserDataFolder: parent, SkipImageMetaResolve: true}
			b := New()
			var contextDir string
			if mode == "generate" {
				result, err := b.Generate(context.Background(), opts)
				if err != nil {
					t.Fatal(err)
				}
				contextDir = result.ContextDir
			} else {
				_, _, dir, _, _, _, _, _, _, err := b.prepareBuildContext(context.Background(), opts, func(string, ...any) {})
				if err != nil {
					t.Fatal(err)
				}
				contextDir = dir
			}
			if !strings.HasPrefix(contextDir, parent+string(filepath.Separator)) {
				t.Fatalf("staged outside selected folder: %s", contextDir)
			}
			if info, err := os.Stat(contextDir); err != nil || !info.IsDir() {
				t.Fatalf("staging context missing: %v", err)
			}
			if content, err := os.ReadFile(sentinel); err != nil || string(content) != "keep" {
				t.Fatal("existing content changed")
			}
		})
	}
}
