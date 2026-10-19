// ---
// relationships:
//
//	references: [CONTRIBUTING]
//
// ---
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/docker/docker/api/types/container"
)

// hostileDocker points DOCKER_HOST at an endpoint that answers every API
// call with a 500 that echoes echo. On the "json" channel the echo is the
// error message. On the "text" channel it is a plain-text body, and the
// version and server headers of every response carry it too. It counts the
// calls it answers.
func hostileDocker(t *testing.T, channel, echo string) *atomic.Int32 {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		version := "1.51"
		if channel == "text" {
			version = echo
			w.Header().Set("Server", echo)
			w.Header().Set("Docker-Experimental", echo)
		}
		if strings.HasSuffix(r.URL.Path, "/_ping") {
			w.Header().Set("API-Version", version)
			w.WriteHeader(http.StatusOK)
			return
		}
		calls.Add(1)
		if channel == "text" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(echo))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"message": echo})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(srv.URL, "http://"))
	t.Setenv("DOCKER_TLS_VERIFY", "")
	t.Setenv("DOCKER_CERT_PATH", "")
	return &calls
}

// A Docker endpoint can echo a secret in any spelling into its error text
// and headers. Each spelling is injected on its own, on each channel,
// through both commands and both log formats, and must reach output only as
// the mask.
func TestHostileDaemonEchoIsMasked(t *testing.T) {
	for i, spelling := range leakSpellings(sampleSecret) {
		for _, channel := range []string{"json", "text"} {
			for _, command := range []string{"up", "run-user-commands"} {
				for _, format := range []string{"text", "json"} {
					t.Run(fmt.Sprintf("%s/%s/%s/spelling-%d", channel, command, format, i), func(t *testing.T) {
						calls := hostileDocker(t, channel, "sample-prefix "+spelling+" sample-suffix")
						dir, config, secrets := secretsFixture(t)
						code, stdout, stderr := runCLI(command, "--log-format", format, "--log-level", "trace",
							"--config", config, "--workspace-folder", dir, "--secrets-file", secrets)
						if code != 1 || calls.Load() == 0 {
							t.Fatalf("exit %d after %d daemon calls; the hostile endpoint was not reached\nstdout=%s\nstderr=%s", code, calls.Load(), stdout, stderr)
						}
						var result map[string]any
						if err := json.Unmarshal([]byte(stdout), &result); err != nil {
							t.Fatalf("result %q: %v", stdout, err)
						}
						if message, _ := result["message"].(string); !strings.Contains(message, "sample-prefix "+secretMaskText+" sample-suffix") {
							t.Errorf("message %q lacks the masked daemon echo", message)
						}
						assertNoSecret(t, "stdout", stdout)
						assertNoSecret(t, "stderr", stderr)
					})
				}
			}
		}
	}
}

// Secrets travel in the exec request body. The Docker client must not
// follow a redirect that would send them to another origin.
func TestSecretsOriginBinding(t *testing.T) {
	for _, status := range []int{301, 302, 303, 307, 308} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var targetHits atomic.Int32
			target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				targetHits.Add(1)
				w.WriteHeader(http.StatusUnauthorized)
			}))
			defer target.Close()
			origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/_ping") {
					w.Header().Set("API-Version", "1.51")
					w.WriteHeader(http.StatusOK)
					return
				}
				w.Header().Set("Location", target.URL+"/target")
				w.WriteHeader(status)
			}))
			defer origin.Close()
			t.Setenv("DOCKER_HOST", "tcp://"+strings.TrimPrefix(origin.URL, "http://"))
			t.Setenv("DOCKER_TLS_VERIFY", "")
			t.Setenv("DOCKER_CERT_PATH", "")
			cli, err := newDockerClient()
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			_, err = cli.ContainerExecCreate(context.Background(), "sample", container.ExecOptions{Cmd: []string{"true"}, Env: []string{"SAMPLE_TOKEN=" + sampleSecret}})
			if err == nil {
				t.Fatal("redirect accepted")
			}
			if n := targetHits.Load(); n != 0 {
				t.Fatalf("redirect target received %d secret-bearing requests", n)
			}
			assertNoSecret(t, "redirect error", err.Error())
		})
	}
}
