//go:build docker_integration

// Package buildcontext runs dcb build and dcb up on Dockerfile configurations
// that copy from their build context. It is a separate package so that its
// Docker work has its own test budget beside cmd/dcb.
package buildcontext

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
	// The workspace is under the repository so the Docker daemon can bind-mount it.
	tmp := filepath.Join(repoRoot(t), ".tmp")
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
	bin := prepare(t)

	for _, layout := range buildContextLayouts {
		t.Run(layout.name, func(t *testing.T) {
			workspace := writeBuildContextWorkspace(t, layout, "FROM scratch\nCOPY . /context/\n", nil)
			out := t.TempDir()
			runDCB(t, bin, "build", "--workspace-folder", workspace, "--skip-features", "--output", out, "--progress", "plain")
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
		runDCB(t, bin, "build", "--workspace-folder", workspace, "--output", out, "--progress", "plain")
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
	bin := prepare(t)

	for _, layout := range buildContextLayouts {
		t.Run(layout.name, func(t *testing.T) {
			workspace := writeBuildContextWorkspace(t, layout, "FROM alpine:3.20\nCOPY . /context/\n", nil)
			tag := fmt.Sprintf("dcb-test/build-context-%s:%d", layout.name, time.Now().UnixNano())
			t.Cleanup(func() {
				if out, err := exec.Command(bin, "down", "--workspace-folder", workspace).CombinedOutput(); err != nil {
					t.Logf("down: %v\n%s", err, out)
				}
				if out, err := exec.Command("docker", "image", "rm", tag).CombinedOutput(); err != nil {
					t.Logf("remove image %s: %v\n%s", tag, err, out)
				}
			})
			runDCB(t, bin, "up", "--workspace-folder", workspace, "--image-name", tag, "--update-remote-user-uid-default", "never")
			out := runDCB(t, bin, "exec", "--workspace-folder", workspace, "--", "sh", "-c",
				"cat /context/kept.txt; if test -e /context/ignored.txt; then echo ignored-present; else echo ignored-absent; fi")
			if got, want := strings.TrimSpace(string(out)), "kept-"+layout.name+"\nignored-absent"; got != want {
				t.Fatalf("container context files = %q, want %q", got, want)
			}
		})
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// prepare checks Docker, makes the base image available and builds dcb.
func prepare(t *testing.T) string {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping Docker integration test in short mode")
	}
	if out, err := exec.Command("docker", "info").CombinedOutput(); err != nil {
		if os.Getenv("CI") == "true" {
			t.Fatalf("docker daemon is required: %v\n%s", err, out)
		}
		t.Skipf("docker daemon is required: %v\n%s", err, out)
	}
	if exec.Command("docker", "image", "inspect", "alpine:3.20").Run() != nil {
		if out, err := exec.Command("docker", "pull", "alpine:3.20").CombinedOutput(); err != nil {
			t.Fatalf("pull alpine:3.20: %v\n%s", err, out)
		}
	}
	bin := filepath.Join(t.TempDir(), "dcb")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/dcb")
	cmd.Dir = repoRoot(t)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build dcb: %v\n%s", err, out)
	}
	return bin
}

// runDCB runs dcb and returns its stdout. Except for exec, stdout must be
// one JSON success result.
func runDCB(t *testing.T, bin string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	t.Logf("dcb %s\n%s", strings.Join(args, " "), out)
	if err != nil {
		t.Fatalf("dcb %s: %v\nstdout: %s\nstderr: %s", args[0], err, out, stderr.String())
	}
	if args[0] != "exec" {
		var result map[string]any
		if json.Unmarshal(out, &result) != nil || result["outcome"] != "success" || bytes.Count(out, []byte("\n")) != 1 {
			t.Fatalf("dcb %s result: %s\nstderr: %s", args[0], out, stderr.String())
		}
	}
	return out
}
