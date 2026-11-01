// relationships:
//
//	references: [dependencies, info_test]
package features

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/wyrd-company/devcontainer-builder/pkg/oci"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func dependencyWorkspace(t *testing.T, config string) string {
	t.Helper()
	w := t.TempDir()
	d := filepath.Join(w, ".devcontainer")
	if err := os.MkdirAll(d, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "devcontainer.json"), []byte(config), 0644); err != nil {
		t.Fatal(err)
	}
	return w
}
func dependencyLocal(t *testing.T, w, id, metadata string) {
	t.Helper()
	d := filepath.Join(w, ".devcontainer", id)
	if err := os.MkdirAll(d, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "devcontainer-feature.json"), []byte(metadata), 0644); err != nil {
		t.Fatal(err)
	}
}
func dependencyEntries(t *testing.T, out string) []struct {
	ID      string `json:"id"`
	Options any    `json:"options"`
} {
	t.Helper()
	_, tail, ok := strings.Cut(out, "\n\n")
	if !ok {
		t.Fatal(out)
	}
	var result struct {
		InstallOrder []struct {
			ID      string `json:"id"`
			Options any    `json:"options"`
		} `json:"installOrder"`
	}
	if err := json.Unmarshal([]byte(tail), &result); err != nil {
		t.Fatal(err, out)
	}
	return result.InstallOrder
}

