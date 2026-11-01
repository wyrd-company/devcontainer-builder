// relationships:
//
//	references: [dependencies, dependencies_test]
package features

import (
	"archive/tar"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func TestDependencyReportWorkspaceSubstitution(t *testing.T) {
	for _, tc := range []struct{ extra, want string }{
		{``, "default"},
		{`"workspaceFolder":"/sample/workspace",`, "/sample/workspace"},
		{`"dockerComposeFile":"compose.yml","workspaceFolder":"/sample/compose",`, "/sample/compose"},
		{`"workspaceFolder":"${localWorkspaceFolderBasename}",`, "basename"},
	} {
		w := dependencyWorkspace(t, `{`+tc.extra+`"features":{"./local":{"folder":"${containerWorkspaceFolder}","base":"${containerWorkspaceFolderBasename}"}}}`)
		dependencyLocal(t, w, "local", `{"id":"local"}`)
		out, err := ResolveDependencies(nil, w)
		if err != nil {
			t.Fatal(err)
		}
		want := tc.want
		if want == "basename" {
			want = filepath.Base(w)
		}
		if want == "default" {
			want = "/workspaces/" + filepath.Base(w)
		}
		options := dependencyEntries(t, out)[0].Options.(map[string]any)
		if options["folder"] != want || options["base"] != filepath.Base(want) {
			t.Fatal(out, want)
		}
	}
}
func TestDependencyReportUnicodeAndNull(t *testing.T) {
	w := dependencyWorkspace(t, `{"features":{"./local":{"text":"\ud83d\ude00","value":null,"array":[null,"\ud83d\ude00"]}}}`)
	dependencyLocal(t, w, "local", `{"id":"local"}`)
	out, err := ResolveDependencies(nil, w)
	if err != nil {
		t.Fatal(err)
	}
	opts := dependencyEntries(t, out)[0].Options.(map[string]any)
	if opts["text"] != "😀" || opts["value"] != nil || opts["array"].([]any)[0] != nil || opts["array"].([]any)[1] != "😀" {
		t.Fatal(out)
	}
	w = dependencyWorkspace(t, `{"features":{"./local":null}}`)
	dependencyLocal(t, w, "local", `{"id":"local"}`)
	out, err = ResolveDependencies(nil, w)
	if err == nil || !strings.HasPrefix(out, "flowchart\n") || strings.Contains(out, "installOrder") {
		t.Fatal(out, err)
	}
}
func TestDependencyReportArrayOptionsRemainDistinct(t *testing.T) {
	w := dependencyWorkspace(t, `{"features":{"./local":["a","b"],"././local":{"x":"a","y":"b"}}}`)
	dependencyLocal(t, w, "local", `{"id":"local"}`)
	out, err := ResolveDependencies(nil, w)
	if err != nil || len(dependencyEntries(t, out)) != 2 {
		t.Fatal(out, err)
	}
}
func TestDependencyReportTrailingPathSerialization(t *testing.T) {
	w := dependencyWorkspace(t, `{"features":{"./local/":{}}}`)
	dependencyLocal(t, w, "local", `{"id":"local"}`)
	n := &infoNode{id: "./local/", kind: "user-provided", options: json.RawMessage(`{}`), configPath: filepath.Join(w, ".devcontainer", "devcontainer.json"), workspace: w}
	if _, err := n.loadLocal(true); err != nil {
		t.Fatal(err)
	}
	var set struct {
		Source struct {
			Path string `json:"resolvedFilePath"`
		} `json:"sourceInformation"`
	}
	_ = json.Unmarshal(n.set, &set)
	if set.Source.Path != filepath.Join(w, ".devcontainer", "local")+"/" {
		t.Fatal(string(n.set))
	}
	out, err := ResolveDependencies(nil, w)
	if err != nil || !strings.Contains(out, n.label()) {
		t.Fatal(out, err)
	}
}
func TestDependencyReportMissingOCIMetadata(t *testing.T) {
	r := newFakeRegistry(t)
	blob := tarGz(t, []tar.Header{{Name: "other.json", Typeflag: tar.TypeReg, Mode: 0644}}, map[string]string{"other.json": "{}"})
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(blob))
	r.blobs[digest] = blob
	manifest := fmt.Sprintf(`{"schemaVersion":2,"config":{"mediaType":"application/vnd.devcontainers"},"layers":[{"digest":%q,"size":%d}]}`, digest, len(blob))
	r.manifests["collection/sample/manifests/1"] = []byte(manifest)
	id := r.host() + "/collection/sample:1"
	w := dependencyWorkspace(t, `{"features":{"`+id+`":{}}}`)
	out, err := ResolveDependencies(r.resolver().OCI, w)
	if err != nil || strings.Contains(out, "aliases:") || len(dependencyEntries(t, out)) != 1 {
		t.Fatal(out, err)
	}
}

func TestDependencyReportNullObjectDuplicateFailsBeforeDiagram(t *testing.T) {
	w := dependencyWorkspace(t, `{"features":{"./local":null,"././local":{}}}`)
	dependencyLocal(t, w, "local", `{"id":"local"}`)
	out, err := ResolveDependencies(nil, w)
	if err == nil || out != "" {
		t.Fatal(out, err)
	}
}
