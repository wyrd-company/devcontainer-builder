package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLI_Version(t *testing.T) {
	cmd := exec.Command("go", "run", ".", "version")
	cmd.Dir = "."
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go run . version: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), `"version":"dev"`) {
		t.Fatalf("version output = %q, want dcb version", out)
	}
}

func TestCLI_GenerateSmokeFixtures(t *testing.T) {
	cases := []struct {
		name         string
		fixture      string
		wantDocker   []string
		wantContext  []string
		skipFeatures bool
	}{
		{
			name:    "image",
			fixture: "image",
			wantDocker: []string{
				"FROM alpine:3.20",
				"ENV DCB_SMOKE=image",
			},
		},
		{
			name:    "dockerfile",
			fixture: "dockerfile",
			wantDocker: []string{
				"FROM <unresolved-base-build-required>",
				"ENV DCB_SMOKE=dockerfile",
			},
		},
		{
			name:    "features",
			fixture: "features",
			wantDocker: []string{
				"FROM alpine:3.20",
				"COPY --chown=root:root _features/",
				" /tmp/dev-container-features/",
				`MARKER="/usr/local/share/dcb-smoke-feature"`,
				"./devcontainer-features-install.sh",
			},
			wantContext: []string{"_features/*/devcontainer-feature.json", "_features/*/install.sh"},
		},
	}

	root := repoRoot(t)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workDir := t.TempDir()
			args := []string{
				"run", ".", "generate",
				"--workspace-folder", filepath.Join(root, "testdata", "smoke", tc.fixture),
				"--work-dir", workDir,
			}
			if tc.skipFeatures {
				args = append(args, "--skip-features")
			}
			cmd := exec.Command("go", args...)
			cmd.Dir = filepath.Join(root, "cmd", "dcb")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
			}

			dockerfilePath := filepath.Join(workDir, "Dockerfile.devcontainer-builder")
			got, err := os.ReadFile(dockerfilePath)
			if err != nil {
				t.Fatalf("read generated Dockerfile: %v\ncommand output:\n%s", err, out)
			}
			for _, want := range tc.wantDocker {
				if !strings.Contains(string(got), want) {
					t.Errorf("generated Dockerfile missing %q:\n%s", want, got)
				}
			}
			if strings.Contains(string(got), "WORKDIR") {
				t.Errorf("generated Dockerfile replaces the base WORKDIR:\n%s", got)
			}
			for _, rel := range tc.wantContext {
				matches, err := filepath.Glob(filepath.Join(workDir, "context", rel))
				if err != nil {
					t.Fatalf("bad context glob %s: %v", rel, err)
				}
				if len(matches) == 0 {
					t.Errorf("generated context missing %s", rel)
				}
			}
		})
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}
