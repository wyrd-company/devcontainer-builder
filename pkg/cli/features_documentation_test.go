// relationships:
//
// references: [features_documentation, ../features/documentation]
package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFeaturesDocumentationCommand(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll("input/sample", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("input/sample/devcontainer-feature.json", []byte(`{"id":"sample","version":"2.3.4"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	invoke(t, 2, "features", "generate-docs")
	invoke(t, 2, "features", "generate-docs", "-n", "example/collection", "extra")
	invoke(t, 2, "features", "generate-docs", "-n", "example/collection", "--log-level", "invalid")
	invoke(t, 1, "features", "generate-docs", "-p", "missing", "-n", "example/collection")
	invoke(t, 1, "features", "generate-docs", "-p", "", "-n", "example/collection")
	invoke(t, 1, "features", "generate-docs", "-p", "input", "-p", "input", "-n", "example/collection")
	_, stderr := invoke(t, 0, "features", "generate-docs", "-p", "input", "-n", "example/collection", "-r", "registry.example", "--github-owner", "example-owner", "--github-repo", "example-repo")
	if !strings.Contains(stderr, "Generating") {
		t.Fatal("progress missing")
	}
	data, err := os.ReadFile("input/sample/README.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"registry.example/example/collection/sample:2", "https://github.com/example-owner/example-repo/blob/main/input/sample/devcontainer-feature.json"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("README missing %q", want)
		}
	}
	invoke(t, 0, "features", "generate-docs", "--project-folder", "input", "--namespace", "", "--registry", "")
	data, _ = os.ReadFile("input/sample/README.md")
	if !strings.Contains(string(data), "//sample:2") {
		t.Fatal("empty namespace/registry did not remain empty")
	}
	invoke(t, 0, "features", "generate-docs", "-p", "input", "-n", "first", "-n", "second", "-r", "one", "-r", "two")
	data, _ = os.ReadFile("input/sample/README.md")
	if !strings.Contains(string(data), "one,two/first,second/sample:2") {
		t.Fatal("repeated strings not accumulated")
	}
	t.Chdir("input")
	invoke(t, 0, "features", "generate-docs", "-n", "example/collection")
	data, _ = os.ReadFile("sample/README.md")
	if !strings.Contains(string(data), "ghcr.io/example/collection/sample:2") {
		t.Fatal("defaults incorrect")
	}
	version, _ := invoke(t, 0, "features", "generate-docs", "--version")
	if version["version"] != "test-version" {
		t.Fatal(version)
	}
	help, _ := invokeHelp(t, "features", "generate-docs", "--help")
	for _, flag := range []string{"--project-folder", "--registry", "--namespace", "--github-owner", "--github-repo", "--log-level"} {
		if !strings.Contains(help, flag) {
			t.Fatalf("help missing %s", flag)
		}
	}
}

func TestFeaturesDocumentationDiagnostics(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.Mkdir("sample", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("sample", "devcontainer-feature.json"), []byte(`{"name":"Missing ID"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_, stderr := invoke(t, 0, "features", "generate-docs", "-n", "example/collection", "--log-level", "error", "--log-format", "json")
	if !strings.Contains(stderr, `"level":"error"`) || !strings.Contains(stderr, "does not contain an 'id'") {
		t.Fatal(stderr)
	}
	if strings.Contains(stderr, "Generating") {
		t.Fatal("info ignored log threshold")
	}
	if _, err := os.Stat("sample/README.md"); !os.IsNotExist(err) {
		t.Fatal("missing ID generated a README")
	}
}

func TestFeaturesDocumentationDelimiterTail(t *testing.T) {
	for _, row := range []struct {
		name string
		args []string
		exit int
	}{
		{"positional", []string{"extra"}, 2},
		{"tail", []string{"--", "extra"}, 0},
		{"ignored-shadow", []string{"--", "--namespace.toString=x"}, 0},
		{"empty", []string{"--"}, 0},
		{"positional-before-tail", []string{"extra", "--", "ignored"}, 2},
		{"conversion-before-tail", []string{"--namespace.toString=x", "--", "extra"}, 1},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			for _, name := range []string{"sample", "sibling"} {
				if err := os.Mkdir(name, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(name, "devcontainer-feature.json"), []byte(`{"id":"`+name+`","version":"1.0.0"}`), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile("sample/README.md", []byte("retained"), 0o644); err != nil {
				t.Fatal(err)
			}
			invoke(t, row.exit, append([]string{"features", "generate-docs", "-n", "plain"}, row.args...)...)
			data, err := os.ReadFile("sample/README.md")
			if err != nil {
				t.Fatal(err)
			}
			if row.exit == 0 {
				if !strings.Contains(string(data), "ghcr.io/plain/sample:1") {
					t.Fatalf("unexpected README: %s", data)
				}
				if _, err := os.Stat("sibling/README.md"); err != nil {
					t.Fatal(err)
				}
			} else {
				if string(data) != "retained" {
					t.Fatalf("README replaced: %s", data)
				}
				if _, err := os.Stat("sibling/README.md"); !os.IsNotExist(err) {
					t.Fatalf("unexpected sibling README: %v", err)
				}
			}
		})
	}
}
