// relationships:
//
//	references: [options_configuration, config, metadata]
package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
)

func TestConfigurationOptionRegistration(t *testing.T) {
	root := (&application{}).command()
	for _, name := range []string{"up", "set-up", "read-configuration", "run-user-commands"} {
		cmd, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatal(err)
		}
		for _, flag := range []string{"include-configuration", "include-merged-configuration", "include-features-configuration"} {
			want := name == "read-configuration" && flag != "include-configuration" || (name == "up" || name == "set-up") && flag != "include-features-configuration"
			f := cmd.Flags().Lookup(flag)
			if (f != nil) != want {
				t.Fatalf("%s --%s present=%v want=%v", name, flag, f != nil, want)
			}
			if want && (f.Value.Type() != "bool" || f.DefValue != "false" || f.Hidden) {
				t.Fatalf("flag=%+v", f)
			}
		}
	}
}

func configurationFixture(t *testing.T) string {
	t.Helper()
	workspace := t.TempDir()
	body := `{"image":"alpine:3.20","remoteEnv":{"VALUE":"${containerEnv:SAMPLE_VALUE}"},"extension":{"enabled":true}}`
	if err := os.WriteFile(filepath.Join(workspace, ".devcontainer.json"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return workspace
}

func TestConfigurationResultControls(t *testing.T) {
	workspace := configurationFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": "sample-container", "Config": map[string]any{"Env": []string{"SAMPLE_VALUE=container-value", "UNREFERENCED_SECRET=synthetic-secret"}, "Labels": map[string]string{"devcontainer.metadata": `[{"remoteUser":"inherited","postCreateCommand":"echo base"}]`}}})
	}))
	defer server.Close()
	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(server.URL, "http://"))
	t.Setenv("DOCKER_API_VERSION", "1.47")
	dc, _, err := (configOptions{workspace: workspace}).load()
	if err != nil {
		t.Fatal(err)
	}
	cli, err := newDockerClient()
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	for _, mode := range []string{"default", "configuration", "merged", "both"} {
		t.Run(mode, func(t *testing.T) {
			a := &application{result: map[string]any{}}
			options := configurationOptions{configuration: mode == "configuration" || mode == "both", merged: mode == "merged" || mode == "both"}
			cmd := &cobra.Command{}
			cmd.SetContext(context.Background())
			if err := options.result(a, cmd, cli, dc, workspace, "sample-container"); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(a.result)
			if err != nil {
				t.Fatal(err)
			}
			var result map[string]any
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
			for key, want := range map[string]bool{"configuration": options.configuration, "mergedConfiguration": options.merged} {
				v, present := result[key]
				if present != want {
					t.Fatalf("%s present=%v want=%v", key, present, want)
				}
				if !present {
					continue
				}
				config := v.(map[string]any)
				if config["remoteEnv"].(map[string]any)["VALUE"] != "container-value" || config["extension"].(map[string]any)["enabled"] != true {
					t.Fatalf("config=%v", config)
				}
				if key == "mergedConfiguration" && config["remoteUser"] != "inherited" {
					t.Fatalf("config=%v", config)
				}
			}
			if strings.Contains(string(raw), "synthetic-secret") {
				t.Fatalf("unreferenced runtime secret disclosed: %s", raw)
			}
		})
	}
}

