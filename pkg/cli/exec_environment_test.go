// relationships:
//
// references: [exec, ../host/runner/env]
package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
)

// Capture the request at the Docker boundary so this tests the exec handler,
// including flag parsing and configuration substitution, without a daemon.
func TestExecRemoteEnvPrecedence(t *testing.T) {
	workspace := t.TempDir()
	config := `{"image":"alpine:3.20","remoteEnv":{"SAMPLE":"configured","EMPTY":"","CONFIG_ONLY":"${localEnv:SAMPLE_VALUE}"}}`
	if err := os.WriteFile(filepath.Join(workspace, ".devcontainer.json"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SAMPLE_VALUE", "from-host")
	var got *container.ExecOptions
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/sample/json"):
			_, _ = w.Write([]byte(`{"Id":"sample","Config":{"User":"root","Labels":{}},"State":{"Running":true}}`))
		case strings.HasSuffix(r.URL.Path, "/containers/sample/exec"):
			var request container.ExecOptions
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			got = &request
			http.Error(w, "sample captured exec", http.StatusInternalServerError)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.Error(w, "unexpected request", http.StatusInternalServerError)
		}
	}))
	defer server.Close()
	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(server.URL, "http://"))
	t.Setenv("DOCKER_API_VERSION", "1.47")
	t.Setenv("DOCKER_TLS_VERIFY", "")
	t.Setenv("DOCKER_CERT_PATH", "")
	code, _, stderr := runCLI("exec", "--workspace-folder", workspace, "--container-id", "sample", "--default-user-env-probe", "none", "--remote-workspace-folder", "/", "--remote-env", "SAMPLE=cli", "--remote-env", "EMPTY=cli", "--remote-env", "CLI_ONLY=extra", "--", "true")
	if code != 1 || got == nil || !strings.Contains(stderr, "sample captured exec") {
		t.Fatalf("exec did not reach capture: code=%d request=%+v stderr=%s", code, got, stderr)
	}
	env := map[string]string{}
	for _, pair := range got.Env {
		key, value, _ := strings.Cut(pair, "=")
		env[key] = value
	}
	want := map[string]string{"SAMPLE": "configured", "EMPTY": "", "CONFIG_ONLY": "from-host", "CLI_ONLY": "extra"}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("exec environment = %v, want %v", env, want)
	}
}
