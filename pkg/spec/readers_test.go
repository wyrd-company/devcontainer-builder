// relationships:
//
//	references: [spec, environment]
package spec

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCommonReadAndSourceRequirement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devcontainer.json")
	if err := os.WriteFile(path, []byte(`{"remoteUser":"root","remoteEnv":{"VALUE":null}}`), 0600); err != nil {
		t.Fatal(err)
	}
	dc, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := dc.ValidateSource(); err == nil {
		t.Fatal("missing execution source accepted")
	}
	if _, err := dc.BuildContextDir(); err == nil {
		t.Fatal("missing build source accepted")
	}
}
func TestEmptyComposePresence(t *testing.T) {
	dc := &DevContainer{DockerComposeFile: StringOrSlice{}, Service: "app", WorkspaceFolder: "/workspace"}
	if err := dc.Validate(); err != nil {
		t.Fatal(err)
	}
	if dc.Mode() != ModeCompose {
		t.Fatal("empty array lost compose mode")
	}
	dc.Service = ""
	if dc.Mode() != ModeCompose {
		t.Fatal("array presence lost")
	}
	if dc.Validate() == nil {
		t.Fatal("missing service accepted")
	}
	dc.Service = "app"
	dc.Image = "alpine"
	if dc.Validate() == nil {
		t.Fatal("mixed sources accepted")
	}
}
func TestEnvironmentSchemaValues(t *testing.T) {
	for _, field := range []string{"containerEnv", "remoteEnv"} {
		for _, value := range []string{`{"VALUE":"text"}`, `{"VALUE":null}`, `{"VALUE":4}`, `{"VALUE":true}`, `{"VALUE":{}}`, `{"VALUE":[]}`, `null`, `[]`, `"text"`, `4`, `true`} {
			t.Run(field+"/"+value, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "devcontainer.json")
				body := `{"image":"alpine","workspaceFolder":"/workspace","` + field + `":` + value + `}`
				if err := os.WriteFile(path, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
				_, err := Load(path)
				valid := value == `{"VALUE":"text"}` || field == "remoteEnv" && value == `{"VALUE":null}`
				if (err == nil) != valid {
					t.Fatalf("valid=%v error=%v", valid, err)
				}
			})
		}
	}
	for _, value := range []any{nil, 4, true, map[string]any{}, []any{}} {
		dc := &DevContainer{Image: "alpine", ContainerEnv: map[string]any{"VALUE": value}}
		if dc.Validate() == nil {
			t.Fatalf("constructed containerEnv accepted %T", value)
		}
		dc.ContainerEnv = nil
		dc.UpdateRemoteEnv = map[string]any{"VALUE": value}
		if (dc.Validate() == nil) != (value == nil) {
			t.Fatalf("constructed remoteEnv %T", value)
		}
	}
}
