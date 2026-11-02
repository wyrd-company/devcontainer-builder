// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package builder

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestBuildAndGenerateUseConfigurationSnapshot(t *testing.T) {
	config := filepath.Join(t.TempDir(), "devcontainer.json")
	if err := os.WriteFile(config, []byte(`{"image":"alpine:3.20"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	snapshot, err := spec.Load(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte(`{"image":"busybox:latest"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	opts := Options{Docker: metadataEngine(t, nil), Spec: snapshot, SpecPath: config, Workspace: filepath.Dir(config), WorkDir: t.TempDir()}
	result, err := New().Generate(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(result.FinalDockerfile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "FROM alpine:3.20") {
		t.Fatalf("Generate independently read changed config: %s", body)
	}
	opts.WorkDir = t.TempDir()
	dc, _, _, base, _, _, _, _, _, err := New().prepareBuildContext(context.Background(), opts, func(string, ...any) {})
	if err != nil {
		t.Fatal(err)
	}
	if dc != snapshot || base != "alpine:3.20" {
		t.Fatalf("Build independently read changed config: dc=%+v base=%q", dc, base)
	}
}

func TestConfigurationSnapshotIsValidated(t *testing.T) {
	opts := Options{Spec: &spec.DevContainer{}}
	if _, err := New().Generate(context.Background(), opts); err == nil {
		t.Fatal("Generate accepted invalid snapshot")
	}
	if _, _, _, _, _, _, _, _, _, err := New().prepareBuildContext(context.Background(), opts, func(string, ...any) {}); err == nil {
		t.Fatal("Build accepted invalid snapshot")
	}
}

func TestLegacyDockerfileContextReachesBuildFrontend(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "devcontainer.json")
	if err := os.WriteFile(p, []byte(`{"dockerFile":"docker/Dockerfile","context":"payload","build":{"args":{"VALUE":"sample"},"target":"final","cacheFrom":"alpine"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	dc, err := spec.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	_, frontend, err := prepareBaseImage(dc, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if frontend.ContextDir != filepath.Join(dir, "payload") || frontend.DockerfileDir != filepath.Join(dir, "docker") || frontend.Filename != "Dockerfile" || frontend.Target != "final" || frontend.BuildArgs["VALUE"] != "sample" {
		t.Fatalf("frontend=%+v", frontend)
	}
	if got := buildCacheFrom(dc); len(got) != 1 || got[0] != "alpine" {
		t.Fatalf("cache=%v", got)
	}
}
