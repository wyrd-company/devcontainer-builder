// relationships:
//
//	references: [outdated, ../features/outdated]
package cli

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOutdatedCommand(t *testing.T) {
	t.Setenv("DOCKER_CONFIG", t.TempDir())
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/json" {
			t.Error("wrong Accept")
		}
		_, _ = w.Write([]byte(`{"tags":["latest","2.0.0","1.1.0","1.0.0"]}`))
	}))
	defer server.Close()
	old := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = old }()
	root := t.TempDir()
	id := strings.TrimPrefix(server.URL, "https://") + "/collection/sample:1"
	config := filepath.Join(root, ".devcontainer.json")
	_ = os.WriteFile(config, []byte(`{"features":{"`+id+`":false},"name":"${localEnv:UNSET:sample}"}`), 0600)
	for _, format := range []string{"json", "text"} {
		var out, errout bytes.Buffer
		args := []string{"outdated", "--workspace-folder", root, "--output-format", format, "--user-data-folder", t.TempDir(), "--terminal-columns", "80", "--terminal-rows", "24", "--log-level", "trace", "--log-format", "json"}
		code := Run(context.Background(), args, "test", nil, &out, &errout)
		if code != 0 {
			t.Fatalf("%d %s %s", code, &out, &errout)
		}
		if format == "json" {
			want := `{"features":{"` + id + `":{"current":"1.1.0","wanted":"1.1.0","wantedMajor":"1","latest":"2.0.0","latestMajor":"2"}}}` + "\n"
			if out.String() != want {
				t.Fatalf("%s", &out)
			}
		} else if !strings.HasPrefix(out.String(), "Feature") || !strings.Contains(out.String(), "1.1.0") || strings.Contains(out.String(), ":1") {
			t.Fatalf("%s", &out)
		}
	}
	for _, args := range [][]string{{"outdated", "--output-format", "yaml"}, {"outdated", "--log-level", "off"}, {"outdated", "--terminal-rows", "1"}, {"outdated", "unexpected"}} {
		var out, errout bytes.Buffer
		if code := Run(context.Background(), args, "test", nil, &out, &errout); code != 2 {
			t.Errorf("%v: %d %s %s", args, code, &out, &errout)
		}
	}
	_ = os.Remove(config)
	var out, errout bytes.Buffer
	if code := Run(context.Background(), []string{"outdated", "--workspace-folder", root}, "test", nil, &out, &errout); code != 1 || out.Len() != 0 || errout.Len() == 0 {
		t.Fatalf("missing config: %d %s %s", code, &out, &errout)
	}
}
