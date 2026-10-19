//go:build docker_integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildContextLayout places the configured build context relative to the
// directory holding devcontainer.json. Every layout keeps the Dockerfile in
// that directory, so the subdir layout also builds a Dockerfile from outside
// its context.
type buildContextLayout struct {
	name, context string
	// dir is the context directory relative to the workspace.
	dir string
}

var buildContextLayouts = []buildContextLayout{
	{name: "config-dir", context: ".", dir: ".devcontainer"},
	{name: "parent-dir", context: "..", dir: "."},
	{name: "subdir", context: "payload", dir: filepath.Join(".devcontainer", "payload")},
}

// writeBuildContextWorkspace writes a Dockerfile configuration whose context
// holds a kept file and a file its .dockerignore excludes. The kept file names
// the layout, so a build from any other directory cannot produce it.
func writeBuildContextWorkspace(t *testing.T, layout buildContextLayout, dockerfile string, extra map[string]any) string {
	t.Helper()
	tmp, err := filepath.Abs(filepath.Join("..", "..", ".tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		t.Fatal(err)
	}
	workspace, err := os.MkdirTemp(tmp, "build-context-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	if syntax := os.Getenv("DCB_TEST_DOCKERFILE_SYNTAX"); syntax != "" {
		dockerfile = "# syntax=" + syntax + "\n" + dockerfile
	}
	config := map[string]any{"build": map[string]any{"dockerfile": "Dockerfile", "context": layout.context}}
	for k, v := range extra {
		config[k] = v
	}
	files := map[string]string{
		filepath.Join(".devcontainer", "devcontainer.json"): mustJSON(t, config),
		filepath.Join(".devcontainer", "Dockerfile"):        dockerfile,
		filepath.Join(layout.dir, "kept.txt"):               "kept-" + layout.name + "\n",
		filepath.Join(layout.dir, "ignored.txt"):            "ignored\n",
		filepath.Join(layout.dir, ".dockerignore"):          "ignored.txt\n",
	}
	for name, body := range files {
		path := filepath.Join(workspace, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return workspace
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	body, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// TestDockerBuildContextIntegration builds a Dockerfile that copies its whole
// context through dcb build, for each context placement. The kept file must
// come from the configured context and the ignored file must be absent.
func TestDockerBuildContextIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping Docker integration test in short mode")
	}
	requireDocker(t)
	bin := buildBinary(t)

	for _, layout := range buildContextLayouts {
		t.Run(layout.name, func(t *testing.T) {
			workspace := writeBuildContextWorkspace(t, layout, "FROM scratch\nCOPY . /context/\n", nil)
			out := t.TempDir()
			hostRunCommand(t, os.Environ(), bin, "build", "--workspace-folder", workspace, "--skip-features", "--output", out, "--progress", "plain")
			assertContextCopy(t, out, layout)
		})
	}

	// A local feature reads the staged features context while the Dockerfile
	// reads the configured context in the same build.
	t.Run("config-dir-with-feature", func(t *testing.T) {
		layout := buildContextLayouts[0]
		workspace := writeBuildContextWorkspace(t, layout, "FROM alpine:3.20\nCOPY . /context/\n",
			map[string]any{"features": map[string]any{"./marker": map[string]any{}}})
		featureDir := filepath.Join(workspace, ".devcontainer", "marker")
		if err := os.MkdirAll(featureDir, 0o755); err != nil {
			t.Fatal(err)
		}
		for name, body := range map[string]string{
			"devcontainer-feature.json": `{"id":"marker","version":"1.0.0"}`,
			"install.sh":                "#!/bin/sh\nset -e\necho feature-marker > /usr/local/share/feature-marker\n",
		} {
			if err := os.WriteFile(filepath.Join(featureDir, name), []byte(body), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		out := t.TempDir()
		hostRunCommand(t, os.Environ(), bin, "build", "--workspace-folder", workspace, "--output", out, "--progress", "plain")
		assertContextCopy(t, out, layout)
		if got, err := os.ReadFile(filepath.Join(out, "usr", "local", "share", "feature-marker")); err != nil || string(got) != "feature-marker\n" {
			t.Fatalf("feature marker = %q, %v; want feature-marker", got, err)
		}
	})
}

func assertContextCopy(t *testing.T, root string, layout buildContextLayout) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(root, "context", "kept.txt"))
	if err != nil || string(got) != "kept-"+layout.name+"\n" {
		t.Fatalf("/context/kept.txt = %q, %v; want the file from the %s context", got, err, layout.name)
	}
	if _, err := os.Stat(filepath.Join(root, "context", "ignored.txt")); !os.IsNotExist(err) {
		t.Fatalf("/context/ignored.txt stat err = %v, want it excluded by .dockerignore", err)
	}
}

// TestDockerUpBuildContextIntegration covers the same context placements
// through dcb up, reading the copied files inside the running container.
func TestDockerUpBuildContextIntegration(t *testing.T) {
	cli := hostRequireDocker(t, context.Background())
	t.Cleanup(func() { _ = cli.Close() })
	hostEnsureImage(t, context.Background(), cli, "alpine:3.20")
	bin := hostBuildBinary(t)

	for _, layout := range buildContextLayouts {
		t.Run(layout.name, func(t *testing.T) {
			workspace := writeBuildContextWorkspace(t, layout, "FROM alpine:3.20\nCOPY . /context/\n", nil)
			tag := fmt.Sprintf("dcb-test/build-context-%s:%d", layout.name, time.Now().UnixNano())
			t.Cleanup(func() {
				if err := hostRunCommandAllowError(bin, "down", "--workspace-folder", workspace); err != nil {
					t.Logf("down: %v", err)
				}
				removeImage(t, tag)
			})
			hostRunCommand(t, os.Environ(), bin, "up", "--workspace-folder", workspace, "--image-name", tag, "--update-remote-user-uid-default", "never")
			out := hostRunCommand(t, os.Environ(), bin, "exec", "--workspace-folder", workspace, "--", "sh", "-c",
				"cat /context/kept.txt; if test -e /context/ignored.txt; then echo ignored-present; else echo ignored-absent; fi")
			if got, want := strings.TrimSpace(string(out)), "kept-"+layout.name+"\nignored-absent"; got != want {
				t.Fatalf("container context files = %q, want %q", got, want)
			}
		})
	}
}
