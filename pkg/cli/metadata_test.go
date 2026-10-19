// relationships:
//
//	references: [metadata, config, lifecycle]
package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/host/dockercompose"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
)

func TestReadMergedConfiguration(t *testing.T) {
	for _, source := range []string{"image", "container", "compose-container", "discovered", "invalid-label", "lookup-error"} {
		t.Run(source, func(t *testing.T) {
			workspace := t.TempDir()
			config := filepath.Join(workspace, ".devcontainer.json")
			body := `{"image":"alpine:3.20","features":{"./sample-feature":{}},"remoteUser":"sample","remoteEnv":{"CONFIG":"${localEnv:SAMPLE_VALUE}"},"postCreateCommand":"echo config","extension":{"enabled":true}}`
			if source == "compose-container" {
				body = strings.Replace(body, `"image":"alpine:3.20",`, `"dockerComposeFile":"compose.yml","service":"app",`, 1)
			}
			if err := os.WriteFile(config, []byte(body), 0644); err != nil {
				t.Fatal(err)
			}
			if source != "container" && source != "compose-container" && source != "discovered" {
				if err := os.Mkdir(filepath.Join(workspace, "sample-feature"), 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(workspace, "sample-feature", "devcontainer-feature.json"), []byte(`{"id":"sample","version":"1.0.0","capAdd":["SYS_PTRACE"],"postCreateCommand":"echo feature"}`), 0644); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("SAMPLE_VALUE", "host-value")
			entries := []metadata.Entry{{ID: "base", RemoteUser: "inherited", PostCreate: spec.Command{Shell: "echo base"}}}
			if source == "container" || source == "compose-container" || source == "discovered" {
				entries = append(entries, metadata.Entry{ID: "./sample-feature", CapAdd: []string{"SYS_PTRACE"}, PostCreate: spec.Command{Shell: "echo feature"}}, metadata.Entry{PostCreate: spec.Command{Shell: "echo config"}})
			}

			value, err := metadata.Encode(entries)
			if err != nil {
				t.Fatal(err)
			}
			if source == "invalid-label" {
				value = "invalid"
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/containers/json"):
					if source == "lookup-error" {
						http.Error(w, `{"message":"unavailable"}`, 500)
						return
					}
					if source == "discovered" {
						_, _ = w.Write([]byte(`[{"Id":"sample-container"}]`))
					} else {
						_, _ = w.Write([]byte(`[]`))
					}
				case strings.Contains(r.URL.Path, "/images/") || strings.Contains(r.URL.Path, "/containers/sample-container/json"):
					labels := map[string]string{metadata.Label: value}
					if source == "compose-container" {
						labels[dockercompose.LabelPrimary] = "true"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"Id": "sample-container", "Config": map[string]any{"User": "root", "Labels": labels}, "State": map[string]any{"Running": true}})
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected", 500)
				}
			}))
			defer srv.Close()
			t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(srv.URL, "http://"))
			t.Setenv("DOCKER_API_VERSION", "1.47")
			args := []string{"read-configuration", "--workspace-folder", workspace, "--include-merged-configuration"}
			if source == "container" || source == "compose-container" {
				args = append(args, "--container-id", "sample-container")
			}
			code := 0
			if source == "lookup-error" {
				code = 1
			}
			result, logs := invoke(t, code, args...)
			if code != 0 {
				return
			}
			merged := result["mergedConfiguration"].(map[string]any)
			wantCommands := []any{"echo base", "echo feature", "echo config"}
			if source == "invalid-label" {
				wantCommands = wantCommands[1:]
				if !strings.Contains(logs, "warning") {
					t.Fatalf("invalid label was not logged: %q", logs)
				}
			}
			if !reflect.DeepEqual(merged["postCreateCommands"], wantCommands) || merged["remoteUser"] != "sample" || !reflect.DeepEqual(merged["capAdd"], []any{"SYS_PTRACE"}) {
				t.Fatalf("merged=%v", merged)
			}
			if merged["configFilePath"] != config || !reflect.DeepEqual(merged["extension"], map[string]any{"enabled": true}) || !reflect.DeepEqual(merged["features"], map[string]any{"./sample-feature": map[string]any{}}) || !reflect.DeepEqual(merged["remoteEnv"], map[string]any{"CONFIG": "host-value"}) {
				t.Fatalf("merged config fields=%v", merged)
			}
			if _, ok := merged["postCreateCommand"]; ok {
				t.Fatal("singular command retained")
			}
			if result["configuration"].(map[string]any)["postCreateCommand"] != "echo config" {
				t.Fatal("ordinary configuration changed")
			}
		})
	}
}

