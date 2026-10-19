// relationships:
//
// references: [config, features_configuration, metadata]
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

func TestReadConfigurationContainerSnapshot(t *testing.T) {
	for _, selection := range []string{"explicit", "workspace"} {
		t.Run(selection, func(t *testing.T) {
			workspace := t.TempDir()
			t.Setenv("SAMPLE_HOST", "host-value")
			body := `{"image":"alpine:3.20","remoteEnv":{"VALUE":"${containerEnv:SAMPLE_VALUE}","HOST":"${localEnv:SAMPLE_HOST}"},"extension":{"values":["${containerEnv:SAMPLE_VALUE}","${localEnv:SAMPLE_HOST}"]},"features":{"./sample":{"marker":"${containerEnv:SAMPLE_VALUE}","host":"${localEnv:SAMPLE_HOST}"}}}`
			if err := os.WriteFile(filepath.Join(workspace, ".devcontainer.json"), []byte(body), 0644); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(workspace, "sample")
			if err := os.Mkdir(dir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "devcontainer-feature.json"), []byte(`{"id":"sample","version":"1.0.0","options":{"marker":{"type":"string"},"host":{"type":"string"}}}`), 0644); err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasSuffix(r.URL.Path, "/containers/json"):
					if selection != "workspace" {
						t.Error("explicit ID performed workspace discovery")
					}
					if !strings.Contains(r.URL.Query().Get("filters"), workspace) {
						t.Errorf("discovery filters=%s", r.URL.RawQuery)
					}
					_, _ = w.Write([]byte(`[{"Id":"sample-container"}]`))
				case strings.HasSuffix(r.URL.Path, "/containers/sample-container/json"):
					_ = json.NewEncoder(w).Encode(map[string]any{"Id": "sample-container", "Config": map[string]any{"Env": []string{"SAMPLE_VALUE=container-value", "UNREFERENCED_SECRET=synthetic-secret"}, "Labels": map[string]string{"devcontainer.metadata": `[{"remoteUser":"inherited"}]`}}})
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					http.Error(w, "unexpected", 500)
				}
			}))
			defer server.Close()
			t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(server.URL, "http://"))
			t.Setenv("DOCKER_API_VERSION", "1.47")
			var baseline any
			for _, mode := range []string{"ordinary", "merged-only", "features-only", "both"} {
				t.Run(mode, func(t *testing.T) {
					merged, features := mode == "merged-only" || mode == "both", mode == "features-only" || mode == "both"
					args := []string{"read-configuration", "--workspace-folder", workspace}
					if selection == "explicit" {
						args = append(args, "--container-id", "sample-container")
					}
					if merged {
						args = append(args, "--include-merged-configuration")
					}
					if features {
						args = append(args, "--include-features-configuration")
					}
					result, _ := invoke(t, 0, args...)
					configuration := result["configuration"].(map[string]any)
					assertReadSnapshot(t, configuration, "container-value")
					if baseline == nil {
						baseline = configuration
					} else if !reflect.DeepEqual(configuration, baseline) {
						t.Fatalf("flag-dependent configuration: %v != %v", configuration, baseline)
					}
					for key, want := range map[string]bool{"mergedConfiguration": merged, "featuresConfiguration": features} {
						if _, present := result[key]; present != want {
							t.Fatalf("%s present=%v want=%v", key, present, want)
						}
					}
					if merged {
						snapshot := result["mergedConfiguration"].(map[string]any)
						assertReadSnapshot(t, snapshot, "container-value")
						if snapshot["remoteUser"] != "inherited" {
							t.Fatal(snapshot)
						}
					}
					if features {
						resolved := result["featuresConfiguration"].(map[string]any)
						t.Cleanup(func() { _ = os.RemoveAll(resolved["dstFolder"].(string)) })
						feature := resolved["featureSets"].([]any)[0].(map[string]any)["features"].([]any)[0].(map[string]any)
						if !reflect.DeepEqual(feature["value"], map[string]any{"marker": "container-value", "host": "host-value"}) {
							t.Fatal(feature)
						}
					}
					raw, err := json.Marshal(result)
					if err != nil {
						t.Fatal(err)
					}
					if strings.Contains(string(raw), "synthetic-secret") {
						t.Fatal("unreferenced environment disclosed")
					}
				})
			}
		})
	}
}

func assertReadSnapshot(t *testing.T, configuration map[string]any, marker string) {
	t.Helper()
	if !reflect.DeepEqual(configuration["remoteEnv"], map[string]any{"VALUE": marker, "HOST": "host-value"}) ||
		!reflect.DeepEqual(configuration["extension"], map[string]any{"values": []any{marker, "host-value"}}) ||
		!reflect.DeepEqual(configuration["features"], map[string]any{"./sample": map[string]any{"marker": marker, "host": "host-value"}}) {
		t.Fatalf("snapshot=%v", configuration)
	}
}

