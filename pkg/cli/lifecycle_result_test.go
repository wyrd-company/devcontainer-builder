// relationships:
//
//	references: [lifecycle, ../host/runner/controls]
package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
)

// Use the real CLI, Docker transport, and shared runner. Only the Engine is a
// fixture: marker checks and command exits travel through its exec endpoints.
func TestRunUserCommandsResult(t *testing.T) {
	for _, tc := range []struct {
		name, waitFor, want string
		flags               []string
		phases              int
	}{
		{name: "done", want: "done", phases: 5},
		{name: "wait-without-skip", waitFor: "initializeCommand", want: "done", phases: 5},
		{name: "skip-default", want: "skipNonBlocking", flags: []string{"--skip-non-blocking-commands"}, phases: 2},
		{name: "skip-initialize", waitFor: "initializeCommand", want: "skipNonBlocking", flags: []string{"--skip-non-blocking-commands"}},
		{name: "skip-create", waitFor: "onCreateCommand", want: "skipNonBlocking", flags: []string{"--skip-non-blocking-commands"}, phases: 1},
		{name: "skip-update-before-prebuild", waitFor: "updateContentCommand", want: "skipNonBlocking", flags: []string{"--skip-non-blocking-commands", "--prebuild", "--stop-for-personalization"}, phases: 2},
		{name: "skip-post-create-before-personalization", waitFor: "postCreateCommand", want: "skipNonBlocking", flags: []string{"--skip-non-blocking-commands", "--stop-for-personalization"}, phases: 3},
		{name: "skip-start", waitFor: "postStartCommand", want: "skipNonBlocking", flags: []string{"--skip-non-blocking-commands"}, phases: 4},
		{name: "prebuild", want: "prebuild", flags: []string{"--prebuild"}, phases: 2},
		{name: "prebuild-before-later-skip", waitFor: "postCreateCommand", want: "prebuild", flags: []string{"--prebuild", "--skip-non-blocking-commands"}, phases: 2},
		{name: "prebuild-before-personalization", want: "prebuild", flags: []string{"--prebuild", "--stop-for-personalization"}, phases: 2},
		{name: "personalization", want: "stopForPersonalization", flags: []string{"--stop-for-personalization"}, phases: 3},
		{name: "personalization-before-later-skip", waitFor: "postStartCommand", want: "stopForPersonalization", flags: []string{"--stop-for-personalization", "--skip-non-blocking-commands"}, phases: 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, shape := range []string{"commands", "empty", "boundary-empty", "current-markers"} {
				t.Run(shape, func(t *testing.T) {
					config := map[string]any{"image": "alpine:3.20", "waitFor": tc.waitFor}
					hooks := []string{"onCreateCommand", "updateContentCommand", "postCreateCommand", "postStartCommand", "postAttachCommand"}
					wantPhases := tc.phases
					if shape == "empty" {
						wantPhases = 0
					} else {
						for i, hook := range hooks {
							if shape == "boundary-empty" && i == tc.phases-1 {
								wantPhases--
								continue
							}
							config[hook] = "true"
						}
					}
					flags := append([]string{}, tc.flags...)
					if shape == "current-markers" {
						flags = append(flags, "--skip-post-attach")
						wantPhases = 0
						// Prebuild deliberately reruns updateContent despite its marker, unless
						// a skip boundary before updateContent prevents reaching it.
						for _, flag := range flags {
							if flag == "--prebuild" && tc.phases >= 2 {
								wantPhases = 1
							}
						}
					}
					workspace := writeLifecycleResultConfig(t, config)
					lifecycleResultEngine(t, shape == "current-markers", false)
					result, _ := invoke(t, 0, append([]string{"run-user-commands", "--container-id", "sample-container", "--workspace-folder", workspace}, flags...)...)
					if result["result"] != tc.want {
						t.Fatalf("result=%v want %s", result, tc.want)
					}
					if got := len(result["phasesRun"].([]any)); got != wantPhases {
						t.Fatalf("phasesRun=%v want %d", result["phasesRun"], wantPhases)
					}
					if result["containerId"] != "sample-container" || result["remoteUser"] != "root" {
						t.Fatalf("existing fields lost: %v", result)
					}
				})
			}
		})
	}
}

func TestLifecycleResultFailureAndSetUp(t *testing.T) {
	for _, command := range []string{"run-user-commands", "set-up"} {
		for _, fail := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/fail-%v", command, fail), func(t *testing.T) {
				workspace := writeLifecycleResultConfig(t, map[string]any{"image": "alpine:3.20", "onCreateCommand": "exit 23"})
				lifecycleResultEngine(t, false, fail)
				code := 0
				if fail {
					code = 1
				}
				result, _ := invoke(t, code, command, "--workspace-folder", workspace, "--container-id", "sample-container")
				_, hasResult := result["result"]
				if hasResult != (!fail && command == "run-user-commands") {
					t.Fatalf("unexpected result field: %v", result)
				}
				if fail && !strings.Contains(result["message"].(string), "exit 23") {
					t.Fatalf("lost command failure: %v", result)
				}
			})
		}
	}
}

func writeLifecycleResultConfig(t *testing.T, config map[string]any) string {
	t.Helper()
	workspace := t.TempDir()
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".devcontainer.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	return workspace
}

func lifecycleResultEngine(t *testing.T, currentMarkers, fail bool) {
	t.Helper()
	exitCode := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/sample-container/json"):
			_, _ = w.Write([]byte(`{"Id":"sample-container","Created":"created","State":{"StartedAt":"started"},"Config":{"User":"root","Labels":{}}}`))
		case strings.HasSuffix(r.URL.Path, "/containers/sample-container/exec"):
			var request container.ExecOptions
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			script := strings.Join(request.Cmd, " ")
			exitCode = 0
			if currentMarkers && strings.Contains(script, "ENOENT") {
				exitCode = 1
			}
			if fail && script == "/bin/sh -c exit 23" {
				exitCode = 23
			}
			_, _ = w.Write([]byte(`{"Id":"sample-exec"}`))
		case strings.HasSuffix(r.URL.Path, "/exec/sample-exec/start"):
			// An empty multiplexed stream suffices for the environment probe and
			// commands; root home detection does not need an exec.
			_, _ = io.Copy(io.Discard, r.Body)
			conn, buf, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			_, _ = buf.WriteString("HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
			_ = buf.Flush()
		case strings.HasSuffix(r.URL.Path, "/exec/sample-exec/json"):
			_ = json.NewEncoder(w).Encode(map[string]any{"ExitCode": exitCode})
		default:
			t.Errorf("unexpected Engine request %s %s", r.Method, r.URL.Path)
			http.Error(w, `{"message":"unexpected request"}`, 500)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("DOCKER_HOST", "tcp://"+server.Listener.Addr().String())
	t.Setenv("DOCKER_API_VERSION", "1.47")
}
