// relationships:
//
//	references: [options_lockfile, features_configuration, ../features/lockfile]
package cli

import (
	"archive/tar"
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wyrd-company/devcontainer-builder/pkg/features"
)

func TestLockfileOptionRegistration(t *testing.T) {
	root := (&application{}).command()
	for _, name := range []string{"build", "up", "generate", "read-configuration", "set-up", "features"} {
		cmd, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatal(err)
		}
		for _, flag := range []string{"experimental-lockfile", "experimental-frozen-lockfile"} {
			f := cmd.Flags().Lookup(flag)
			want := name == "build" || name == "up"
			if (f != nil) != want {
				t.Fatalf("%s --%s present=%v want=%v", name, flag, f != nil, want)
			}
			if want && (f.Value.Type() != "bool" || f.DefValue != "false" || !f.Hidden) {
				t.Fatalf("flag=%+v", f)
			}
		}
	}
}

func lockfileFixture(t *testing.T) string {
	t.Helper()
	workspace := t.TempDir()
	feature := filepath.Join(workspace, "sample")
	if err := os.Mkdir(feature, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		".devcontainer.json":               `{"image":"alpine:3.20","features":{"./sample":{}},"updateRemoteUserUID":false}`,
		"sample/devcontainer-feature.json": `{"id":"sample","version":"1.0.0"}`,
		"sample/install.sh":                "#!/bin/sh\nset -e\nprintf ready > /tmp/sample-feature\n",
	} {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return workspace
}

func TestLockfileReadConfigurationUpdatesAfterSuccess(t *testing.T) {
	workspace := lockfileFixture(t)
	path := features.LockfilePath(filepath.Join(workspace, ".devcontainer.json"))
	old := `{"features":{"registry.example/collection/stale:1":{"version":"1.0.0","resolved":"registry.example/collection/stale@sha256:unused","integrity":"sha256:unused"}}}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("[]")) // No existing container.
	}))
	defer server.Close()
	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(server.URL, "http://"))
	t.Setenv("DOCKER_API_VERSION", "1.47")
	result, _ := invoke(t, 0, "read-configuration", "--workspace-folder", workspace, "--include-features-configuration")
	configuration := result["featuresConfiguration"].(map[string]any)
	defer os.RemoveAll(configuration["dstFolder"].(string))
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "{\n  \"features\": {}\n}\n" {
		t.Fatalf("lockfile=%s err=%v", data, err)
	}
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "sample", "devcontainer-feature.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	invoke(t, 1, "read-configuration", "--workspace-folder", workspace, "--include-features-configuration")
	if data, _ := os.ReadFile(path); string(data) != old {
		t.Fatal("failed command changed lockfile")
	}
}

func TestLockfileFrozenHandlerBeforeBuild(t *testing.T) {
	for _, command := range []string{"build", "up"} {
		t.Run(command, func(t *testing.T) {
			workspace := lockfileFixture(t)
			// Client creation needs no daemon; frozen validation precedes building.
			t.Setenv("DOCKER_HOST", "unix://"+filepath.Join(t.TempDir(), "absent.sock"))
			result, _ := invoke(t, 1, command, "--experimental-frozen-lockfile", "--workspace-folder", workspace)
			if result["message"] != "Lockfile does not exist." {
				t.Fatalf("result=%v", result)
			}
			path := features.LockfilePath(filepath.Join(workspace, ".devcontainer.json"))
			if err := os.WriteFile(path, []byte(`{"features":{"extra":{}}}`), 0o644); err != nil {
				t.Fatal(err)
			}
			result, _ = invoke(t, 1, command, "--experimental-frozen-lockfile", "--experimental-lockfile", "--workspace-folder", workspace)
			if result["message"] != "Lockfile does not match." {
				t.Fatalf("result=%v", result)
			}
			if data, _ := os.ReadFile(path); !strings.Contains(string(data), "extra") {
				t.Fatal("frozen mode wrote")
			}
		})
	}
}

func TestDirectTarballConfigurationSource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "devcontainer-feature.json"), []byte(`{"id":"sample","version":"1.0.0"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	f := features.Resolved{Source: "https://registry.example/devcontainer-feature-sample.tgz", Tarball: "https://registry.example/devcontainer-feature-sample.tgz", Digest: "sha256:sample", Metadata: features.Metadata{ID: "sample"}, Dir: dir, Value: true}
	set, err := resolvedFeatureSet(f, 0, dir)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(set)
	source := set["sourceInformation"].(map[string]any)
	if source["type"] != "direct-tarball" || source["tarballUri"] != f.Source || set["computedDigest"] != f.Digest {
		t.Fatalf("set=%s", data)
	}
}

func TestReadConfigurationUsesLockfileDigest(t *testing.T) {
	var archive bytes.Buffer
	tw := tar.NewWriter(&archive)
	metadata := []byte(`{"id":"sample","version":"1.0.0"}`)
	if err := tw.WriteHeader(&tar.Header{Name: "devcontainer-feature.json", Mode: 0o644, Size: int64(len(metadata))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(metadata); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	layerDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(archive.Bytes()))
	manifest := fmt.Sprintf(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","layers":[{"digest":%q,"size":%d}]}`, layerDigest, archive.Len())
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(manifest)))
	registry := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v2/collection/sample/manifests/" + digest:
			w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
			_, _ = w.Write([]byte(manifest))
		case "/v2/collection/sample/blobs/" + layerDigest:
			_, _ = w.Write(archive.Bytes())
		default:
			http.NotFound(w, r) // A live-tag request is a regression.
		}
	}))
	defer registry.Close()
	transport := http.DefaultTransport
	http.DefaultTransport = registry.Client().Transport
	defer func() { http.DefaultTransport = transport }()
	daemon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("[]")) }))
	defer daemon.Close()
	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(daemon.URL, "http://"))
	t.Setenv("DOCKER_API_VERSION", "1.47")
	workspace := t.TempDir()
	config := filepath.Join(workspace, ".devcontainer.json")
	resource := strings.TrimPrefix(registry.URL, "https://") + "/collection/sample"
	source := resource + ":1"
	if err := os.WriteFile(config, []byte(fmt.Sprintf(`{"image":"alpine:3.20","features":{%q:{}}}`, source)), 0o644); err != nil {
		t.Fatal(err)
	}
	lock := features.Lockfile{Features: map[string]features.LockedFeature{source: {Version: "1.0.0", Resolved: resource + "@" + digest, Integrity: digest}}}
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(features.LockfilePath(config), data, 0o644); err != nil {
		t.Fatal(err)
	}
	result, _ := invoke(t, 0, "read-configuration", "--workspace-folder", workspace, "--include-features-configuration")
	configuration := result["featuresConfiguration"].(map[string]any)
	defer os.RemoveAll(configuration["dstFolder"].(string))
	set := configuration["featureSets"].([]any)[0].(map[string]any)
	info := set["sourceInformation"].(map[string]any)
	ref := info["featureRef"].(map[string]any)
	if info["manifestDigest"] != digest || set["computedDigest"] != digest || ref["tag"] != "1" || info["userFeatureId"] != source {
		t.Fatalf("set=%v", set)
	}
}
