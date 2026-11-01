// relationships:
//
//	references: [spec, ../../reference/spec/schemas/devContainer.base.schema.json]
package spec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDockerfileContextSchemaForms(t *testing.T) {
	for _, tc := range []struct{ name, doc, context, dockerfile string }{
		{"top parent", `{"dockerFile":"docker/Dockerfile","context":".."}`, "..", "docker/Dockerfile"},
		{"top current", `{"dockerFile":"docker/Dockerfile","context":"."}`, ".", "docker/Dockerfile"},
		{"top default", `{"dockerFile":"docker/Dockerfile"}`, "docker", "docker/Dockerfile"},
		{"top build options", `{"dockerFile":"docker/Dockerfile","context":"payload","build":{"args":{"VALUE":"sample"},"target":"final","cacheFrom":"alpine"}}`, "payload", "docker/Dockerfile"},
		{"nested parent", `{"build":{"dockerfile":"docker/Dockerfile","context":".."}}`, "..", "docker/Dockerfile"},
		{"nested default", `{"build":{"dockerfile":"docker/Dockerfile"}}`, "docker", "docker/Dockerfile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "devcontainer.json")
			if err := os.WriteFile(p, []byte(tc.doc), 0600); err != nil {
				t.Fatal(err)
			}
			dc, err := Load(p)
			if err != nil {
				t.Fatal(err)
			}
			got, err := dc.BuildContextDir()
			if err != nil || got != filepath.Join(dir, tc.context) {
				t.Fatalf("context=%q, %v", got, err)
			}
			if got := dc.DockerfileFile(); got != filepath.Join(dir, tc.dockerfile) {
				t.Fatalf("dockerfile=%q", got)
			}
		})
	}
}

func TestStringOrSliceSchemaForms(t *testing.T) {
	for _, field := range []string{"compose", "cache"} {
		for _, tc := range []struct {
			input string
			valid bool
			want  StringOrSlice
		}{
			{`"sample"`, true, StringOrSlice{"sample"}},
			{`["sample","other"]`, true, StringOrSlice{"sample", "other"}},
			{`[]`, true, StringOrSlice{}},
			{`{"sample":"other"}`, false, nil},
			{`{"sample":["other"]}`, false, nil},
			{`["sample",7]`, false, nil},
		} {
			t.Run(field+tc.input, func(t *testing.T) {
				var dc DevContainer
				doc := `{"dockerComposeFile":` + tc.input + `}`
				if field == "cache" {
					doc = `{"build":{"dockerfile":"Dockerfile","cacheFrom":` + tc.input + `}}`
				}
				err := json.Unmarshal([]byte(doc), &dc)
				if (err == nil) != tc.valid {
					t.Fatalf("decode=%v, valid=%v", err, tc.valid)
				}
				if tc.valid {
					got := dc.DockerComposeFile
					if field == "cache" {
						got = dc.Build.CacheFrom
					}
					if !reflect.DeepEqual(got, tc.want) {
						t.Fatalf("got=%v want=%v", got, tc.want)
					}
				}
			})
		}
	}
}

func TestParallelCommandSchemaForms(t *testing.T) {
	for _, tc := range []struct {
		input string
		valid bool
	}{
		{`"echo sample"`, true}, {`["echo","sample"]`, true},
		{`{"first":"echo sample","second":["echo","other"]}`, true},
		{`{"outer":{"inner":"echo sample"}}`, false},
	} {
		t.Run(tc.input, func(t *testing.T) {
			var c Command
			err := json.Unmarshal([]byte(tc.input), &c)
			if (err == nil) != tc.valid {
				t.Fatalf("decode=%v valid=%v", err, tc.valid)
			}
			if tc.valid {
				data, err := json.Marshal(c)
				if err != nil {
					t.Fatal(err)
				}
				var got, want any
				if err := json.Unmarshal(data, &got); err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal([]byte(tc.input), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("roundtrip=%s", data)
				}
			}
		})
	}
}
