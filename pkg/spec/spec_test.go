package spec

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestUnmarshal_AppPortVariants(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want any
	}{
		{"scalar number", `{"appPort": 3000}`, float64(3000)},
		{"scalar string", `{"appPort": "3000"}`, "3000"},
		{"array", `{"appPort": [3000, "4000"]}`, []any{float64(3000), "4000"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var dc DevContainer
			if err := json.Unmarshal([]byte(c.in), &dc); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual(dc.AppPort, c.want) {
				t.Errorf("appPort: got %#v want %#v", dc.AppPort, c.want)
			}
		})
	}
}

func TestUnmarshal_UpdateRemoteUserUID(t *testing.T) {
	cases := []struct {
		in   string
		want *bool
	}{
		{`{"updateRemoteUserUID": true}`, ptrBool(true)},
		{`{"updateRemoteUserUID": false}`, ptrBool(false)},
		{`{}`, nil},
	}
	for _, c := range cases {
		var dc DevContainer
		if err := json.Unmarshal([]byte(c.in), &dc); err != nil {
			t.Fatalf("unmarshal %q: %v", c.in, err)
		}
		switch {
		case c.want == nil && dc.UpdateRemoteUserUID != nil:
			t.Errorf("%q: want nil, got %v", c.in, *dc.UpdateRemoteUserUID)
		case c.want != nil && dc.UpdateRemoteUserUID == nil:
			t.Errorf("%q: want %v, got nil", c.in, *c.want)
		case c.want != nil && *c.want != *dc.UpdateRemoteUserUID:
			t.Errorf("%q: want %v, got %v", c.in, *c.want, *dc.UpdateRemoteUserUID)
		}
	}
}

func TestUnmarshal_ForwardPorts(t *testing.T) {
	var dc DevContainer
	if err := json.Unmarshal([]byte(`{"forwardPorts": [3000, "8080:80"]}`), &dc); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := []any{float64(3000), "8080:80"}
	if !reflect.DeepEqual(dc.ForwardPorts, want) {
		t.Errorf("forwardPorts: got %#v want %#v", dc.ForwardPorts, want)
	}
}

func ptrBool(b bool) *bool { return &b }

func TestUnmarshal_DockerComposeFile_StringAndArray(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"string", `{"dockerComposeFile": "docker-compose.yml", "service": "app"}`, []string{"docker-compose.yml"}},
		{"array", `{"dockerComposeFile": ["base.yml", "override.yml"], "service": "app"}`, []string{"base.yml", "override.yml"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var dc DevContainer
			if err := json.Unmarshal([]byte(c.in), &dc); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if !reflect.DeepEqual([]string(dc.DockerComposeFile), c.want) {
				t.Errorf("dockerComposeFile: got %#v want %#v", dc.DockerComposeFile, c.want)
			}
		})
	}
}

func TestMode(t *testing.T) {
	cases := []struct {
		name string
		dc   DevContainer
		want Mode
	}{
		{"image", DevContainer{Image: "alpine"}, ModeSingle},
		{"build", DevContainer{Build: &Build{Dockerfile: "Dockerfile"}}, ModeSingle},
		{"compose", DevContainer{DockerComposeFile: StringOrSlice{"compose.yml"}, Service: "app"}, ModeCompose},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.dc.Mode(); got != c.want {
				t.Errorf("Mode: got %v want %v", got, c.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		dc      DevContainer
		wantErr string // substring; "" means must succeed
	}{
		{
			name: "single with image",
			dc:   DevContainer{Image: "alpine"},
		},
		{
			name: "compose ok",
			dc:   DevContainer{DockerComposeFile: StringOrSlice{"compose.yml"}, Service: "app"},
		},
		{
			name:    "no fields at all",
			dc:      DevContainer{},
			wantErr: "no image, build, dockerFile, or dockerComposeFile",
		},
		{
			name:    "compose missing service",
			dc:      DevContainer{DockerComposeFile: StringOrSlice{"compose.yml"}},
			wantErr: "service is missing",
		},
		{
			name:    "compose missing file",
			dc:      DevContainer{Service: "app"},
			wantErr: "dockerComposeFile is missing",
		},
		{
			name:    "mixed compose and image",
			dc:      DevContainer{Image: "alpine", DockerComposeFile: StringOrSlice{"compose.yml"}, Service: "app"},
			wantErr: "mixes compose fields",
		},
		{
			name:    "mixed compose and build",
			dc:      DevContainer{Build: &Build{Dockerfile: "Dockerfile"}, DockerComposeFile: StringOrSlice{"compose.yml"}, Service: "app"},
			wantErr: "mixes compose fields",
		},
		{
			name: "shutdownAction none in single",
			dc:   DevContainer{Image: "alpine", ShutdownAction: ShutdownNone},
		},
		{
			name: "shutdownAction stopContainer in single",
			dc:   DevContainer{Image: "alpine", ShutdownAction: ShutdownStopContainer},
		},
		{
			name:    "shutdownAction stopCompose in single",
			dc:      DevContainer{Image: "alpine", ShutdownAction: ShutdownStopCompose},
			wantErr: "only valid in compose mode",
		},
		{
			name: "shutdownAction stopCompose in compose",
			dc:   DevContainer{DockerComposeFile: StringOrSlice{"c.yml"}, Service: "app", ShutdownAction: ShutdownStopCompose},
		},
		{
			name:    "shutdownAction stopContainer in compose",
			dc:      DevContainer{DockerComposeFile: StringOrSlice{"c.yml"}, Service: "app", ShutdownAction: ShutdownStopContainer},
			wantErr: "not valid in compose mode",
		},
		{
			name:    "shutdownAction unknown",
			dc:      DevContainer{Image: "alpine", ShutdownAction: "kill"},
			wantErr: "unknown shutdownAction",
		},
		{
			name:    "overrideCommand in single",
			dc:      DevContainer{Image: "alpine", OverrideCommand: ptrBool(true)},
			wantErr: "overrideCommand is only meaningful in compose mode",
		},
		{
			name: "overrideCommand in compose",
			dc:   DevContainer{DockerComposeFile: StringOrSlice{"c.yml"}, Service: "app", OverrideCommand: ptrBool(false)},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.dc.Validate()
			if c.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate: unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate: expected error containing %q, got nil", c.wantErr)
			}
			if !strings.Contains(err.Error(), c.wantErr) {
				t.Errorf("Validate: got %q, want substring %q", err.Error(), c.wantErr)
			}
		})
	}
}

func TestComposeFiles_Resolution(t *testing.T) {
	dir := t.TempDir()
	devDir := filepath.Join(dir, ".devcontainer")
	if err := os.MkdirAll(devDir, 0o755); err != nil {
		t.Fatal(err)
	}
	specPath := filepath.Join(devDir, "devcontainer.json")
	body := `{"dockerComposeFile": ["docker-compose.yml", "../shared/override.yml"], "service": "app"}`
	if err := os.WriteFile(specPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	dc, err := Load(specPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got := dc.ComposeFiles()
	want := []string{
		filepath.Join(devDir, "docker-compose.yml"),
		filepath.Join(dir, "shared", "override.yml"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ComposeFiles: got %v want %v", got, want)
	}
}

func TestLoad_RejectsInvalid(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "devcontainer.json")
	body := `{"image": "alpine", "service": "app"}`
	if err := os.WriteFile(specPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(specPath); err == nil {
		t.Fatal("Load: expected validation error, got nil")
	} else if !strings.Contains(err.Error(), "mixes compose fields") {
		t.Errorf("Load: error = %q, want substring %q", err.Error(), "mixes compose fields")
	}
}
