// relationships:
//
//	references: [builder, generate]
package builder

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestBuildFailureStagingCleanup(t *testing.T) {
	for _, phase := range []string{"prepare", "buildkit"} {
		for _, location := range []string{"user-data", "temporary", "explicit"} {
			for _, keep := range []bool{false, true} {
				name := phase + "/" + location + "/remove"
				if keep {
					name = phase + "/" + location + "/keep"
				}
				t.Run(name, func(t *testing.T) {
					root := t.TempDir()
					opts := Options{Spec: &spec.DevContainer{Image: "alpine"}, SkipImageMetaResolve: true, KeepWorkDir: keep}
					switch location {
					case "user-data":
						opts.UserDataFolder = root
					case "temporary":
						t.Setenv("TMPDIR", root)
					case "explicit":
						opts.WorkDir = filepath.Join(root, "staging")
						if err := os.Mkdir(opts.WorkDir, 0o755); err != nil {
							t.Fatal(err)
						}
						if err := os.WriteFile(filepath.Join(opts.WorkDir, "existing"), []byte("staged"), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					wantError := "--push requires"
					if phase == "prepare" {
						opts.Spec.Features, _ = spec.ParseFeatures([]byte(`{"./missing":{}}`))
						opts.FeatureBaseDir = t.TempDir()
						wantError = "features:"
					} else {
						opts.Push = true
					}
					result, err := New().Build(context.Background(), opts)
					if err == nil || !strings.Contains(err.Error(), wantError) {
						t.Fatalf("wrong failure: %v", err)
					}
					entries, err := os.ReadDir(root)
					if err != nil {
						t.Fatal(err)
					}
					if !keep {
						if len(entries) != 0 {
							t.Fatalf("failed build leaked staging: %v", entries)
						}
						return
					}
					if len(entries) != 1 {
						t.Fatalf("kept staging count = %d", len(entries))
					}
					if result == nil || result.ContextDir == "" {
						t.Fatal("kept failure has no context result")
					}
					if info, err := os.Stat(result.ContextDir); err != nil || !info.IsDir() {
						t.Fatalf("kept context missing: %v", err)
					}
				})
			}
		}
	}
}

func TestBuildFailureBeforeStagingPreservesContent(t *testing.T) {
	for _, failure := range []string{"invalid-spec", "mkdir"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			sentinel := filepath.Join(root, "existing")
			if err := os.WriteFile(sentinel, []byte("preserve"), 0o600); err != nil {
				t.Fatal(err)
			}
			opts := Options{Spec: &spec.DevContainer{Image: "alpine"}, WorkDir: sentinel}
			if failure == "invalid-spec" {
				opts.SpecPath = filepath.Join(root, "missing.json")
				opts.Spec = nil
				opts.WorkDir = root
			}
			if _, err := New().Build(context.Background(), opts); err == nil {
				t.Fatal("expected failure")
			}
			if content, err := os.ReadFile(sentinel); err != nil || string(content) != "preserve" {
				t.Fatalf("caller content changed: %q %v", content, err)
			}
		})
	}
}

func TestBuildFailurePreservesSiblingStaging(t *testing.T) {
	root := t.TempDir()
	sibling := filepath.Join(root, "devcontainer-builder-existing")
	if err := os.Mkdir(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(sibling, "existing")
	if err := os.WriteFile(sentinel, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := New().Build(context.Background(), Options{
		Spec: &spec.DevContainer{Image: "alpine"}, UserDataFolder: root,
		SkipImageMetaResolve: true, Push: true,
	})
	if err == nil || !strings.Contains(err.Error(), "--push requires") {
		t.Fatalf("wrong failure: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(sibling) {
		t.Fatalf("sibling staging changed or own staging leaked: %v %v", entries, err)
	}
	if content, err := os.ReadFile(sentinel); err != nil || string(content) != "preserve" {
		t.Fatalf("sibling content changed: %q %v", content, err)
	}
}