func TestReadConfigurationContainerLookup(t *testing.T) {
	for _, mode := range []string{"no-container", "no-container-features", "no-container-merged", "no-container-both", "offline", "features-offline", "merged-offline", "discovery-error", "inspection-error", "explicit-offline"} {
		t.Run(mode, func(t *testing.T) {
			workspace := configurationFixture(t)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case strings.HasPrefix(mode, "no-container"):
					if strings.HasSuffix(r.URL.Path, "/containers/json") {
						_, _ = w.Write([]byte(`[]`))
					} else if strings.Contains(r.URL.Path, "/images/alpine:3.20/json") {
						_, _ = w.Write([]byte(`{"Config":{}}`))
					} else {
						t.Errorf("unexpected request %s", r.URL.Path)
						http.Error(w, "unexpected", 500)
					}
				default:
					http.Error(w, `{"message":"sample lookup failure"}`, 500)
				}
			}))
			defer server.Close()
			host := "tcp://" + strings.TrimPrefix(server.URL, "http://")
			if strings.Contains(mode, "offline") {
				server.Close()
			}
			t.Setenv("DOCKER_HOST", host)
			t.Setenv("DOCKER_API_VERSION", "1.47")
			args := []string{"read-configuration", "--workspace-folder", workspace}
			if mode == "inspection-error" || mode == "explicit-offline" {
				args = append(args, "--container-id", "sample-container")
			}
			merged := strings.Contains(mode, "merged") || strings.Contains(mode, "both")
			if merged {
				args = append(args, "--include-merged-configuration")
			}
			if strings.Contains(mode, "features") || strings.Contains(mode, "both") {
				args = append(args, "--include-features-configuration")
			}
			code := 0
			if mode == "features-offline" || mode == "merged-offline" || mode == "discovery-error" || mode == "inspection-error" || mode == "explicit-offline" {
				code = 1
			}
			result, _ := invoke(t, code, args...)
			if code == 0 {
				if result["configuration"].(map[string]any)["remoteEnv"].(map[string]any)["VALUE"] != "${containerEnv:SAMPLE_VALUE}" {
					t.Fatal(result)
				}
				if value, ok := result["mergedConfiguration"]; ok != merged {
					t.Fatal(result)
				} else if ok && value.(map[string]any)["remoteEnv"].(map[string]any)["VALUE"] != "${containerEnv:SAMPLE_VALUE}" {
					t.Fatal(value)
				}
				if _, ok := result["featuresConfiguration"]; ok {
					t.Fatal(result)
				}
			} else if result["outcome"] != "error" || result["configuration"] != nil {
				t.Fatal(result)
			}
		})
	}
}

func TestReadConfigurationLabelOnlySnapshot(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			var filters map[string]map[string]bool
			if err := json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filters); err != nil {
				t.Error(err)
			}
			if !reflect.DeepEqual(filters["label"], map[string]bool{"sample=1": true}) {
				t.Errorf("discovery filters=%v", filters)
			}
			_, _ = w.Write([]byte(`[{"Id":"sample-container"}]`))
		case strings.HasSuffix(r.URL.Path, "/containers/sample-container/json"):
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": "sample-container", "Config": map[string]any{"Labels": map[string]string{"sample": "1", "devcontainer.metadata": `[{"remoteUser":"sample"}]`}}})
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer server.Close()
	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(server.URL, "http://"))
	t.Setenv("DOCKER_API_VERSION", "1.47")
	for _, mode := range []string{"ordinary", "merged-only", "features-only", "both"} {
		t.Run(mode, func(t *testing.T) {
			args := []string{"read-configuration", "--id-label", "sample=1"}
			merged := mode == "merged-only" || mode == "both"
			if merged {
				args = append(args, "--include-merged-configuration")
			}
			if mode == "features-only" || mode == "both" {
				args = append(args, "--include-features-configuration")
			}
			result, _ := invoke(t, 0, args...)
			if !reflect.DeepEqual(result["configuration"], map[string]any{}) {
				t.Fatalf("configuration without a file must be empty: %v", result)
			}
			if _, present := result["workspace"]; present {
				t.Fatalf("label-only read must omit workspace: %v", result)
			}
			if _, present := result["mergedConfiguration"]; present != merged {
				t.Fatalf("mergedConfiguration present=%v want=%v", present, merged)
			}
			if _, present := result["featuresConfiguration"]; present {
				t.Fatalf("no features were configured: %v", result)
			}
		})
	}
}
