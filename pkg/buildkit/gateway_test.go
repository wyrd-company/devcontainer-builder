package buildkit

import (
	"encoding/json"
	"testing"

	dockerspec "github.com/moby/docker-image-spec/specs-go/v1"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
	"github.com/wyrd-company/devcontainer-builder/pkg/platform"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestMergeImageConfig_AppendsEnvAndOverridesUserAndWorkdir(t *testing.T) {
	base := dockerspec.DockerOCIImage{}
	base.Config.Env = []string{"PATH=/usr/bin", "EXISTING=keep"}
	base.Config.User = "1000:1000"
	base.Config.WorkingDir = "/old"
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
	out, err := mergeImageConfig(baseJSON, g, ocispecs.Platform{OS: "linux", Architecture: "arm64"})
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
	if got.Config.WorkingDir != "/workspaces/x" {
		t.Errorf("WorkingDir not overridden: %q", got.Config.WorkingDir)
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
		{"REMOTE", "yes"},
	} {
		if envIdx[want.k] != want.v {
			t.Errorf("env %s: want %q got %q", want.k, want.v, envIdx[want.k])
		}
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
