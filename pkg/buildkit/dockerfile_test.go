package buildkit

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	fsutil "github.com/tonistiigi/fsutil"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

// The dockerfile frontend reads the build context from the local named
// "context" and the Dockerfile from the local named by dockerfilekey.
func TestAddDockerfileMounts_FrontendNames(t *testing.T) {
	contextDir, dockerfileDir := t.TempDir(), t.TempDir()
	writeMountTestFile(t, filepath.Join(contextDir, "kept.txt"), "context file")
	writeMountTestFile(t, filepath.Join(dockerfileDir, "Dockerfile"), "FROM scratch\n")
	mounts := map[string]fsutil.FS{}
	if err := addDockerfileMounts(mounts, &DockerfileBuild{ContextDir: contextDir, DockerfileDir: dockerfileDir}); err != nil {
		t.Fatal(err)
	}
	if got := readMountFile(t, mounts, "context", "kept.txt"); got != "context file" {
		t.Fatalf("context mount kept.txt = %q", got)
	}
	if got := readMountFile(t, mounts, dockerfileFrontendAttrs(&DockerfileBuild{}, buildSettings{})["dockerfilekey"], "Dockerfile"); got != "FROM scratch\n" {
		t.Fatalf("dockerfile mount Dockerfile = %q", got)
	}
}

// A Dockerfile base must not share the "context" local with the staged
// features context, or the frontend reads the staging directory.
func TestSolveDevcontainer_RejectsLocalNamedContext(t *testing.T) {
	staging, contextDir := t.TempDir(), t.TempDir()
	_, err := SolveDevcontainer(context.Background(), BuildSpec{
		Graph:    GraphInputs{Spec: &spec.DevContainer{WorkspaceFolder: "/workspaces/demo"}, LocalContextName: "context"},
		Upstream: &DockerfileBuild{ContextDir: contextDir},
	}, SolveOptions{LocalContextName: "context", LocalContextDir: staging})
	if err == nil || !strings.Contains(err.Error(), `local mount "context" is already in use`) {
		t.Fatalf("SolveDevcontainer err = %v, want the context mount collision", err)
	}
}

func readMountFile(t *testing.T, mounts map[string]fsutil.FS, name, file string) string {
	t.Helper()
	fs, ok := mounts[name]
	if !ok {
		t.Fatalf("no local mount %q in %v", name, mounts)
	}
	rc, err := fs.Open(file)
	if err != nil {
		t.Fatalf("open %s in mount %q: %v", file, name, err)
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func writeMountTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
