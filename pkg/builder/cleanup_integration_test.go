//go:build docker_integration

// relationships:
//
//	references: [builder, engine_integration_test]
package builder

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildStagingSuccessDockerIntegration(t *testing.T) {
	ctx := context.Background()
	engine := requireEngine(t, ctx)
	for _, keep := range []bool{false, true} {
		name := "remove"
		if keep {
			name = "keep"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			result, err := New().Build(ctx, Options{
				Workspace: filepath.Join(repoRoot(t), "testdata", "smoke", "dockerfile"),
				Docker:    engine, UserDataFolder: root, KeepWorkDir: keep,
				Progress: "plain", Stdout: io.Discard, Stderr: testLogWriter{t},
			})
			if err != nil {
				t.Fatal(err)
			}
			entries, err := os.ReadDir(root)
			if err != nil {
				t.Fatal(err)
			}
			if !keep {
				if len(entries) != 0 {
					t.Fatalf("successful build leaked staging: %v", entries)
				}
			} else if info, err := os.Stat(result.ContextDir); err != nil || !info.IsDir() {
				t.Fatalf("kept success context missing: %v", err)
			}
		})
	}
}
