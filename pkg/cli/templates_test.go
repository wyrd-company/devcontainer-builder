// relationships:
//
//	references: [templates]
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTemplatesMetadata(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		annotation := `{"id":"sample","unknown":true}`
		if strings.Contains(r.URL.Path, "/missing/") {
			annotation = ""
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"schemaVersion": 2, "config": map[string]string{"mediaType": "application/vnd.devcontainers"}, "annotations": map[string]string{"dev.containers.metadata": annotation}})
	}))
	defer server.Close()
	old := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = old }()
	for _, tt := range []struct {
		args   []string
		code   int
		output string
	}{
		{[]string{"templates", "metadata", strings.TrimPrefix(server.URL, "https://") + "/collection/sample"}, 0, "{\"id\":\"sample\",\"unknown\":true}\n"},
		{[]string{"templates", "metadata", strings.TrimPrefix(server.URL, "https://") + "/collection/missing"}, 1, "{}\n"},
		{[]string{"templates", "metadata", "invalid"}, 1, "{}\n"},
		{[]string{"templates", "metadata"}, 2, ""},
		{[]string{"templates", "metadata", "a", "b"}, 2, ""},
	} {
		var out, errout bytes.Buffer
		code := Run(context.Background(), tt.args, "test", strings.NewReader(""), &out, &errout)
		if code != tt.code || (tt.output != "" && out.String() != tt.output) {
			t.Errorf("%v: %d %s stderr %s", tt.args, code, out.String(), errout.String())
		}
	}
}

func TestTemplatesMetadataReviewRegressions(t *testing.T) {
	config := t.TempDir()
	t.Setenv("DOCKER_CONFIG", config)
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/v2/collection/auth403/manifests/latest" && r.Header.Get("Authorization") != "Basic dXNlcjpwYXNz" {
			w.Header().Set("WWW-Authenticate", `Basic realm="sample"`)
			w.WriteHeader(403)
			return
		}
		if r.Header.Get("Accept") != "application/vnd.oci.image.manifest.v1+json" {
			t.Error("advertised unsupported media type")
		}
		w.Header().Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
		if strings.Contains(r.URL.Path, "nullmanifest") {
			_, _ = w.Write([]byte(`null`))
			return
		}
		if strings.Contains(r.URL.Path, "noconfig") {
			_, _ = w.Write([]byte(`{"schemaVersion":2}`))
			return
		}
		if strings.Contains(r.URL.Path, "nullconfig") {
			_, _ = w.Write([]byte(`{"schemaVersion":2,"config":null}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"config": map[string]string{"mediaType": "application/vnd.devcontainers"}, "annotations": map[string]string{"dev.containers.metadata": `{"id":"first","id":"last","number":9007199254740993,"overflow":1e400}`}})
	}))
	defer server.Close()
	host := strings.TrimPrefix(server.URL, "https://")
	data, _ := json.Marshal(map[string]any{"auths": map[string]any{host: map[string]string{"auth": "dXNlcjpwYXNz"}}})
	if err := os.WriteFile(filepath.Join(config, "config.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	old := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = old }()
	for _, tt := range []struct {
		path        string
		code, count int
		output      string
	}{
		{"collection/auth403", 0, 2, "{\"id\":\"last\",\"number\":9007199254740992,\"overflow\":null}\n"},
		{"collection/nullmanifest", 1, 1, "{}\n"},
		{"collection/noconfig", 1, 1, ""}, {"collection/nullconfig", 1, 1, ""},
		{"sample", 1, 0, "{}\n"}, {"sample:stable", 1, 0, "{}\n"},
	} {
		requests = 0
		var out, diagnostics bytes.Buffer
		code := Run(context.Background(), []string{"templates", "metadata", host + "/" + tt.path}, "test", strings.NewReader(""), &out, &diagnostics)
		if code != tt.code || out.String() != tt.output || requests != tt.count {
			t.Errorf("%s: exit%d output%q requests%d; stderr %s", tt.path, code, out.String(), requests, diagnostics.String())
		}
	}
}
