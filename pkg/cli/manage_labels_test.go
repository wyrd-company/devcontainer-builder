// relationships:
//
//	references: [host, options_mounts, ../host/docker/spawn]
package cli

import (
	"context"
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

type managementCalls struct {
	removed, stopped []string
}

// managementEngine exposes only the requested id-label match and its own
// Compose project. Any attempt to use the current folder as a selector fails.
func managementEngine(t *testing.T, labels map[string]string, conflict bool) *managementCalls {
	t.Helper()
	calls := &managementCalls{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		const project = "selected-project"
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			var filter map[string]map[string]bool
			if err := json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filter); err != nil {
				t.Error(err)
			}
			idFilter := map[string]map[string]bool{"label": {"example.test/selection=managed": true, "example.test/role=primary": true}}
			projectFilter := map[string]map[string]bool{"label": {"com.docker.compose.project=" + project: true}}
			switch {
			case reflect.DeepEqual(filter, idFilter):
				_ = json.NewEncoder(w).Encode([]map[string]any{{"Id": "selected", "Labels": labels}})
			case reflect.DeepEqual(filter, projectFilter):
				sidecar := map[string]string{"com.docker.compose.project": project}
				if conflict {
					sidecar["devcontainer.local_folder"] = "/other-workspace"
				}
				_ = json.NewEncoder(w).Encode([]map[string]any{{"Id": "selected", "Labels": labels}, {"Id": "sidecar", "Labels": sidecar}})
			default:
				t.Errorf("unexpected container selector (must not use cwd): %v", filter)
				_, _ = w.Write([]byte(`[]`))
			}
		case strings.HasSuffix(r.URL.Path, "/containers/selected/json"):
			_ = json.NewEncoder(w).Encode(map[string]any{"Id": "selected", "Config": map[string]any{"Labels": labels}})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/stop"):
			calls.stopped = append(calls.stopped, filepath.Base(filepath.Dir(r.URL.Path)))
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/containers/"):
			calls.removed = append(calls.removed, filepath.Base(r.URL.Path))
			w.WriteHeader(http.StatusNoContent)
		case strings.HasSuffix(r.URL.Path, "/networks"), strings.HasSuffix(r.URL.Path, "/volumes"):
			var filter map[string]map[string]bool
			_ = json.Unmarshal([]byte(r.URL.Query().Get("filters")), &filter)
			if strings.HasSuffix(r.URL.Path, "/volumes") {
				ownership := map[string]map[string]bool{"label": {"devcontainer.compose.objects": true, "devcontainer.local_folder=" + labels["devcontainer.local_folder"]: true}}
				if reflect.DeepEqual(filter, ownership) {
					_, _ = w.Write([]byte(`{"Volumes":[]}`))
					return
				}
			}
			want := map[string]map[string]bool{"label": {"com.docker.compose.project=" + project: true}}
			if !reflect.DeepEqual(filter, want) {
				t.Errorf("resource selector=%v, want %v", filter, want)
			}
			if strings.HasSuffix(r.URL.Path, "/volumes") {
				_, _ = w.Write([]byte(`{"Volumes":[]}`))
			} else {
				_, _ = w.Write([]byte(`[]`))
			}
		default:
			t.Errorf("unexpected Engine call: %s %s", r.Method, r.URL.Path)
			http.Error(w, `{}`, http.StatusInternalServerError)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("DOCKER_HOST", "tcp://"+server.Listener.Addr().String())
	t.Setenv("DOCKER_API_VERSION", "1.47")
	return calls
}

func TestManageIDLabelsIgnoreCWDConfiguration(t *testing.T) {
	for _, command := range []string{"down", "inspect"} {
		for _, config := range []string{`invalid json`, `{"dockerComposeFile":"missing.yml","service":"app"}`, `{"image":"alpine:3.20","workspaceFolder":"/cwd-only"}`} {
			t.Run(command+"/"+config, func(t *testing.T) {
				cwd := t.TempDir()
				writeTestFile(t, filepath.Join(cwd, ".devcontainer.json"), config)
				t.Chdir(cwd)
				removed := managementEngine(t, map[string]string{"example.test/selection": "managed", "example.test/role": "primary"}, false)
				result, _ := invoke(t, 0, command, "--id-label", "example.test/selection=managed", "--id-label", "example.test/role=primary")
				if command == "down" && !reflect.DeepEqual(removed.removed, []string{"selected"}) {
					t.Fatalf("removed=%v", removed.removed)
				}
				if command == "inspect" && (len(removed.removed) != 0 || result["containerId"] != "selected" || result["remoteWorkspaceFolder"] != "") {
					t.Fatalf("result=%v removed=%v", result, removed.removed)
				}
			})
		}
	}
}

func TestManageIDLabelsComposeProjectOwnership(t *testing.T) {
	for _, tc := range []struct {
		name, workspace string
		conflict        bool
		exit            int
	}{
		{"own project from empty cwd", "/selected-workspace", false, 0},
		{"missing workspace ownership", "", false, 1},
		{"foreign project member", "/selected-workspace", true, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Chdir(t.TempDir())
			labels := map[string]string{"example.test/selection": "managed", "example.test/role": "primary", "com.docker.compose.project": "selected-project", "com.docker.compose.service": "app", "devcontainer.local_folder": tc.workspace}
			removed := managementEngine(t, labels, tc.conflict)
			result, _ := invoke(t, tc.exit, "down", "--id-label", "example.test/selection=managed", "--id-label", "example.test/role=primary")
			if tc.exit == 0 && !reflect.DeepEqual(removed.removed, []string{"selected", "sidecar"}) {
				t.Fatalf("removed=%v", removed.removed)
			}
			if tc.exit != 0 && (len(removed.removed) != 0 || len(removed.stopped) != 0) {
				t.Fatalf("refused project had mutations=%+v", removed)
			}
			if tc.workspace == "" && !strings.Contains(result["message"].(string), "requires project and workspace labels") {
				t.Fatalf("missing ownership error=%v", result)
			}
		})
	}
}

func TestManageSelectedComposeProjectRequiresIdentity(t *testing.T) {
	for _, identity := range [][2]string{{"", "/selected-workspace"}, {"selected-project", ""}} {
		t.Run(identity[0]+identity[1], func(t *testing.T) {
			calls := managementEngine(t, nil, false)
			cli, err := newDockerClient()
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			err = (&application{}).composeHost(cli).RemoveProject(context.Background(), identity[0], identity[1])
			if err == nil || !strings.Contains(err.Error(), "requires project and workspace labels") {
				t.Fatalf("error=%v", err)
			}
			if len(calls.removed) != 0 || len(calls.stopped) != 0 {
				t.Fatalf("mutations=%+v", calls)
			}
		})
	}
}
