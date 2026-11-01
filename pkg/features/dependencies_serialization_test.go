// relationships:
//
//	references: [dependencies, info, dependencies_values_test]
package features

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestDependencyReportDistinctSurrogateOptions(t *testing.T) {
	for _, options := range []string{`"./local":"\ud800","././local":"\ud801"`, `"./local":{"x":"\ud800"},"././local":{"x":"\ud801"}`, `"./local":"\udfff","././local":"�"`} {
		w := dependencyWorkspace(t, `{"features":{`+options+`}}`)
		dependencyLocal(t, w, "local", `{"id":"local"}`)
		out, err := ResolveDependencies(nil, w)
		if err != nil || len(dependencyEntries(t, out)) != 2 || dependencyEntries(t, out)[0].ID != "./local" {
			t.Fatal(out, err)
		}
	}
}
func TestDependencyReportAliasSlotWithLegacyIDs(t *testing.T) {
	for _, tc := range []struct{ metadata, aliases, label string }{
		{`{"legacyIds":["old"]}`, `[null,"old"]`, "aliases: , old"},
		{`{"id":""}`, `[""]`, "aliases: "},
		{`{"id":"","legacyIds":["old"]}`, `["","old"]`, "aliases: , old"},
	} {
		w := dependencyWorkspace(t, `{"features":{"./local":{}}}`)
		dependencyLocal(t, w, "local", tc.metadata)
		n := &infoNode{id: "./local", kind: "user-provided", options: json.RawMessage(`{}`), configPath: filepath.Join(w, ".devcontainer", "devcontainer.json"), workspace: w}
		if _, err := n.loadLocal(true); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(n.raw()), `"featureIdAliases":`+tc.aliases) {
			t.Fatal(string(n.raw()), tc.aliases)
		}
		out, err := ResolveDependencies(nil, w)
		if err != nil || !strings.Contains(out, tc.label) || !strings.Contains(out, n.label()) {
			t.Fatal(out, err)
		}
	}
}
func TestDependencyReportNonemptyArrayMetadataProperties(t *testing.T) {
	r := newFakeRegistry(t)
	r.publish("collection/sample", "1", `{"id":"sample"}`, `[{"id":"nested"}]`)
	w := dependencyWorkspace(t, `{"features":{"`+r.host()+`/collection/sample:1":{}}}`)
	n := &infoNode{id: r.host() + "/collection/sample:1", kind: "user-provided", options: json.RawMessage(`{}`), configPath: filepath.Join(w, ".devcontainer", "devcontainer.json"), workspace: w}
	if _, err := n.load(r.resolver().OCI, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(n.set), `"0":{"id":"nested"}`) {
		t.Fatal(string(n.set))
	}
	out, err := ResolveDependencies(r.resolver().OCI, w)
	if err != nil || !strings.Contains(out, n.label()) || len(dependencyEntries(t, out)) != 1 {
		t.Fatal(out, err)
	}
}