func TestReadMergedConfigurationRejectsBlankContainerID(t *testing.T) {
	result, _ := invoke(t, 2, "read-configuration", "--include-merged-configuration", "--container-id=")
	if !strings.Contains(result["message"].(string), "must not be empty") {
		t.Fatalf("result=%v", result)
	}
}

func TestEffectiveContainerConfigUsesMetadata(t *testing.T) {
	value, err := metadata.Encode([]metadata.Entry{{RemoteUser: "sample", ContainerEnv: map[string]any{"IMAGE": "image"}, RemoteEnv: map[string]any{"BASE": "base"}}})
	if err != nil {
		t.Fatal(err)
	}
	dc := &spec.DevContainer{Image: "alpine", UpdateRemoteEnv: map[string]any{"CONFIG": "config"}}
	got := effectiveContainerConfig(dc, map[string]string{metadata.Label: value})
	if got.RemoteUser != "sample" || got.ContainerEnv["IMAGE"] != "image" || !reflect.DeepEqual(got.UpdateRemoteEnv, map[string]any{"BASE": "base", "CONFIG": "config"}) {
		t.Fatalf("config=%+v", got)
	}
}

func TestMetadataBaseSpecCompose(t *testing.T) {
	for _, mode := range []string{"image", "build", "default-dockerfile"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			compose := "services:\n  app:\n    image: alpine:3.20\n"
			if mode != "image" {
				compose = "services:\n  app:\n    build:\n      context: .\n      target: selected\n      args:\n        SAMPLE: value\n"
				if mode == "build" {
					compose += "      dockerfile: Samplefile\n"
				}
			}
			if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte(compose), 0644); err != nil {
				t.Fatal(err)
			}
			dc := &spec.DevContainer{Path: filepath.Join(dir, "devcontainer.json"), DockerComposeFile: spec.StringOrSlice{"compose.yml"}, Service: "app"}
			got, err := metadataBaseSpec(context.Background(), dc, dir)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "image" {
				if got.Image != "alpine:3.20" || got.Build != nil {
					t.Fatalf("spec=%+v", got)
				}
			} else {
				want := "Dockerfile"
				if mode == "build" {
					want = "Samplefile"
				}
				if got.Build == nil || got.Build.Dockerfile != want || got.Build.Context != dir || got.Build.Target != "selected" || got.Build.Args["SAMPLE"] != "value" {
					t.Fatalf("spec=%+v", got)
				}
			}
		})
	}
}

func TestMetadataDoesNotChangeDevcontainerID(t *testing.T) {
	dc := &spec.DevContainer{Path: "/workspace/devcontainer.json"}
	app := &application{}
	before := app.substitutionContext(dc, "/workspace", nil)
	after := app.substitutionContext(dc, "/workspace", map[string]string{"devcontainer.local_folder": "/workspace", "devcontainer.config_file": dc.Path, metadata.Label: `[{"postCreateCommand":"echo ${devcontainerId}"}]`, "com.docker.compose.project": "sample"})
	if a, b := substitute.Substitute(before, "${devcontainerId}"), substitute.Substitute(after, "${devcontainerId}"); a == "" || a != b {
		t.Fatalf("identity changed: %q != %q", a, b)
	}
}

