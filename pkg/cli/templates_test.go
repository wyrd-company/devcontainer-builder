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
