// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package cli

import (
	"os"
	"path/filepath"
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
