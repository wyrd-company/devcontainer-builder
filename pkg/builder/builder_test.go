// relationships:
//   references: [builder, metadata]

package builder

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestValidateBuildSupport_RejectsBuildOptions(t *testing.T) {
	dc := &spec.DevContainer{Build: &spec.Build{Options: []string{"--ssh", "default"}}}
	err := validateBuildSupport(dc)
	if err == nil || !strings.Contains(err.Error(), "build.options is not supported") {
		t.Fatalf("err = %v, want build.options rejection", err)
	}
	if err := validateBuildSupport(&spec.DevContainer{Build: &spec.Build{}}); err != nil {
		t.Fatalf("spec without build.options rejected: %v", err)
	}
}

func TestBuildCacheFrom_MergesSpecAndCLI(t *testing.T) {
	dc := &spec.DevContainer{Build: &spec.Build{CacheFrom: spec.StringOrSlice{"type=registry,ref=spec-cache"}}}
	got := mergeSlices(buildCacheFrom(dc), []string{"type=local,src=.cache"})
	want := []string{"type=registry,ref=spec-cache", "type=local,src=.cache"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("cacheFrom merge = %v, want %v", got, want)
	}
}

// countingTransport fails every request and counts them.
type countingTransport struct{ calls int }

func (c *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	c.calls++
	return nil, errors.New("network disabled in test")
}

func TestVSCodeServerIsOptIn(t *testing.T) {
	dir := t.TempDir()
	specPath := filepath.Join(dir, "devcontainer.json")
	if err := os.WriteFile(specPath, []byte(`{"image": "alpine:3.20"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	type entry func(*Builder, Options) (*Result, error)
	entries := map[string]entry{
		"Generate": func(b *Builder, o Options) (*Result, error) { return b.Generate(context.Background(), o) },
		// Build reaches the solve only when staging succeeds; the
		// unreachable BuildKit then fails it without a network.
		"Build": func(b *Builder, o Options) (*Result, error) {
			o.BuildKitAddress = "unix://" + filepath.Join(dir, "no-buildkitd.sock")
			o.Progress = "plain"
			o.Stderr = io.Discard
			return b.Build(context.Background(), o)
		},
	}
	for name, run := range entries {
		for _, install := range []bool{false, true} {
			rt := &countingTransport{}
			b := New()
			b.Downloader.HTTP = &http.Client{Transport: rt}
			_, err := run(b, Options{
				SpecPath:            specPath,
				Docker:              metadataEngine(t, nil),
				WorkDir:             t.TempDir(),
				InstallVSCodeServer: install,
			})
			fetchFailed := err != nil && strings.Contains(err.Error(), "vscode server")
			if install && (rt.calls == 0 || !fetchFailed) {
				t.Errorf("%s with InstallVSCodeServer: %d requests, err = %v; want the server fetch", name, rt.calls, err)
			}
			if !install && (rt.calls != 0 || fetchFailed) {
				t.Errorf("%s by default: %d requests, err = %v; want no server fetch", name, rt.calls, err)
			}
		}
	}
}

func TestPrepareBaseImage_DockerfileRelativeToConfig(t *testing.T) {
	configDir := filepath.Join(string(filepath.Separator), "work", ".devcontainer")
	cases := []struct{ context, wantContext string }{
		{".", configDir},
		{"..", filepath.Dir(configDir)},
		{"payload", filepath.Join(configDir, "payload")},
	}
	for _, c := range cases {
		t.Run(c.context, func(t *testing.T) {
			dc := &spec.DevContainer{Path: filepath.Join(configDir, "devcontainer.json"), Build: &spec.Build{Dockerfile: "Dockerfile", Context: c.context}}
			_, upstream, err := prepareBaseImage(dc, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if upstream.ContextDir != c.wantContext || upstream.DockerfileDir != configDir || upstream.Filename != "Dockerfile" {
				t.Fatalf("upstream = %+v, want context %s and Dockerfile in %s", upstream, c.wantContext, configDir)
			}
		})
	}
}
