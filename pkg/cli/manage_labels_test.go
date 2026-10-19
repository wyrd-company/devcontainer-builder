// relationships:
//
//	references: [host, options_mounts, ../host/docker/spawn]
package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestManageIDLabelValidation(t *testing.T) {
	for _, command := range []string{"down", "inspect"} {
		for _, label := range []string{"", "missing", "name=", "=value"} {
			t.Run(command+"/"+label, func(t *testing.T) {
				result, _ := invoke(t, 2, command, "--id-label", label)
				if result["message"] != "Unmatched argument format: id-label must match <name>=<value>" {
					t.Fatalf("result=%v", result)
				}
			})
		}
	}
}

// Exercise the command handlers through the real Engine client. The fake
// Engine checks the entire selector and records the exact removal target.
func TestManageIDLabelSelection(t *testing.T) {
	for _, command := range []string{"down", "inspect"} {
		for _, configured := range []bool{false, true} {
			t.Run(command+"/configured="+map[bool]string{false: "false", true: "true"}[configured], func(t *testing.T) {
				workspace := t.TempDir()
				if configured {
					writeTestFile(t, filepath.Join(workspace, ".devcontainer.json"), `{"image":"alpine:3.20"}`)
				}
				var lists, removes, inspections int
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					switch {
					case strings.HasSuffix(r.URL.Path, "/containers/json"):
						lists++
						var filter map[string]map[string]bool
						if err := json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filter); err != nil {
							t.Error(err)
						}
						want := map[string]map[string]bool{"label": {"example.test/selection=sample,one": true, "example.test/role=primary=one": true}}
						if !reflect.DeepEqual(filter, want) {
							t.Errorf("selector=%v, want %v", filter, want)
						}
						_, _ = w.Write([]byte(`[{"Id":"selected","Labels":{"example.test/selection":"sample,one","example.test/role":"primary=one"}}]`))
					case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/containers/selected/stop"):
						w.WriteHeader(http.StatusNoContent)
					case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/containers/selected"):
						removes++
						w.WriteHeader(http.StatusNoContent)
					case strings.HasSuffix(r.URL.Path, "/containers/selected/json"):
						inspections++
						_, _ = w.Write([]byte(`{"Id":"selected","Config":{"Labels":{"example.test/selection":"sample,one","example.test/role":"primary=one"}}}`))
					default:
						t.Errorf("unexpected Engine call: %s %s", r.Method, r.URL.Path)
						http.Error(w, `{}`, 500)
					}
				}))
				defer srv.Close()
				t.Setenv("DOCKER_HOST", "tcp://"+srv.Listener.Addr().String())
				t.Setenv("DOCKER_API_VERSION", "1.47")
				result, _ := invoke(t, 0, command, "--workspace-folder", workspace, "--id-label", "example.test/selection=sample,one", "--id-label", "example.test/role=primary=one")
				if lists == 0 {
					t.Fatal("container selection was not reached")
				}
				if command == "down" && removes != 1 {
					t.Fatalf("removes=%d", removes)
				}
				if command == "inspect" && (removes != 0 || inspections == 0 || result["containerId"] != "selected") {
					t.Fatalf("result=%v removes=%d inspections=%d", result, removes, inspections)
				}
			})
		}
	}
}
