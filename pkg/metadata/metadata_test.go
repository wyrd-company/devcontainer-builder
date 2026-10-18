// relationships:
//
//	references: [metadata, merge, requirements]
package metadata

import (
	"encoding/json"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func parse(t *testing.T, value string) []Entry {
	t.Helper()
	entries, err := Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}
func TestLabelCodec(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        int
		invalid     bool
	}{
		{"absent", "", 0, false}, {"array", `[{"remoteUser":"sample"}]`, 1, false},
		{"object", `{"remoteUser":"sample"}`, 1, false}, {"empty", "[]", 0, false},
		{"null", "null", 0, true}, {"scalar", "1", 0, true}, {"string", `"entry"`, 0, true},
		{"invalid", "[", 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries, err := Parse(tc.input)
			if (err != nil) != tc.invalid || len(entries) != tc.want {
				t.Fatalf("entries=%v err=%v", entries, err)
			}
			if err == nil {
				value, err := Encode(entries)
				if err != nil || !strings.HasPrefix(value, "[") {
					t.Fatalf("encode=%q %v", value, err)
				}
				if _, err := Parse(value); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	logged := false
	if got := Read(map[string]string{Label: "invalid"}, func(string, ...any) { logged = true }); len(got) != 0 || !logged {
		t.Fatalf("invalid fallback=%v logged=%v", got, logged)
	}
}

func TestCommandRoundTrip(t *testing.T) {
	for _, value := range []string{`"echo sample"`, `["printf","sample"]`, `{"one":"echo one","two":["echo","two"]}`} {
		entries := parse(t, `[{"postCreateCommand":`+value+`}]`)
		encoded, err := Encode(entries)
		if err != nil {
			t.Fatal(err)
		}
		if encoded != `[{"postCreateCommand":`+value+`}]` {
			t.Fatalf("roundtrip=%s", encoded)
		}
	}
}

func TestCollect(t *testing.T) {
	truth := true
	dc := &spec.DevContainer{Image: "alpine", RemoteUser: "sample", RunArgs: []string{"--read-only"}}
	entries := Collect(parse(t, `{"id":"base"}`), []features.Resolved{
		{Source: "./second", Metadata: features.Metadata{ID: "second", Init: &truth, Customizations: map[string]any{"editor": map[string]any{"value": "sample"}}, ContainerEnv: map[string]string{"SAMPLE": "image-only"}}},
		{Source: "./first", Metadata: features.Metadata{ID: "first", PostCreateCommand: spec.Command{Shell: "echo first"}}},
	}, dc)
	value, err := Encode(entries)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 || entries[0].ID != "base" || entries[1].ID != "./second" || entries[2].ID != "./first" || entries[3].RemoteUser != "sample" {
		t.Fatalf("order=%s", value)
	}
	for _, forbidden := range []string{"image-only", "runArgs", "alpine", "\"Image\"", "\"Shell\""} {
		if strings.Contains(value, forbidden) {
			t.Fatalf("unexpected %s in %s", forbidden, value)
		}
	}
	if got := Collect(nil, nil, &spec.DevContainer{Image: "alpine"}); len(got) != 0 {
		t.Fatalf("empty config entry=%v", got)
	}
	updated := Update(entries, &spec.DevContainer{RemoteUser: "other", Privileged: &truth, PostCreate: spec.Command{Shell: "not persisted"}})
	if len(updated) != 5 || updated[4].RemoteUser != "other" || updated[4].Privileged != nil || !updated[4].PostCreate.IsZero() {
		t.Fatalf("update=%v", updated[4])
	}
}

// These cases mirror mergeConfiguration, should-deduplicate-mounts, and GPU
// cases in reference/cli/src/test/imageMetadata.test.ts, with generic values.
func TestMergeUpstreamRules(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        map[string]any
	}{
		{"empty", `[]`, map[string]any{"remoteEnv": map[string]any{}, "containerEnv": map[string]any{}, "portsAttributes": map[string]any{}, "onCreateCommands": []any{}, "updateContentCommands": []any{}, "postCreateCommands": []any{}, "postStartCommands": []any{}, "postAttachCommands": []any{}}},
		{"forward-port-types", `[{"forwardPorts":["3000",3000,"localhost:4000","service:5432",3000,"localhost:3000","3000"]}]`, map[string]any{"forwardPorts": []any{"3000", float64(3000), float64(4000), "service:5432"}}},
		{"env", `[{"remoteEnv":{"ENV1":"a","ENV2":"a","ENV3":"a","ENV4":"a"}},{"remoteEnv":{"ENV1":"b","ENV2":"b"}},{"remoteEnv":{"ENV1":"config","ENV3":"config","NULL":null}}]`, map[string]any{"remoteEnv": map[string]any{"ENV1": "config", "ENV2": "b", "ENV3": "config", "ENV4": "a", "NULL": nil}}},
		{"booleans", `[{"init":true,"privileged":true,"overrideCommand":true,"updateRemoteUserUID":true},{"init":false,"privileged":false,"overrideCommand":false,"updateRemoteUserUID":false}]`, map[string]any{"init": true, "privileged": true, "overrideCommand": false, "updateRemoteUserUID": false}},
		{"union", `[{"capAdd":["A","B"],"securityOpt":["one","two"]},{"capAdd":["B","C"],"securityOpt":["two","three"]}]`, map[string]any{"capAdd": []any{"A", "B", "C"}, "securityOpt": []any{"one", "two", "three"}}},
		{"scalars", `[{"remoteUser":"first","containerUser":"root","waitFor":"postCreateCommand","userEnvProbe":"none","shutdownAction":"none"},{"remoteUser":"last","containerUser":"sample","waitFor":"postStartCommand","userEnvProbe":"loginShell","shutdownAction":"stopContainer"},{"remoteUser":""}]`, map[string]any{"remoteUser": "last", "containerUser": "sample", "waitFor": "postStartCommand", "userEnvProbe": "loginShell", "shutdownAction": "stopContainer"}},
		{"mounts", `[{"mounts":["source=one,dst=/one,type=volume","source=two,target=/two,type=volume","source=three,destination=/three,type=volume"]},{"mounts":[{"source":"four","target":"/one","type":"volume"}]},{"mounts":[{"source":"five","target":"/three","type":"volume"}]}]`, map[string]any{"mounts": []any{"source=two,target=/two,type=volume", map[string]any{"source": "four", "target": "/one", "type": "volume"}, map[string]any{"source": "five", "target": "/three", "type": "volume"}}}},
		{"collected", `[{"entrypoint":"first","customizations":{"editor":{"a":1}},"postCreateCommand":"echo first","onCreateCommand":["echo","one"]},{"entrypoint":"second","customizations":{"editor":{"b":2}},"postCreateCommand":{"parallel":["echo","two"]},"postStartCommand":"echo start","updateContentCommand":"echo update","postAttachCommand":"echo attach"}]`, map[string]any{"entrypoints": []any{"first", "second"}, "customizations": map[string]any{"editor": []any{map[string]any{"a": float64(1)}, map[string]any{"b": float64(2)}}}, "postCreateCommands": []any{"echo first", map[string]any{"parallel": []any{"echo", "two"}}}, "onCreateCommands": []any{[]any{"echo", "one"}}, "postStartCommands": []any{"echo start"}, "postAttachCommands": []any{"echo attach"}, "updateContentCommands": []any{"echo update"}}},
		{"container-env-and-ports", `[{"containerEnv":{"A":"one","B":"base"},"portsAttributes":{"80":{"label":"one"}},"forwardPorts":[80,"localhost:80",81],"otherPortsAttributes":{"label":"one"}},{"containerEnv":{"A":"two"},"portsAttributes":{"80":{"label":"two"}},"forwardPorts":[81,"service:80"],"otherPortsAttributes":{"onAutoForward":"ignore"}}]`, map[string]any{"containerEnv": map[string]any{"A": "two", "B": "base"}, "portsAttributes": map[string]any{"80": map[string]any{"label": "two"}}, "forwardPorts": []any{float64(80), float64(81), "service:80"}, "otherPortsAttributes": map[string]any{"onAutoForward": "ignore"}}},
		{"requirements", `[{"hostRequirements":{"cpus":2,"memory":"2gb","storage":"1tb","gpu":true}},{"hostRequirements":{"cpus":4,"memory":"1gb","gpu":{"cores":4}}},{"hostRequirements":{"gpu":{"memory":"8gb"}}}]`, map[string]any{"hostRequirements": map[string]any{"cpus": float64(4), "memory": "2147483648", "storage": "1099511627776", "gpu": map[string]any{"cores": float64(4), "memory": "8589934592"}}}},
		{"optional-gpu", `[{"hostRequirements":{"gpu":"optional"}},{"hostRequirements":{"gpu":"optional"}}]`, map[string]any{"hostRequirements": map[string]any{"cpus": float64(0), "gpu": "optional"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries := parse(t, tc.input)
			before, _ := Encode(entries)
			dc := &spec.DevContainer{Image: "alpine", PostCreate: spec.Command{Shell: "should be replaced"}}
			merged := Merge(dc, entries)
			data, err := json.Marshal(merged)
			if err != nil {
				t.Fatal(err)
			}
			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			for k, want := range tc.want {
				if !reflect.DeepEqual(got[k], want) {
					t.Errorf("%s = %#v want %#v\n%s", k, got[k], want, data)
				}
			}
			if got["image"] != "alpine" {
				t.Fatal("lost config image")
			}
			if _, ok := got["postCreateCommand"]; ok {
				t.Fatal("singular command retained")
			}
			after, _ := Encode(entries)
			if before != after || dc.PostCreate.Shell != "should be replaced" {
				t.Fatal("mutated input")
			}
		})
	}
}

func TestPlanMetadataOrderAndForms(t *testing.T) {
	phases := Plan(parse(t, `[{"id":"base","postCreateCommand":["echo","base"]},{"id":"feature","onCreateCommand":"echo create","postCreateCommand":{"parallel":"echo feature"}},{"postCreateCommand":"echo config"}]`))
	if len(phases) != 4 || phases[0].Origin.Source != "feature" || phases[1].Origin.Source != "base" || phases[2].Command.Parallel["parallel"].Shell != "echo feature" || phases[3].Origin.Source != "devcontainer.json" {
		t.Fatalf("plan=%+v", phases)
	}
}

func TestUpstreamLabelFixture(t *testing.T) {
	// Exact legacy label authored in upstream's image-metadata/base-image fixture.
	const fixture = `{"id":"baseFeature"}`
	data, err := os.ReadFile("../../reference/cli/src/test/configs/image-metadata/base-image/Dockerfile")
	if err == nil && !strings.Contains(string(data), strconv.Quote(fixture)) {
		t.Fatal("upstream fixture changed")
	}
	entries := parse(t, fixture)
	encoded, err := Encode(entries)
	if err != nil {
		t.Fatal(err)
	}
	if encoded != `[{"id":"baseFeature"}]` {
		t.Fatalf("label=%s", encoded)
	}
	if got := Merge(&spec.DevContainer{Image: "alpine"}, entries); *got.Init || len(got.PostCreateCommands) != 0 {
		t.Fatalf("merge=%+v", got)
	}
}

func TestMergedJSONPreservesConfigExtensionsAndFeatureShape(t *testing.T) {
	raw := []byte(`{"name":"original","build":{"dockerfile":"Dockerfile","args":{"OLD":"value"},"extension":{"enabled":true}},"features":{"./sample":{"value":true}},"extension":{"enabled":true},"remoteUser":"stale","onCreateCommand":"stale"}`)
	var dc spec.DevContainer
	if err := json.Unmarshal(raw, &dc); err != nil {
		t.Fatal(err)
	}
	dc.Raw = raw
	dc.Name = ""
	dc.Build.Dockerfile = "Containerfile"
	dc.Build.Args = nil
	m := Merge(&dc, parse(t, `[{"remoteUser":"sample"}]`))
	encoded, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if got["remoteUser"] != "sample" || !reflect.DeepEqual(got["build"], map[string]any{"dockerfile": "Containerfile", "extension": map[string]any{"enabled": true}}) || !reflect.DeepEqual(got["features"], map[string]any{"./sample": map[string]any{"value": true}}) || !reflect.DeepEqual(got["extension"], map[string]any{"enabled": true}) {
		t.Fatalf("merged=%s", encoded)
	}
	if _, ok := got["name"]; ok {
		t.Fatalf("cleared config field retained: %s", encoded)
	}
	if _, ok := got["onCreateCommand"]; ok {
		t.Fatalf("stale command=%s", encoded)
	}
	empty, err := json.Marshal(Merge(&spec.DevContainer{}, nil))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(empty), "features") {
		t.Fatalf("empty features=%s", empty)
	}
}

func TestCollectUpstreamPropertyAllowlists(t *testing.T) {
	config := `{"init":true,"privileged":true,"capAdd":["A"],"securityOpt":["B"],"mounts":["type=volume,target=/sample"],"customizations":{"editor":{"sample":true}},"onCreateCommand":"one","updateContentCommand":["echo","two"],"postCreateCommand":{"parallel":"three"},"postStartCommand":"four","postAttachCommand":"five","waitFor":"postCreateCommand","remoteUser":"sample","containerUser":"root","userEnvProbe":"none","remoteEnv":{"A":null},"containerEnv":{"B":"value"},"overrideCommand":false,"portsAttributes":{"80":{"label":"sample"}},"otherPortsAttributes":{"onAutoForward":"ignore"},"forwardPorts":[80],"shutdownAction":"none","updateRemoteUserUID":false,"hostRequirements":{"cpus":2}}`
	var dc spec.DevContainer
	if err := json.Unmarshal([]byte(config), &dc); err != nil {
		t.Fatal(err)
	}
	entries := Collect(nil, nil, &dc)
	if len(entries) != 1 {
		t.Fatalf("entries=%+v", entries)
	}
	encoded, err := json.Marshal(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	var want, got map[string]any
	if err := json.Unmarshal([]byte(config), &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	for key, value := range want {
		t.Run("config/"+key, func(t *testing.T) {
			if !reflect.DeepEqual(got[key], value) {
				t.Fatalf("%s=%#v want %#v", key, got[key], value)
			}
		})
	}
	feature := `{"id":"sample","init":true,"privileged":true,"capAdd":["A"],"securityOpt":["B"],"entrypoint":"sample","mounts":["type=volume,target=/sample"],"customizations":{"editor":{"sample":true}},"onCreateCommand":"one","updateContentCommand":["echo","two"],"postCreateCommand":{"parallel":"three"},"postStartCommand":"four","postAttachCommand":"five"}`
	var m features.Metadata
	if err := json.Unmarshal([]byte(feature), &m); err != nil {
		t.Fatal(err)
	}
	entries = Collect(nil, []features.Resolved{{Source: "./sample", Metadata: m}}, nil)
	encoded, err = json.Marshal(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	got = map[string]any{}
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	want = map[string]any{}
	if err := json.Unmarshal([]byte(feature), &want); err != nil {
		t.Fatal(err)
	}
	want["id"] = "./sample"
	for key, value := range want {
		t.Run("feature/"+key, func(t *testing.T) {
			if !reflect.DeepEqual(got[key], value) {
				t.Fatalf("%s=%#v want %#v", key, got[key], value)
			}
		})
	}
}

func TestGPURequirementsBranches(t *testing.T) {
	for _, tc := range []struct {
		name       string
		a, b, want any
	}{
		{"absent", nil, nil, nil}, {"false-left", false, true, true}, {"false-right", true, false, true},
		{"optional-only", "optional", nil, "optional"}, {"optional-both", "optional", "optional", "optional"},
		{"required-over-optional", true, "optional", map[string]any{}},
		{"maxima", map[string]any{"cores": 8, "memory": "8gb"}, map[string]any{"cores": 4, "memory": "4gb"}, map[string]any{"cores": float64(8), "memory": "8589934592"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := mergeGPU(tc.a, tc.b); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("gpu=%#v want %#v", got, tc.want)
			}
		})
	}
}
