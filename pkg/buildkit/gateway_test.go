// relationships:
//   references: [gateway, metadata]

package buildkit

import (
	"encoding/json"
	"reflect"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"github.com/wyrd-company/devcontainer-builder/pkg/platform"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestMergeImageConfig_AppendsEnvOverridesUserAndKeepsWorkdir(t *testing.T) {
	base := dockerspec.DockerOCIImage{}
	base.Config.Env = []string{"PATH=/usr/bin", "EXISTING=keep"}
	base.Config.User = "1000:1000"
	base.Config.WorkingDir = "/old"
	base.Config.Labels = map[string]string{"org.example.base": "base", "org.example.kept": "kept"}
	baseJSON, err := json.Marshal(base)
	if err != nil {
		t.Fatalf("marshal base: %v", err)
	}

	g := GraphInputs{
		Spec: &spec.DevContainer{
			ContainerEnv:    map[string]any{"DEMO": "true"},
			UpdateRemoteEnv: map[string]any{"REMOTE": "yes"},
			WorkspaceFolder: "/workspaces/x",
			RemoteUser:      "vscode",
		},
		Features: []features.Resolved{{
			Metadata: features.Metadata{ContainerEnv: map[string]string{"FROM_FEATURE": "1"}},
		}},
	}
	out, err := mergeImageConfig(baseJSON, g, map[string]string{"org.example.added": "yes", "org.example.base": "override"}, ocispecs.Platform{OS: "linux", Architecture: "arm64"})
	if err != nil {
		t.Fatalf("mergeImageConfig: %v", err)
	}
	var got dockerspec.DockerOCIImage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("decode merged: %v", err)
	}

	if got.OS != "linux" || got.Architecture != "arm64" {
		t.Fatalf("expected linux/arm64, got %s/%s", got.OS, got.Architecture)
	}
	if got.Config.WorkingDir != "/old" {
		t.Errorf("WorkingDir = %q, want the base image's /old", got.Config.WorkingDir)
	}
	if got.Config.User != "vscode" {
		t.Errorf("User not overridden: %q", got.Config.User)
	}

	envIdx := envMap(got.Config.Env)
	for _, want := range []struct{ k, v string }{
		{"PATH", "/usr/bin"},
		{"EXISTING", "keep"},
		{"DEMO", "true"},
		{"FROM_FEATURE", "1"},
	} {
		if envIdx[want.k] != want.v {
			t.Errorf("env %s: want %q got %q", want.k, want.v, envIdx[want.k])
		}
	}
	if _, ok := envIdx["REMOTE"]; ok {
		t.Errorf("remoteEnv must not be baked into image config: %+v", got.Config.Env)
	}
	entries, err := metadata.Parse(got.Config.Labels[metadata.Label])
	if err != nil || len(entries) != 2 || entries[1].RemoteUser != "vscode" {
		t.Fatalf("metadata=%v err=%v", entries, err)
	}
	delete(got.Config.Labels, metadata.Label)
	wantLabels := map[string]string{"org.example.added": "yes", "org.example.base": "override", "org.example.kept": "kept"}
	if !reflect.DeepEqual(got.Config.Labels, wantLabels) {
		t.Errorf("labels = %v, want %v", got.Config.Labels, wantLabels)
	}
}

func TestParseTargetPlatforms(t *testing.T) {
	out, err := parseTargetPlatforms([]string{"linux/amd64", "linux/arm64"}, platform.Platform{})
	if err != nil {
		t.Fatalf("parseTargetPlatforms: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 platforms, got %d", len(out))
	}
	if out[0].Architecture != "amd64" || out[1].Architecture != "arm64" {
		t.Errorf("unexpected platforms: %+v", out)
	}
}

func TestMergeImageConfig_MetadataPreservesBaseAndInstallOrder(t *testing.T) {
	base := dockerspec.DockerOCIImage{}
	base.Config.Labels = map[string]string{metadata.Label: `{"remoteUser":"sample","postCreateCommand":"echo base"}`}
	raw, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	g := GraphInputs{Spec: &spec.DevContainer{PostCreate: spec.Command{Args: []string{"echo", "config"}}}, Features: []features.Resolved{
		{Source: "./second", Metadata: features.Metadata{CapAdd: []string{"SYS_PTRACE"}, PostCreateCommand: spec.Command{Shell: "echo second"}}},
		{Source: "./first", Metadata: features.Metadata{PostCreateCommand: spec.Command{Parallel: map[string]spec.Command{"one": {Shell: "echo first"}}}}},
	}}
	raw, err = mergeImageConfig(raw, g, nil, ocispecs.Platform{OS: "linux", Architecture: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	var img dockerspec.DockerOCIImage
	if err := json.Unmarshal(raw, &img); err != nil {
		t.Fatal(err)
	}
	entries, err := metadata.Parse(img.Config.Labels[metadata.Label])
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 || entries[0].RemoteUser != "sample" || entries[1].ID != "./second" || entries[2].ID != "./first" || len(entries[3].PostCreate.Args) != 2 {
		t.Fatalf("entries=%+v", entries)
	}
}