func TestResolveDependenciesLocalOrderingAndSubstitution(t *testing.T) {
	t.Setenv("SAMPLE_OPTION", "chosen")
	w := dependencyWorkspace(t, `{"features":{"./root":{"z":true,"a":"${localEnv:SAMPLE_OPTION}"},"./leaf":false},"overrideFeatureInstallOrder":["./root","./leaf"]}`)
	dependencyLocal(t, w, "root", `{"id":"root","dependsOn":{"./leaf":false}}`)
	dependencyLocal(t, w, "leaf", `{"id":"leaf"}`)
	out, err := ResolveDependencies(nil, w)
	if err != nil {
		t.Fatal(err)
	}
	entries := dependencyEntries(t, out)
	if len(entries) != 2 || entries[0].ID != "./leaf" || entries[0].Options != false || entries[1].ID != "./root" || entries[1].Options.(map[string]any)["a"] != "chosen" || !strings.Contains(out, " --> ") || !strings.Contains(out, "<2>") {
		t.Fatal(out)
	}
}
func TestResolveDependenciesSoftAbsentAndPresent(t *testing.T) {
	for _, present := range []bool{false, true} {
		config := `{"features":{"./root":{}`
		if present {
			config += `,"./leaf":{}`
		}
		config += `}}`
		w := dependencyWorkspace(t, config)
		dependencyLocal(t, w, "root", `{"id":"root","installsAfter":["./leaf"]}`)
		dependencyLocal(t, w, "leaf", `{"id":"leaf"}`)
		out, err := ResolveDependencies(nil, w)
		if err != nil {
			t.Fatal(err)
		}
		entries := dependencyEntries(t, out)
		if present && (len(entries) != 2 || entries[0].ID != "./leaf" || !strings.Contains(out, " -.-> ")) {
			t.Fatal(out)
		}
		if !present && len(entries) != 1 {
			t.Fatal(out)
		}
	}
}
func TestResolveDependenciesCycleRetainsDiagram(t *testing.T) {
	w := dependencyWorkspace(t, `{"features":{"./root":{}}}`)
	dependencyLocal(t, w, "root", `{"id":"root","dependsOn":{"./root":{}}}`)
	out, err := ResolveDependencies(nil, w)
	if err == nil || !strings.Contains(out, " --> ") || strings.Contains(out, "installOrder") {
		t.Fatal(out, err)
	}
}
func TestResolveDependenciesEmptyAndMissing(t *testing.T) {
	for _, config := range []string{`{}`, `{"features":{}}`} {
		w := dependencyWorkspace(t, config)
		out, err := ResolveDependencies(nil, w)
		if err == nil || out != "" {
			t.Fatal(out, err)
		}
	}
	if _, err := ResolveDependencies(nil, t.TempDir()); err == nil {
		t.Fatal("missing config accepted")
	}
}
func TestResolveDependenciesDiscovery(t *testing.T) {
	w := dependencyWorkspace(t, `{"features":{"./missing":{}}}`)
	dependencyLocal(t, w, "leaf", `{"id":"leaf"}`)
	if err := os.WriteFile(filepath.Join(w, ".devcontainer.json"), []byte(`{"features":{"./.devcontainer/leaf":true}}`), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := ResolveDependencies(nil, w)
	if err != nil || dependencyEntries(t, out)[0].Options != true {
		t.Fatal(out, err)
	}
}
func TestResolveDependenciesOverrideAndPathBoundary(t *testing.T) {
	w := dependencyWorkspace(t, `{"features":{"./alpha":{},"./zeta":{}},"overrideFeatureInstallOrder":["./zeta","./alpha"]}`)
	dependencyLocal(t, w, "alpha", `{"id":"alpha"}`)
	dependencyLocal(t, w, "zeta", `{"id":"zeta"}`)
	out, err := ResolveDependencies(nil, w)
	if err != nil || dependencyEntries(t, out)[0].ID != "./zeta" {
		t.Fatal(out, err)
	}
	w = dependencyWorkspace(t, `{"features":{"../escape":{}}}`)
	if _, err = ResolveDependencies(nil, w); err == nil {
		t.Fatal("escaped path accepted")
	}
}
func TestResolveDependenciesOCIAndLockfile(t *testing.T) {
	r := newFakeRegistry(t)
	id := r.host() + "/collection/sample:1"
	r.publish("collection/sample", "1", `{"id":"sample","version":"1.0.0"}`, "")
	body := r.manifests["collection/sample/manifests/1"]
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(body))
	r.manifests["collection/sample/manifests/"+digest] = body
	w := dependencyWorkspace(t, `{"features":{"`+id+`":"chosen"}}`)
	lock := filepath.Join(w, ".devcontainer", "devcontainer-lock.json")
	data := `{"features":{"` + id + `":{"integrity":"` + digest + `"}}}`
	if err := os.WriteFile(lock, []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	out, err := ResolveDependencies(r.resolver().OCI, w)
	if err != nil {
		t.Fatal(err)
	}
	entries := dependencyEntries(t, out)
	if len(entries) != 1 || entries[0].ID != r.host()+"/collection/sample@"+digest || entries[0].Options != "chosen" {
		t.Fatal(out)
	}
	after, _ := os.ReadFile(lock)
	if string(after) != data {
		t.Fatal("lockfile changed")
	}
}

func TestResolveDependenciesLocaleOptionOrder(t *testing.T) {
	w := dependencyWorkspace(t, `{"features":{"./local":"Z","././local":"a"}}`)
	dependencyLocal(t, w, "local", `{"id":"local"}`)
	out, err := ResolveDependencies(nil, w)
	if err != nil {
		t.Fatal(err)
	}
	entries := dependencyEntries(t, out)
	if len(entries) != 2 || entries[0].Options != "a" {
		t.Fatal(out)
	}
}

func TestResolveDependenciesTarballIntegrityMetadataFallback(t *testing.T) {
	r := newFakeRegistry(t)
	r.publish("collection/sample", "1", `{"id":"sample","version":"1.0.0"}`, "")
	var blob []byte
	for _, b := range r.blobs {
		blob = b
		break
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(blob) }))
	defer server.Close()
	client := oci.NewClient()
	client.HTTP = server.Client()
	id := server.URL + "/devcontainer-feature-sample.tgz"
	w := dependencyWorkspace(t, `{"features":{"`+id+`":true}}`)
	out, err := ResolveDependencies(client, w)
	if err != nil || !strings.Contains(out, "aliases: sample") {
		t.Fatal(out, err)
	}
	lock := `{"features":{"` + id + `":{"integrity":"sha256:` + strings.Repeat("1", 64) + `"}}}`
	if err = os.WriteFile(filepath.Join(w, ".devcontainer", "devcontainer-lock.json"), []byte(lock), 0644); err != nil {
		t.Fatal(err)
	}
	out, err = ResolveDependencies(client, w)
	if err != nil || strings.Contains(out, "aliases:") || dependencyEntries(t, out)[0].ID != id {
		t.Fatal(out, err)
	}
}
