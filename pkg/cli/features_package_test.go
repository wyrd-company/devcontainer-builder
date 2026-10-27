// relationships:
//
//	references: [features_package, stage, ../features/package]
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFeaturesPackageCommand(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.WriteFile("devcontainer-feature.json", []byte(`{"id":"sample","version":"1.2.3","name":"Sample tool"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	invoke(t, 2, "features", "package")
	result, _ := invoke(t, 0, "features", "package", ".")
	if len(result["features"].([]any)) != 1 {
		t.Fatalf("result = %v", result)
	}
	for _, file := range []string{"devcontainer-feature-sample.tgz", "devcontainer-collection.json"} {
		if _, err := os.Stat(filepath.Join("output", file)); err != nil {
			t.Fatal(err)
		}
	}
	invoke(t, 1, "features", "package", ".", "--force-clean-output-folder", "false")
	invoke(t, 1, "features", "package", ".", "-f", "false")
	invoke(t, 0, "features", "package", "-f", "-o", "output", ".")
	invoke(t, 0, "features", "package", ".", "--output-folder", "alternate")
	invoke(t, 1, "features", "package", "missing")
	invoke(t, 1, "features", "package", ".", "--output-folder", "")
	invoke(t, 2, "features", "package", ".", "extra")
	version, _ := invoke(t, 0, "features", "package", "--version")
	if version["version"] != "test-version" {
		t.Fatal(version)
	}
	help, _ := invokeHelp(t, "features", "package", "--help")
	if !strings.Contains(help, "--output-folder") || !strings.Contains(help, "--force-clean-output-folder") {
		t.Fatal(help)
	}
}

func TestFeaturesPackageTarget(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("input", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("input/devcontainer-feature.json", []byte(`{"id":"sample","version":"1.2.3","name":"Sample tool"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	invoke(t, 0, "features", "package", "input", "-o", "packaged")
	if _, err := os.Stat("packaged/devcontainer-feature-sample.tgz"); err != nil {
		t.Fatal(err)
	}
}
