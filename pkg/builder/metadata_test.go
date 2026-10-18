// relationships:
//
//	references: [metadata, dockerfileuser]
package builder

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dockerclient "github.com/docker/docker/client"

	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

type metadataTestTransport func(*http.Request) (*http.Response, error)

func (f metadataTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func offlineMetadataBuilder() *Builder {
	b := New()
	b.Resolver.OCI.HTTP = &http.Client{Transport: metadataTestTransport(func(r *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("unexpected registry request to %s", r.URL.Host)
	})}
	return b
}

func TestBaseMetadataUsesLoadedConfigAndDockerfileTarget(t *testing.T) {
	var images []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1.47/images/") {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.Error(w, "unexpected", 500)
			return
		}
		image := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1.47/images/"), "/json")
		images = append(images, image)
		value, _ := json.Marshal([]metadata.Entry{{ID: image, RemoteUser: "sample"}})
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": "sha256:sample", "Config": map[string]any{"User": "root", "Labels": map[string]string{metadata.Label: string(value)}}})
	}))
	defer srv.Close()
	cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost("tcp://"+strings.TrimPrefix(srv.URL, "http://")), dockerclient.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "Dockerfile")
	writeFile(t, path, "ARG BASE=alpine:3.20\nFROM ${BASE} AS parent\nUSER root\nFROM parent AS selected\nUSER root\nFROM busybox:1.36 AS unused\n")
	for _, tc := range []struct {
		name string
		dc   *spec.DevContainer
		args map[string]string
		want string
	}{
		{"image", &spec.DevContainer{Image: "alpine:3.20"}, nil, "alpine:3.20"},
		{"target", &spec.DevContainer{Path: filepath.Join(dir, ".devcontainer.json"), Build: &spec.Build{Dockerfile: "Dockerfile", Context: ".", Target: "selected"}}, nil, "alpine:3.20"},
		{"arg", &spec.DevContainer{Path: filepath.Join(dir, ".devcontainer.json"), Build: &spec.Build{Dockerfile: "Dockerfile", Context: ".", Target: "selected", Args: map[string]string{"BASE": "ignored:tag"}}}, map[string]string{"BASE": "busybox:1.36"}, "busybox:1.36"},
		{"default-stage", &spec.DevContainer{Path: filepath.Join(dir, ".devcontainer.json"), Dockerfile: "Dockerfile"}, nil, "busybox:1.36"},
		{"scratch", &spec.DevContainer{Image: "scratch"}, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			images = nil
			entries, err := offlineMetadataBuilder().BaseMetadata(context.Background(), tc.dc, Options{Docker: cli, BuildArgs: tc.args})
			if err != nil {
				t.Fatal(err)
			}
			if tc.want == "" {
				if len(entries) != 0 || len(images) != 0 {
					t.Fatalf("scratch=%v images=%v", entries, images)
				}
				return
			}
			if len(entries) != 1 || entries[0].ID != tc.want || entries[0].RemoteUser != "sample" || len(images) != 1 || images[0] != tc.want {
				t.Fatalf("entries=%+v images=%v", entries, images)
			}
		})
	}
}

