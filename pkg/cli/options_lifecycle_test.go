// relationships:
//
//	references: [options_lifecycle, ../host/dockercompose/existing]
package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestComposeExpectExistingBeforeBuildAndRemoval(t *testing.T) {
	for _, engineError := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "engine-error"}[engineError], func(t *testing.T) {
			workspace := t.TempDir()
			if err := os.WriteFile(filepath.Join(workspace, ".devcontainer.json"), []byte(`{"dockerComposeFile":"compose.yml","service":"app"}`), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(workspace, "compose.yml"), []byte("name: sample-project\nservices:\n  app:\n    build: .\n"), 0600); err != nil {
				t.Fatal(err)
			}
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if !strings.HasSuffix(r.URL.Path, "/containers/json") {
					t.Errorf("build/removal before expectation check: %s %s", r.Method, r.URL.Path)
				}
				var filter map[string]map[string]bool
				if err := json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filter); err != nil {
					t.Error(err)
				}
				if !filter["label"]["com.docker.compose.project=sample-project"] || !filter["label"]["com.docker.compose.service=app"] || r.URL.Query().Get("all") != "1" {
					t.Errorf("lookup=%s", r.URL.RawQuery)
				}
				w.Header().Set("Content-Type", "application/json")
				if engineError {
					http.Error(w, `{"message":"lookup unavailable"}`, 500)
				} else {
					_, _ = w.Write([]byte(`[]`))
				}
			}))
			defer server.Close()
			t.Setenv("DOCKER_HOST", "tcp://"+server.Listener.Addr().String())
			t.Setenv("DOCKER_API_VERSION", "1.47")
			result, _ := invoke(t, 1, "up", "--workspace-folder", workspace, "--expect-existing-container", "--remove-existing-container")
			want := "The expected container does not exist."
			if engineError {
				want = "lookup unavailable"
			}
			if !strings.Contains(result["message"].(string), want) || requests != 1 {
				t.Fatalf("result=%v requests=%d", result, requests)
			}
		})
	}
}

func TestLifecycleOptionRegistration(t *testing.T) {
	root := (&application{}).command()
	for _, name := range []string{"up", "set-up", "run-user-commands"} {
		cmd, _, err := root.Find([]string{name})
		if err != nil {
			t.Fatal(err)
		}
		for flag, supported := range map[string]bool{
			"skip-non-blocking-commands": true,
			"prebuild":                   name != "set-up",
			"stop-for-personalization":   name == "run-user-commands",
			"expect-existing-container":  name == "up",
		} {
			f := cmd.Flags().Lookup(flag)
			if (f != nil) != supported {
				t.Fatalf("%s --%s registration=%v", name, flag, f)
			}
			if f != nil && (f.Value.Type() != "bool" || f.DefValue != "false" || f.Hidden) {
				t.Fatalf("%s --%s type/default/visibility=%+v", name, flag, f)
			}
		}
	}
}
