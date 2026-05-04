//go:build k8svalidate

package kubernetes_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const kubernetesSchemaVersion = "1.30.0"

func TestRenderedManifestsValidate(t *testing.T) {
	if _, err := exec.LookPath("kubeconform"); err != nil {
		if os.Getenv("CI") == "true" {
			t.Fatalf("kubeconform is required: %v", err)
		}
		t.Skipf("kubeconform is required: %v", err)
	}

	root := repoRoot(t)
	render := exec.Command("go", "run", "./cmd/internal/render-k8s-testdata")
	render.Dir = root
	data, err := render.CombinedOutput()
	if err != nil {
		t.Fatalf("render manifests: %v\n%s", err, data)
	}

	path := filepath.Join(t.TempDir(), "devcontainer-kubernetes.yaml")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	validate := exec.Command("kubeconform", "-strict", "-kubernetes-version", kubernetesSchemaVersion, path)
	out, err := validate.CombinedOutput()
	if err != nil {
		t.Fatalf("kubeconform: %v\n%s\nrendered:\n%s", err, out, data)
	}
	if len(out) > 0 {
		t.Logf("kubeconform output:\n%s", out)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join(wd, "..", "..", ".."))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("locate repo root from %s: %v", wd, err)
	}
	return strings.TrimSpace(root)
}