func TestReadFeaturesConfiguration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/containers/json") {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Config": map[string]any{"Env": []string{"SAMPLE_VALUE=container-value"}}})
	}))
	defer server.Close()
	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(server.URL, "http://"))
	t.Setenv("DOCKER_API_VERSION", "1.47")
	workspace := t.TempDir()
	for _, id := range []string{"declared", "extra", "dependency"} {
		dir := filepath.Join(workspace, id)
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal(err)
		}
		body := `{"id":"` + id + `","version":"1.0.0","licenseURL":"https://example.invalid/license","options":{"marker":{"type":"string","default":"unset"}}}`
		if id == "declared" {
			body = strings.TrimSuffix(body, "}") + `,"dependsOn":{"./dependency":{"marker":"dependency-value"}}}`
		}
		if err := os.WriteFile(filepath.Join(dir, "devcontainer-feature.json"), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(workspace, ".devcontainer.json"), []byte(`{"image":"alpine:3.20","features":{"./declared":{"marker":"config-value"}}}`), 0644); err != nil {
		t.Fatal(err)
	}
	base := []string{"read-configuration", "--workspace-folder", workspace}
	defaults, _ := invoke(t, 0, base...)
	if _, ok := defaults["featuresConfiguration"]; ok {
		t.Fatal("features included by default")
	}
	result, _ := invoke(t, 0, append(base, "--include-features-configuration", "--additional-features", `{"./extra":true}`)...)
	config, ok := result["featuresConfiguration"].(map[string]any)
	if !ok {
		t.Fatalf("result=%v", result)
	}
	sets := config["featureSets"].([]any)
	if len(sets) != 3 {
		t.Fatalf("sets=%v", sets)
	}
	seen := map[string]bool{}
	for i, item := range sets {
		set := item.(map[string]any)
		source := set["sourceInformation"].(map[string]any)
		feature := set["features"].([]any)[0].(map[string]any)
		id := source["userFeatureId"].(string)
		seen[id] = true
		if source["type"] != "file-path" || source["resolvedFilePath"] != filepath.Join(workspace, strings.TrimPrefix(id, "./")) || set["internalVersion"] != "2" || feature["included"] != true || feature["licenseURL"] != "https://example.invalid/license" {
			t.Fatalf("set=%v", set)
		}
		if i == 0 && id != "./dependency" {
			t.Fatalf("dependency did not install first: %v", sets)
		}
		if id == "./declared" && feature["value"].(map[string]any)["marker"] != "config-value" {
			t.Fatal(feature)
		}
		if id == "./extra" && feature["value"] != true {
			t.Fatal(feature)
		}
	}
	if !seen["./declared"] || !seen["./extra"] || !seen["./dependency"] {
		t.Fatal(seen)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".devcontainer.json"), []byte(`{"image":"alpine:3.20","features":{"./extra":{"marker":"${containerEnv:SAMPLE_VALUE}"}}}`), 0644); err != nil {
		t.Fatal(err)
	}
	existing, _ := invoke(t, 0, append(base, "--include-features-configuration", "--container-id", "sample-container")...)
	existingFeature := existing["featuresConfiguration"].(map[string]any)["featureSets"].([]any)[0].(map[string]any)["features"].([]any)[0].(map[string]any)
	if existingFeature["value"].(map[string]any)["marker"] != "container-value" {
		t.Fatal(existingFeature)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".devcontainer.json"), []byte(`{"image":"alpine:3.20"}`), 0644); err != nil {
		t.Fatal(err)
	}
	empty, _ := invoke(t, 0, append(base, "--include-features-configuration")...)
	if _, ok := empty["featuresConfiguration"]; ok {
		t.Fatal("empty feature configuration must be omitted")
	}
}

func TestResolvedOCIFeatureConfiguration(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "devcontainer-feature.json"), []byte(`{"id":"sample","version":"1.0.0","name":"Sample"}`), 0644); err != nil {
		t.Fatal(err)
	}
	ref := oci.Reference{Registry: "registry.example.invalid", Repository: "collection/sample", Tag: "1"}
	manifest := &oci.Manifest{SchemaVersion: 2}
	for _, digest := range []string{"", "sha256:" + strings.Repeat("a", 64)} {
		ref.Digest = digest
		f := features.Resolved{Source: ref.String(), Reference: &ref, Manifest: manifest, Digest: "sha256:" + strings.Repeat("b", 64), Dir: dir, Value: "selected"}
		set, err := resolvedFeatureSet(f, 2, dir)
		if err != nil {
			t.Fatal(err)
		}
		source := set["sourceInformation"].(map[string]any)
		parsed := source["featureRef"].(map[string]any)
		if source["type"] != "oci" || source["manifest"] != manifest || source["manifestDigest"] != f.Digest || source["userFeatureIdWithoutVersion"] != "registry.example.invalid/collection/sample" || set["computedDigest"] != f.Digest {
			t.Fatal(set)
		}
		if parsed["registry"] != ref.Registry || parsed["namespace"] != "collection" || parsed["path"] != "collection/sample" || parsed["owner"] != "collection" || parsed["id"] != "sample" {
			t.Fatal(parsed)
		}
		if digest == "" && (parsed["version"] != "1" || parsed["tag"] != "1") || digest != "" && (parsed["version"] != digest || parsed["digest"] != digest) {
			t.Fatal(parsed)
		}
		feature := set["features"].([]any)[0].(map[string]any)
		if feature["value"] != "selected" || feature["name"] != "Sample" || feature["consecutiveId"] != "sample_2" {
			t.Fatal(feature)
		}
	}
}