func TestBaseMetadataRegistryFallbackAndPlatform(t *testing.T) {
	paths := map[string][]byte{}
	var descriptors []map[string]any
	for _, arch := range []string{"amd64", "arm64"} {
		value, _ := metadata.Encode([]metadata.Entry{{RemoteUser: "sample-" + arch}})
		cfg, _ := json.Marshal(map[string]any{"architecture": arch, "os": "linux", "config": map[string]any{"Labels": map[string]string{metadata.Label: value}}})
		paths["/v2/sample/blobs/"+sha(cfg)] = cfg
		manifest, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.manifest.v1+json", "config": map[string]any{"mediaType": "application/vnd.oci.image.config.v1+json", "digest": sha(cfg), "size": len(cfg)}, "layers": []any{}})
		paths["/v2/sample/manifests/"+sha(manifest)] = manifest
		descriptors = append(descriptors, map[string]any{"mediaType": "application/vnd.oci.image.manifest.v1+json", "digest": sha(manifest), "size": len(manifest), "platform": map[string]string{"os": "linux", "architecture": arch}})
	}
	index, _ := json.Marshal(map[string]any{"schemaVersion": 2, "mediaType": "application/vnd.oci.image.index.v1+json", "manifests": descriptors})
	paths["/v2/sample/manifests/latest"] = index
	registry := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := paths[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/latest") {
			w.Header().Set("Content-Type", "application/vnd.oci.image.index.v1+json")
		}
		_, _ = w.Write(body)
	}))
	defer registry.Close()
	for _, mode := range []string{"registry-only", "missing-local", "wrong-local-platform", "inspect-error", "disconnected-engine"} {
		t.Run(mode, func(t *testing.T) {
			b := New()
			b.Resolver.OCI.HTTP = registry.Client()
			opts := Options{Platforms: []string{"linux/arm64"}}
			if mode == "disconnected-engine" {
				cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost("unix://"+filepath.Join(t.TempDir(), "missing.sock")), dockerclient.WithVersion("1.47"))
				if err != nil {
					t.Fatal(err)
				}
				defer cli.Close()
				opts.Docker = cli
			} else if mode != "registry-only" {
				daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if mode == "missing-local" {
						http.Error(w, `{"message":"not found"}`, http.StatusNotFound)
						return
					}
					if mode == "inspect-error" {
						http.Error(w, `{"message":"unavailable"}`, http.StatusInternalServerError)
						return
					}
					_, _ = w.Write([]byte(`{"Id":"sha256:sample","Os":"linux","Architecture":"amd64","Config":{"Labels":{"devcontainer.metadata":"[{\"remoteUser\":\"wrong\"}]"}}}`))
				}))
				defer daemon.Close()
				cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost("tcp://"+strings.TrimPrefix(daemon.URL, "http://")), dockerclient.WithVersion("1.47"))
				if err != nil {
					t.Fatal(err)
				}
				defer cli.Close()
				opts.Docker = cli
			}
			entries, err := b.BaseMetadata(context.Background(), &spec.DevContainer{Image: strings.TrimPrefix(registry.URL, "https://") + "/sample:latest"}, opts)
			if mode == "inspect-error" {
				if err == nil {
					t.Fatal("inspect error lost")
				}
				return
			}
			if err != nil || len(entries) != 1 || entries[0].RemoteUser != "sample-arm64" {
				t.Fatalf("entries=%+v err=%v", entries, err)
			}
		})
	}
}

func TestBaseMetadataRejectsInvalidDockerfile(t *testing.T) {
	for _, tc := range []struct{ name, source, target, want string }{
		{"parser", "FROM alpine\nRUN <<EOF\n", "", "unterminated heredoc"},
		{"parse", "not an instruction\n", "", "unknown instruction"}, {"no-stages", "ARG BASE=alpine\n", "", "has no stages"},
		{"missing-target", "FROM scratch AS sample\n", "other", "has no stage"}, {"cycle", "FROM sample AS sample\n", "", "depends on itself"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "Dockerfile"), tc.source)
			_, err := offlineMetadataBuilder().BaseMetadata(context.Background(), &spec.DevContainer{Path: filepath.Join(dir, ".devcontainer.json"), Build: &spec.Build{Dockerfile: "Dockerfile", Target: tc.target}}, Options{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid Dockerfile error=%v want %s", err, tc.want)
			}
		})
	}
	if _, err := offlineMetadataBuilder().BaseMetadata(context.Background(), nil, Options{}); err == nil {
		t.Fatal("nil config accepted")
	}
}

func metadataEngine(t *testing.T, entries []metadata.Entry) *dockerclient.Client {
	t.Helper()
	value, err := metadata.Encode(entries)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/images/") || !strings.HasSuffix(r.URL.Path, "/json") {
			t.Errorf("unexpected image configuration request %s", r.URL.Path)
			http.Error(w, "unexpected", 500)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"Id": "sha256:sample", "Config": map[string]any{"User": "root", "Labels": map[string]string{metadata.Label: value}}})
	}))
	t.Cleanup(srv.Close)
	cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost("tcp://"+strings.TrimPrefix(srv.URL, "http://")), dockerclient.WithVersion("1.47"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cli.Close() })
	return cli
}

