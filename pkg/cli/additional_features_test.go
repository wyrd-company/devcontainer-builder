// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestAdditionalFeaturesInvalidJSON(t *testing.T) {
	t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "missing.sock"))
	cfg := filepath.Join(t.TempDir(), "devcontainer.json")
	if err := os.WriteFile(cfg, []byte(`{"image":"alpine:3.20"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"up", "build", "generate"} {
		result, _ := invoke(t, 2, command, "--config", cfg, "--additional-features", `{"example.invalid/one:1":`)
		if message, _ := result["message"].(string); !strings.HasPrefix(message, "--additional-features: ") {
			t.Errorf("%s: message=%q", command, message)
		}
	}
}

// The additional feature reaches the generated image metadata; for a key the
// configuration declares, the configuration's options win.
func TestAdditionalFeaturesReachImageMetadata(t *testing.T) {
	generateImageFixture(t)
	workspace := t.TempDir()
	dir := filepath.Join(workspace, ".devcontainer")
	for _, id := range []string{"declared", "extra"} {
		feature := filepath.Join(dir, id)
		if err := os.MkdirAll(feature, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"id":"` + id + `","version":"1.0.0","options":{"marker":{"type":"string","default":"unset"}}}`
		if err := os.WriteFile(filepath.Join(feature, "devcontainer-feature.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(feature, "install.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	config := `{"image":"alpine:3.20","features":{"./declared":{"marker":"from-config"}}}`
	if err := os.WriteFile(filepath.Join(dir, "devcontainer.json"), []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}
	extra := `{"./extra":{"marker":"from-flag"},"./declared":{"marker":"from-flag"}}`
	result, _ := invoke(t, 0, "generate", "--workspace-folder", workspace, "--work-dir", t.TempDir(), "--additional-features", extra)
	dockerfile, err := os.ReadFile(result["dockerfile"].(string))
	if err != nil {
		t.Fatal(err)
	}
	body := string(dockerfile)
	if !strings.Contains(body, `LABEL devcontainer.metadata="[{\"id\":\"./declared\"},{\"id\":\"./extra\"}]"`) {
		t.Errorf("metadata label lacks the additional feature after the declared one:\n%s", body)
	}
	// Each feature's installer prints its options once.
	if strings.Count(body, `echo '    MARKER="from-config"'`) != 1 || strings.Count(body, `echo '    MARKER="from-flag"'`) != 1 {
		t.Fatalf("feature options:\n%s", body)
	}
}

// Without a container, upstream read-configuration resolves additional
// features into the merged configuration; its features property stays the
// configuration's own.
func TestReadMergedConfigurationAdditionalFeatures(t *testing.T) {
	workspace := t.TempDir()
	body := `{"image":"alpine:3.20","features":{"./declared":{}}}`
	if err := os.WriteFile(filepath.Join(workspace, ".devcontainer.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"declared", "extra"} {
		if err := os.Mkdir(filepath.Join(workspace, id), 0o755); err != nil {
			t.Fatal(err)
		}
		feature := `{"id":"` + id + `","version":"1.0.0","postCreateCommand":"echo ` + id + `"}`
		if err := os.WriteFile(filepath.Join(workspace, id, "devcontainer-feature.json"), []byte(feature), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	withContainer := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/json") && withContainer:
			_, _ = w.Write([]byte(`[{"Id":"sample-container"}]`))
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			_, _ = w.Write([]byte(`[]`))
		case strings.HasSuffix(r.URL.Path, "/containers/sample-container/json"):
			label, _ := json.Marshal([]map[string]any{{"postCreateCommand": "echo container"}})
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": "sample-container", "Config": map[string]any{"User": "root", "Labels": map[string]string{"devcontainer.metadata": string(label)}}})
		case strings.Contains(r.URL.Path, "/images/"):
			_, _ = w.Write([]byte(`{"Config":{"User":"root","Labels":{}}}`))
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	t.Cleanup(srv.Close)
	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(srv.URL, "http://"))
	t.Setenv("DOCKER_API_VERSION", "1.47")
	result, _ := invoke(t, 0, "read-configuration", "--workspace-folder", workspace, "--include-merged-configuration", "--additional-features", `{"./extra":{}}`)
	merged := result["mergedConfiguration"].(map[string]any)
	if !reflect.DeepEqual(merged["postCreateCommands"], []any{"echo declared", "echo extra"}) {
		t.Fatalf("postCreateCommands=%v", merged["postCreateCommands"])
	}
	if !reflect.DeepEqual(merged["features"], map[string]any{"./declared": map[string]any{}}) {
		t.Fatalf("features=%v", merged["features"])
	}
	invoke(t, 2, "read-configuration", "--workspace-folder", workspace, "--additional-features", `[`)

	// With a container, its metadata is the source; a feature that does not
	// exist would fail if dcb resolved the additional features.
	withContainer = true
	result, _ = invoke(t, 0, "read-configuration", "--workspace-folder", workspace, "--include-merged-configuration", "--additional-features", `{"./missing":{}}`)
	merged = result["mergedConfiguration"].(map[string]any)
	if !reflect.DeepEqual(merged["postCreateCommands"], []any{"echo container"}) {
		t.Fatalf("container postCreateCommands=%v", merged["postCreateCommands"])
	}
}
