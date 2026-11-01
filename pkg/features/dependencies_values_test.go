// relationships:
//
//	references: [dependencies, dependencies_remediation_test]
package features

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestDependencyReportNestedWorkspaceContext(t *testing.T) {
	t.Setenv("SAMPLE_FOLDER", "/sample/env")
	for _, compose := range []bool{false, true} {
		for _, folder := range []string{"/code/${localWorkspaceFolderBasename}", "${localWorkspaceFolder}", "${localEnv:SAMPLE_FOLDER}", ""} {
			config := map[string]any{"workspaceFolder": folder, "features": map[string]any{"./local": map[string]any{"nested": []any{"${containerWorkspaceFolder}", "${containerWorkspaceFolderBasename}"}}}}
			if compose {
				config["dockerComposeFile"] = "compose.yml"
			}
			data, _ := json.Marshal(config)
			w := dependencyWorkspace(t, string(data))
			dependencyLocal(t, w, "local", `{"id":"local"}`)
			out, err := ResolveDependencies(nil, w)
			if err != nil {
				t.Fatal(err)
			}
			want := folder
			switch folder {
			case "/code/${localWorkspaceFolderBasename}":
				want = "/code/" + filepath.Base(w)
			case "${localWorkspaceFolder}":
				want = w
			case "${localEnv:SAMPLE_FOLDER}":
				want = "/sample/env"
			case "":
				if compose {
					want = "/"
				} else {
					want = "/workspaces/" + filepath.Base(w)
				}
			}
			base := filepath.Base(want)
			if want == "/" {
				base = ""
			}
			nested := dependencyEntries(t, out)[0].Options.(map[string]any)["nested"].([]any)
			if nested[0] != want || nested[1] != base {
				t.Fatal(out, want, base)
			}
		}
	}
}
func TestDependencyReportLoneSurrogates(t *testing.T) {
	w := dependencyWorkspace(t, `{"features":{"./local":{"high":"\ud800","low":"\udfff","mixed":"\ud800${localWorkspaceFolderBasename}\udfff"}}}`)
	dependencyLocal(t, w, "local", `{"id":"local"}`)
	out, err := ResolveDependencies(nil, w)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"high": "\ud800"`) || !strings.Contains(out, `"low": "\udfff"`) || !strings.Contains(out, `"mixed": "\ud800`+filepath.Base(w)+`\udfff"`) || strings.Contains(out, "�") {
		t.Fatal(out)
	}
}
func TestDependencyReportRuntimeMetadataShapes(t *testing.T) {
	for _, metadata := range []string{`[]`, `{}`} {
		r := newFakeRegistry(t)
		r.publish("collection/sample", "1", `{"id":"sample"}`, metadata)
		w := dependencyWorkspace(t, `{"features":{"`+r.host()+`/collection/sample:1":{}}}`)
		n := &infoNode{id: r.host() + "/collection/sample:1", kind: "user-provided", options: json.RawMessage(`{}`), configPath: filepath.Join(w, ".devcontainer", "devcontainer.json"), workspace: w}
		if _, err := n.load(r.resolver().OCI, true); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(n.raw()), `"featureIdAliases":[null]`) {
			t.Fatal(string(n.raw()))
		}
		out, err := ResolveDependencies(r.resolver().OCI, w)
		if err != nil || !strings.Contains(out, "<br>aliases: ]") || !strings.Contains(out, n.label()) || len(dependencyEntries(t, out)) != 1 {
			t.Fatal(out, err)
		}
	}
}
