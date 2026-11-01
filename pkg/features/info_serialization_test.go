// relationships:
//
// references: [info, info_test, ../../internal/collectiondocs/jsonc_value]
package features

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestInfoEscapedUnicodeReportsRemainValid(t *testing.T) {
	r := newFakeRegistry(t)
	meta := `{"id":"sample","description":"Literal \\u003c \\u003e \\u0026"}`
	r.publish("collection/sample", "1", meta, meta)
	r.manifests["collection/sample/tags/list"] = []byte(`{"tags":["1"]}`)
	for _, mode := range []string{"manifest", "dependencies", "verbose"} {
		for _, format := range []string{"text", "json"} {
			got, err := Info(r.resolver().OCI, mode, r.host()+"/collection/sample:1", format)
			if err != nil || strings.TrimSpace(got) == "" {
				t.Fatalf("%s/%s: %q %v", mode, format, got, err)
			}
			if format == "json" && !json.Valid([]byte(got)) {
				t.Fatalf("invalid report: %s", got)
			}
			if format == "text" && mode != "manifest" && !strings.Contains(got, "flowchart\n") {
				t.Fatalf("missing graph: %s", got)
			}
		}
	}
}

func TestInfoNodeHashUsesJavaScriptSerialization(t *testing.T) {
	raw := json.RawMessage(`{"html":"<>&","literal":"\\u003c","line":"x\u2028y\u2029z","zero":-0,"tiny":1e-7,"big":1e20}`)
	want := "{\"html\":\"<>&\",\"literal\":\"\\\\u003c\",\"line\":\"x\u2028y\u2029z\",\"zero\":0,\"tiny\":1e-7,\"big\":100000000000000000000}"
	if got := string(infoNormalize(raw)); got != want {
		t.Fatalf("got %s; want %s", got, want)
	}
	n := &infoNode{id: "sample", kind: "resolved", options: infoNormalize(raw)}
	expectedRaw := `{"type":"resolved","userFeatureId":"sample","options":` + want + `,"dependsOn":[],"installsAfter":[],"roundPriority":0}`
	sum := sha256.Sum256([]byte(expectedRaw))
	if got, want := n.label(), fmt.Sprintf("%x[sample<br/><0>]", sum[:3]); got != want {
		t.Fatalf("hash label %s want %s; raw=%s", got, want, n.raw())
	}
}

func TestInfoPrettyRejectsInvalidJSON(t *testing.T) {
	if got, err := infoPretty([]byte(`{"invalid":`), 4); err == nil || got != "" {
		t.Fatalf("%q %v", got, err)
	}
}
