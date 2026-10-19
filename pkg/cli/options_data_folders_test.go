// relationships:
//
// references: [options_data_folders, stage, ../../internal/datafolder/staging]
package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestDataFolderOptions(t *testing.T) {
	root := (&application{}).command()
	for _, cmd := range root.Commands() {
		for _, option := range []struct{ name, commands string }{
			{"user-data-folder", "build exec read-configuration run-user-commands set-up up"},
			{"container-data-folder", "exec run-user-commands set-up up"},
			{"container-system-data-folder", "exec run-user-commands set-up up"},
			{"container-session-data-folder", "run-user-commands set-up up"},
		} {
			want := strings.Contains(" "+option.commands+" ", " "+cmd.Name()+" ")
			flag := cmd.Flags().Lookup(option.name)
			if (flag != nil) != want {
				t.Errorf("%s --%s present=%v want=%v", cmd.Name(), option.name, flag != nil, want)
				continue
			}
			if !want {
				continue
			}
			if flag.Value.Type() != "string" || flag.DefValue != "" || flag.Hidden {
				t.Errorf("unexpected flag: %+v", flag)
			}
			value := "../sample folder/./state/"
			if err := cmd.Flags().Set(option.name, value); err != nil {
				t.Fatal(err)
			}
			if got := folderOption(cmd, option.name); got != value {
				t.Errorf("%s %s=%q", cmd.Name(), option.name, got)
			}
		}
	}
}

func TestUserDataFolderFeatureStaging(t *testing.T) {
	workspace, parent := t.TempDir(), t.TempDir()
	feature := filepath.Join(workspace, "sample")
	if err := os.Mkdir(feature, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(feature, "devcontainer-feature.json"), []byte(`{"id":"sample","version":"1.0.0","name":"Sample"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(feature, "install.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(parent, "keep")
	if err := os.WriteFile(sentinel, []byte("existing"), 0o644); err != nil {
		t.Fatal(err)
	}
	var dc spec.DevContainer
	if err := json.Unmarshal([]byte(`{"features":{"./sample":true}}`), &dc); err != nil {
		t.Fatal(err)
	}
	dc.Path = filepath.Join(workspace, "devcontainer.json")
	a := &application{userDataFolder: parent}
	resolved, cleanup, err := a.resolveFeatures(&dc)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if len(resolved) != 1 || !strings.HasPrefix(resolved[0].Dir, parent+string(filepath.Separator)) {
		t.Fatalf("features staged outside folder: %+v", resolved)
	}
	cleanup()
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 1 || entries[0].Name() != "keep" {
		t.Fatalf("existing contents changed: %v %v", entries, err)
	}
}

func TestUserDataFolderReadConfigurationHandler(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/containers/json") {
			t.Errorf("unexpected Docker request: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(server.URL, "http://"))
	t.Setenv("DOCKER_API_VERSION", "1.47")
	workspace, parent := t.TempDir(), t.TempDir()
	feature := filepath.Join(workspace, "sample")
	if err := os.Mkdir(feature, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(feature, "devcontainer-feature.json"), []byte(`{"id":"sample","version":"1.0.0","name":"Sample"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(feature, "install.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(workspace, ".devcontainer.json")
	if err := os.WriteFile(cfg, []byte(`{"image":"alpine:3.20","features":{"./sample":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	result, _ := invoke(t, 0, "read-configuration", "--workspace-folder", workspace, "--include-features-configuration", "--user-data-folder", parent)
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), parent+"/") {
		t.Fatalf("host folder handler was ignored: %s", data)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 1 {
		t.Fatalf("feature configuration should retain its published staging: %v %v", entries, err)
	}
}

func TestUserDataFolderBuildOptions(t *testing.T) {
	parent := t.TempDir()
	a := &application{userDataFolder: parent}
	bo, err := (&buildOptions{}).prepare(a, &spec.DevContainer{Image: "alpine:3.20"}, t.TempDir(), false)
	if err != nil {
		t.Fatal(err)
	}
	if bo.UserDataFolder != parent {
		t.Fatalf("builder folder=%q want=%q", bo.UserDataFolder, parent)
	}
}