func TestComposeCapturedContainerMetadataKeepsUpstreamPolicy(t *testing.T) {
	dc := &spec.DevContainer{DockerComposeFile: spec.StringOrSlice{"compose.yml"}, Service: "app", PostCreate: spec.Command{Shell: "echo current"}, ContainerUser: "current", ContainerEnv: map[string]any{"CURRENT": "config"}, RemoteUser: "current-remote", UpdateRemoteEnv: map[string]any{"UPDATE": "value"}}
	value, err := metadata.Encode([]metadata.Entry{{ID: "./sample", ContainerUser: "stored", ContainerEnv: map[string]any{"STORED": "stored"}, PostCreate: spec.Command{Shell: "echo captured"}}})
	if err != nil {
		t.Fatal(err)
	}
	app := &application{}
	for _, primary := range []string{"", "true"} {
		labels := map[string]string{metadata.Label: value, dockercompose.LabelPrimary: primary}
		entries, err := app.containerMetadata(dc, labels)
		if err != nil {
			t.Fatal(err)
		}
		effective := effectiveContainerConfig(dc, labels)
		if effective.ContainerUser != "stored" || effective.ContainerEnv["CURRENT"] != nil || effective.RemoteUser != "current-remote" || effective.UpdateRemoteEnv["UPDATE"] != "value" {
			t.Fatalf("captured settings: %+v", effective)
		}
		plan := metadata.Plan(entries)
		if len(plan) != 1 || plan[0].Command.Shell != "echo captured" {
			t.Fatalf("captured container hooks changed: %+v", plan)
		}
	}
}

func TestGenerateIncludesLocalImageMetadata(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, ".devcontainer.json"), []byte(`{"image":"alpine:3.20","postCreateCommand":"echo config"}`), 0644); err != nil {
		t.Fatal(err)
	}
	entries := []metadata.Entry{{ID: "base", RemoteUser: "root", PostCreate: spec.Command{Shell: "echo base"}}}
	label, err := metadata.Encode(entries)
	if err != nil {
		t.Fatal(err)
	}
	inspected := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/images/alpine:3.20/json") {
			t.Errorf("unexpected request %s", r.URL.Path)
			http.Error(w, "unexpected", 500)
			return
		}
		inspected = true
		_ = json.NewEncoder(w).Encode(map[string]any{"Config": map[string]any{"Labels": map[string]string{metadata.Label: label}}})
	}))
	defer srv.Close()
	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(srv.URL, "http://"))
	t.Setenv("DOCKER_API_VERSION", "1.47")
	result, _ := invoke(t, 0, "generate", "--workspace-folder", workspace, "--work-dir", t.TempDir())
	body, err := os.ReadFile(result["dockerfile"].(string))
	if err != nil {
		t.Fatal(err)
	}
	want, err := metadata.DockerfileLabel(metadata.Collect(entries, nil, &spec.DevContainer{PostCreate: spec.Command{Shell: "echo config"}}))
	if err != nil {
		t.Fatal(err)
	}
	if !inspected || !strings.Contains(string(body), want) {
		t.Fatalf("CLI generator omitted inherited label: inspected=%v dockerfile=%s", inspected, body)
	}
}

// generateImageFixture keeps CLI generation tests independent of the host daemon.
func generateImageFixture(t *testing.T) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/containers/json") {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		if !strings.Contains(r.URL.Path, "/images/") || !strings.HasSuffix(r.URL.Path, "/json") {
			t.Errorf("unexpected configuration request %s", r.URL.Path)
			http.Error(w, "unexpected", 500)
			return
		}
		_, _ = w.Write([]byte(`{"Config":{"User":"root","Labels":{}}}`))
	}))
	t.Cleanup(srv.Close)
	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(srv.URL, "http://"))
	t.Setenv("DOCKER_API_VERSION", "1.47")
}
