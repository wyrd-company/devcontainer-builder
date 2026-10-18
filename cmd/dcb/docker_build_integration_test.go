//go:build docker_integration

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDockerBuildIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Docker integration test in short mode")
	}
	requireDocker(t)

	root := repoRoot(t)
	bin := buildBinary(t)
	tagBase := fmt.Sprintf("devcontainer-builder/integration-%d", time.Now().UnixNano())

	cases := []struct {
		name      string
		fixture   string
		assertion func(*testing.T, string)
	}{
		{
			name:    "buildkit-image",
			fixture: "image",
			assertion: func(t *testing.T, tag string) {
				cfg := inspectImage(t, tag)
				assertEnv(t, cfg.Env, "DCB_SMOKE=image")
				if cfg.WorkingDir != "/workspaces/smoke-image" {
					t.Fatalf("working dir = %q, want /workspaces/smoke-image", cfg.WorkingDir)
				}
				if cfg.User != "nobody" {
					t.Fatalf("user = %q, want nobody", cfg.User)
				}
				runDocker(t, "run", "--rm", tag, "sh", "-lc", "test \"$DCB_SMOKE\" = image && test \"$(id -un)\" = nobody")
			},
		},
		{
			name:    "buildkit-dockerfile",
			fixture: "dockerfile",
			assertion: func(t *testing.T, tag string) {
				runDocker(t, "run", "--rm", tag, "sh", "-lc", "grep -qx dockerfile-smoke /usr/local/share/dcb-smoke-dockerfile")
			},
		},
		{
			name:    "buildkit-feature",
			fixture: "features",
			assertion: func(t *testing.T, tag string) {
				runDocker(t, "run", "--rm", tag, "sh", "-lc", "grep -qx feature-smoke /usr/local/share/dcb-smoke-feature")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tag := tagBase + "-" + tc.name + ":latest"
			t.Cleanup(func() {
				removeImage(t, tag)
			})

			args := []string{
				"build",
				"--workspace-folder", filepath.Join(root, "testdata", "smoke", tc.fixture),
				"--image-name", tag,
				"--load",
				"--progress", "plain",
			}

			cmd := exec.Command(bin, args...)
			var diagnostics bytes.Buffer
			cmd.Stderr = &diagnostics
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("build: %v, stdout=%s stderr=%s", err, out, diagnostics.String())
			}
			var result map[string]any
			if err := json.Unmarshal(out, &result); err != nil || result["outcome"] != "success" || bytes.Count(out, []byte("\n")) != 1 {
				t.Fatalf("invalid build result: %s (%v)", out, err)
			}
			names, ok := result["imageName"].([]any)
			if !ok || len(names) != 1 || names[0] != tag {
				t.Fatalf("build imageName=%v, want %s", result["imageName"], tag)
			}

			tc.assertion(t, tag)
		})
	}
}

func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "dcb")
	runCommand(t, "go", "build", "-o", bin, ".")
	return bin
}

func requireDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		skipOrFail(t, "docker is required for Docker integration tests: %v", err)
	}
	cmd := exec.Command("docker", "info")
	if out, err := cmd.CombinedOutput(); err != nil {
		skipOrFail(t, "docker daemon is required for Docker integration tests: %v\n%s", err, out)
	}
}

func skipOrFail(t *testing.T, format string, args ...any) {
	t.Helper()
	if os.Getenv("CI") == "true" {
		t.Fatalf(format, args...)
	}
	t.Skipf(format, args...)
}

func inspectImage(t *testing.T, tag string) imageConfig {
	t.Helper()
	out := runCommand(t, "docker", "image", "inspect", tag)
	writeArtifact(t, safeArtifactName(tag)+"-image-inspect.json", out)
	var images []struct {
		Config imageConfig `json:"Config"`
	}
	if err := json.Unmarshal(out, &images); err != nil {
		t.Fatalf("parse docker image inspect output: %v\n%s", err, out)
	}
	if len(images) != 1 {
		t.Fatalf("docker image inspect returned %d images, want 1", len(images))
	}
	return images[0].Config
}

type imageConfig struct {
	Env        []string `json:"Env"`
	User       string   `json:"User"`
	WorkingDir string   `json:"WorkingDir"`
}

func assertEnv(t *testing.T, env []string, want string) {
	t.Helper()
	for _, got := range env {
		if got == want {
			return
		}
	}
	t.Fatalf("image env missing %q in %v", want, env)
}

func runDocker(t *testing.T, args ...string) []byte {
	t.Helper()
	return runCommand(t, "docker", args...)
}

func runCommand(t *testing.T, name string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = filepath.Join(repoRoot(t), "cmd", "dcb")
	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		t.Logf("%s %s\n%s", name, strings.Join(args, " "), out)
	}
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return out
}

func removeImage(t *testing.T, tag string) {
	t.Helper()
	cmd := exec.Command("docker", "image", "rm", "-f", tag)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Logf("cleanup docker image %s failed: %v\n%s", tag, err, out)
	}
}

func writeArtifact(t *testing.T, name string, data []byte) {
	t.Helper()
	dir := os.Getenv("DCB_ARTIFACT_DIR")
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Logf("create artifact dir %s: %v", dir, err)
		return
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Logf("write artifact %s: %v", path, err)
	}
}

func safeArtifactName(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9':
			return r
		}
		return '-'
	}, s)
}
