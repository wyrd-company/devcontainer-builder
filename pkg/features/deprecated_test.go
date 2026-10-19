// relationships:
//
//	references: [deprecated, mapping_test, registry_test]
package features

import (
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestDeprecatedFeatureOptionsDifferential(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("optional upstream differential test requires Node")
	}
	source, err := os.ReadFile("../../reference/cli/src/spec-configuration/containerFeaturesConfiguration.ts")
	if os.IsNotExist(err) {
		t.Skip("reference submodule is not initialized")
	}
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	start := strings.Index(text, "const deprecatedFeaturesIntoOptions: Record")
	if start < 0 {
		t.Fatal("upstream deprecated feature preprocessing missing")
	}
	end := strings.Index(text[start:], "export async function getFeatureIdType")
	if start < 0 || end < 0 {
		t.Fatal("upstream deprecated feature preprocessing missing")
	}
	js := text[start : start+end]
	js = strings.Replace(js, ": Record<string, { mapTo: string; withOptions: any }>", "", 1)
	js = strings.Replace(js, "export function updateDeprecatedFeaturesIntoOptions(userFeatures: DevContainerFeature[] | undefined, output: Log)", "function updateDeprecatedFeaturesIntoOptions(userFeatures, output)", 1)
	cases := []string{
		`{"gradle":true,"maven":true}`,
		`{"maven":true,"java":"17"}`,
		`{"gradle":true,"ghcr.io/devcontainers/features/java:2":{"version":"21","installGradle":false,"other":"kept"}}`,
		`{"jupyterlab":false,"ghcr.io/devcontainers/features/python":{"version":"3"}}`,
		`{"./sample":{},"jupyterlab":true,"gradle":false}`,
		`{"ghcr.io/devcontainers/features/java:2":true,"maven":false}`,
		`{"gradle":true,"java:2":{"other":"kept"}}`,
		`{}`,
	}
	var corpus [][]map[string]any
	var inputs []spec.OrderedMap
	for _, raw := range cases {
		input, err := spec.ParseFeatures([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, input)
		list := []map[string]any{}
		for _, id := range input.Keys {
			var value any
			if err := json.Unmarshal(input.Values[id], &value); err != nil {
				t.Fatal(err)
			}
			list = append(list, map[string]any{"userFeatureId": id, "options": value})
		}
		corpus = append(corpus, list)
	}
	data, _ := json.Marshal(corpus)
	script := "const LogLevel={Warning:0,Trace:1};\n" + js + "\nconsole.log(JSON.stringify(" + string(data) + ".map(input=>updateDeprecatedFeaturesIntoOptions(input,{write(){}}))))"
	output, err := exec.Command(node, "-e", script).CombinedOutput()
	if err != nil {
		t.Fatalf("upstream preprocessing: %v: %s", err, output)
	}
	var expected []any
	if err := json.Unmarshal(output, &expected); err != nil {
		t.Fatal(err)
	}
	for i, input := range inputs {
		t.Run(cases[i], func(t *testing.T) {
			before, _ := json.Marshal(input.Values)
			out, err := New().upgradeDeprecatedFeatureOptions(input)
			if err != nil {
				t.Fatal(err)
			}
			got := []any{}
			for _, id := range out.Keys {
				var value any
				if err := json.Unmarshal(out.Values[id], &value); err != nil {
					t.Fatal(err)
				}
				got = append(got, map[string]any{"userFeatureId": id, "options": value})
			}
			if !reflect.DeepEqual(got, expected[i]) {
				t.Fatalf("got=%+v want=%+v", got, expected[i])
			}
			after, _ := json.Marshal(input.Values)
			if string(before) != string(after) {
				t.Fatal("input options mutated")
			}
		})
	}
}

func TestResolveDeprecatedFeatureOptions(t *testing.T) {
	for _, skip := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "skip"}[skip], func(t *testing.T) {
			reg := newFakeRegistry(t)
			reg.publish("devcontainers/features/java", "1", `{"id":"sample","version":"1.0.0"}`, "")
			reg.publish("devcontainers/features/python", "1", `{"id":"sample","version":"1.0.0"}`, "")
			r := reg.resolver()
			r.SkipAutoMapping = skip
			r.OCI.Auth = nil
			r.OCI.HTTP.Transport = mappingTransport{target: reg.host(), next: r.OCI.HTTP.Transport}
			got, err := r.Resolve(userFeatures("gradle", `true`, "maven", `false`, "jupyterlab", `true`), nil, t.TempDir(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 2 {
				t.Fatalf("resolved=%+v", got)
			}
			found := map[string]map[string]any{}
			for _, f := range got {
				found[f.Source] = f.Options
			}
			if !reflect.DeepEqual(found["ghcr.io/devcontainers/features/java:1"], map[string]any{"installGradle": true, "installMaven": true}) || !reflect.DeepEqual(found["ghcr.io/devcontainers/features/python:1"], map[string]any{"installJupyterlab": true}) {
				t.Fatalf("resolved options=%+v", found)
			}
		})
	}
}