func TestGeneratedDockerfilesRetainBaseMetadata(t *testing.T) {
	for _, entrypoint := range []string{"Generate", "prepareBuildContext", "Generate-local-offline", "prepareBuildContext-local-offline"} {
		t.Run(entrypoint, func(t *testing.T) {
			base := []metadata.Entry{{ID: "base", RemoteUser: "root", CapAdd: []string{"SYS_PTRACE"}, PostCreate: spec.Command{Shell: "echo base"}}}
			dc := &spec.DevContainer{Image: "alpine:3.20", PostCreate: spec.Command{Shell: "echo config"}}
			opts := Options{Spec: dc, WorkDir: t.TempDir(), Docker: metadataEngine(t, base), SkipImageMetaResolve: strings.HasSuffix(entrypoint, "-local-offline")}
			var path string
			if strings.HasPrefix(entrypoint, "Generate") {
				res, err := offlineMetadataBuilder().Generate(context.Background(), opts)
				if err != nil {
					t.Fatal(err)
				}
				path = res.FinalDockerfile
			} else {
				_, _, _, _, _, _, _, resultPath, _, err := offlineMetadataBuilder().prepareBuildContext(context.Background(), opts, func(string, ...any) {})
				if err != nil {
					t.Fatal(err)
				}
				path = resultPath
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			label, err := metadata.DockerfileLabel(metadata.Collect(base, nil, dc))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), label) {
				t.Fatalf("generated label dropped inherited metadata: %s", body)
			}
		})
	}
}

func TestGeneratorCallersPropagateMetadataErrors(t *testing.T) {
	for _, generate := range []bool{true, false} {
		t.Run(fmt.Sprint(generate), func(t *testing.T) {
			cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost("tcp://127.0.0.1:1"), dockerclient.WithVersion("1.47"), dockerclient.WithHTTPClient(&http.Client{Transport: metadataTestTransport(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 500, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"message":"sample unavailable"}`))}, nil
			})}))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			opts := Options{Spec: &spec.DevContainer{Image: "alpine:3.20"}, WorkDir: t.TempDir(), Docker: cli}
			if generate {
				_, err = offlineMetadataBuilder().Generate(context.Background(), opts)
			} else {
				_, _, _, _, _, _, _, _, _, err = offlineMetadataBuilder().prepareBuildContext(context.Background(), opts, func(string, ...any) {})
			}
			if err == nil || !strings.Contains(err.Error(), "sample unavailable") {
				t.Fatalf("metadata lookup error lost: %v", err)
			}
		})
	}
}

func TestGeneratorDefaultsToLocalImageMetadata(t *testing.T) {
	base := []metadata.Entry{{ID: "base", PostCreate: spec.Command{Shell: "echo base"}}}
	cli := metadataEngine(t, base)
	t.Setenv("DOCKER_HOST", cli.DaemonHost())
	t.Setenv("DOCKER_API_VERSION", "1.47")
	for _, generate := range []bool{true, false} {
		t.Run(fmt.Sprint(generate), func(t *testing.T) {
			dc := &spec.DevContainer{Image: "alpine:3.20", PostCreate: spec.Command{Shell: "echo config"}}
			opts := Options{Spec: dc, WorkDir: t.TempDir()}
			var path string
			if generate {
				res, err := offlineMetadataBuilder().Generate(context.Background(), opts)
				if err != nil {
					t.Fatal(err)
				}
				path = res.FinalDockerfile
			} else {
				_, _, _, _, _, _, _, resultPath, _, err := offlineMetadataBuilder().prepareBuildContext(context.Background(), opts, func(string, ...any) {})
				if err != nil {
					t.Fatal(err)
				}
				path = resultPath
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := metadata.DockerfileLabel(metadata.Collect(base, nil, dc))
			if !strings.Contains(string(body), want) {
				t.Fatalf("default client dropped local metadata: %s", body)
			}
		})
	}
}
