// relationships:
//
//	references: [config, substitute]
package builder

import (
	"encoding/json"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"github.com/wyrd-company/devcontainer-builder/pkg/substitute"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBuildConfigurationSubstitution(t *testing.T) {
	dc := &spec.DevContainer{Path: "/config/devcontainer.json", Image: "${localEnv:BASE:alpine}", Context: "${localWorkspaceFolder}", Build: &spec.Build{Dockerfile: "${localEnv:FILE:Dockerfile}", Context: "${localWorkspaceFolder}", Target: "${localEnv:STAGE:final}", Args: map[string]string{"VALUE": "${localEnv:VALUE}", "FALLBACK": "${localEnv:ABSENT:fallback}", "WORKSPACE": "${localWorkspaceFolder}", "LITERAL": "${localEnv:LITERAL}", "CONTAINER": "${containerWorkspaceFolder}", "ID": "${devcontainerId}"}, CacheFrom: spec.StringOrSlice{"${localEnv:CACHE:cache}"}, Options: []string{"--label=sample=${localEnv:VALUE}"}}, Features: spec.OrderedMap{Keys: []string{"./feature"}, Values: map[string]json.RawMessage{"./feature": json.RawMessage(`{"value":"${localEnv:VALUE}"}`)}}, PostCreate: spec.Command{Shell: "${localEnv:VALUE}"}}
	before, _ := json.Marshal(dc)
	ctx := substitute.Context{ContainerWorkspaceFolder: "/workspace", IDLabels: map[string]string{"sample": "value"}}
	got, err := loadBuildSpec(Options{Spec: dc, Workspace: "/host/workspace", LocalEnv: map[string]string{"VALUE": "selected", "LITERAL": "${localEnv:VALUE}"}, Substitution: ctx})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"VALUE": "selected", "FALLBACK": "fallback", "WORKSPACE": "/host/workspace", "LITERAL": "${localEnv:VALUE}", "CONTAINER": "/workspace", "ID": substitute.Substitute(ctx, "${devcontainerId}")}
	if !reflect.DeepEqual(got.Build.Args, want) {
		t.Fatalf("args = %#v, want %#v", got.Build.Args, want)
	}
	if got.Build.Dockerfile != "Dockerfile" || got.Build.Context != "/host/workspace" || got.Build.Target != "final" || got.Build.CacheFrom[0] != "cache" || got.Build.Options[0] != "--label=sample=selected" || string(got.Features.Values["./feature"]) != `{"value":"selected"}` {
		t.Fatalf("build inputs not substituted: %#v", got)
	}
	after, _ := json.Marshal(dc)
	if string(before) != string(after) {
		t.Fatal("caller snapshot mutated")
	}
	if got.PostCreate.Shell != dc.PostCreate.Shell || got.Path != dc.Path {
		t.Fatal("runtime contribution or config path changed")
	}
}

func TestBuildConfigurationEnvironmentIsExplicit(t *testing.T) {
	t.Setenv("SAMPLE_BUILD_VALUE", "ambient")
	dc := &spec.DevContainer{Image: "${localEnv:SAMPLE_BUILD_VALUE:fallback}"}
	got, err := loadBuildSpec(Options{Spec: dc})
	if err != nil {
		t.Fatal(err)
	}
	if got.Image != "fallback" {
		t.Fatalf("image = %q", got.Image)
	}
}

func TestBuildConfigurationWorkspacePath(t *testing.T) {
	dir := t.TempDir()
	dc := &spec.DevContainer{Path: filepath.Join(dir, "devcontainer.json"), Dockerfile: "${localWorkspaceFolder}/Dockerfile"}
	got, err := loadBuildSpec(Options{Spec: dc, Workspace: dir})
	if err != nil {
		t.Fatal(err)
	}
	if got.Dockerfile != filepath.Join(dir, "Dockerfile") {
		t.Fatal(got.Dockerfile)
	}
}
