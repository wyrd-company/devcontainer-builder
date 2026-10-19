// relationships:
//   references: [options_gpu, ../host/docker/gpu]

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

// Exercise the real command through Up's merged image metadata and the Engine
// create boundary; stop there so no lifecycle shell is needed in the fake.
func TestGPUAvailabilityEngineRequests(t *testing.T) { testGPUAvailabilityEngineRequests(t, false) }
func TestComposeGPUAvailabilityEngineRequests(t *testing.T) {
	testGPUAvailabilityEngineRequests(t, true)
}

func testGPUAvailabilityEngineRequests(t *testing.T, compose bool) {
	for _, tc := range []struct {
		name, mode, requirement, runtime, runArgs string
		wantInfo, wantGPU, infoError, createError bool
	}{
		{name: "config-requirement", requirement: `true`, runtime: "nvidia-container-runtime", wantInfo: true, wantGPU: true},
		{name: "default-present", requirement: `true`, runtime: "/usr/bin/nvidia-container-runtime", wantInfo: true, wantGPU: true},
		{name: "detect-absent", mode: "detect", requirement: `true`, wantInfo: true},
		{name: "runtime-arguments", requirement: `true`, runtime: "runtime-wrapper", wantInfo: true, wantGPU: true},
		{name: "detect-other-runtime", mode: "detect", requirement: `true`, runtime: "runc", wantInfo: true},
		{name: "all", mode: "all", requirement: `true`, wantGPU: true},
		{name: "all-engine-rejects", mode: "all", requirement: `true`, wantGPU: true, createError: true},
		{name: "none", mode: "none", requirement: `true`, runtime: "nvidia-container-runtime"},
		{name: "no-requirement", mode: "all"},
		{name: "false-requirement", mode: "all", requirement: `false`},
		{name: "optional", requirement: `"optional"`, runtime: "nvidia-container-runtime", wantInfo: true, wantGPU: true},
		{name: "optional-absent", requirement: `"optional"`, wantInfo: true},
		{name: "object", requirement: `{"cores":2,"memory":"4gb"}`, runtime: "nvidia-container-runtime", wantInfo: true, wantGPU: true},
		{name: "gpus-selection", mode: "none", requirement: `true`, runArgs: `["--gpus","device=GPU-example"]`, wantGPU: true},
		{name: "explicit-selection", mode: "all", requirement: `true`, runArgs: `["--gpus","device=GPU-example"]`, wantGPU: true},
		{name: "none-explicit-selection", mode: "none", requirement: `true`, runArgs: `["--gpus","device=GPU-example"]`, wantGPU: true},
		{name: "detection-fails", requirement: `true`, wantInfo: true, infoError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			workspace := t.TempDir()
			cfg := `{"image":"alpine:3.20"`
			if compose {
				cfg = `{"dockerComposeFile":"compose.yml","service":"app"`
				content := "name: sample\nservices:\n  app:\n    image: alpine:3.20\n    network_mode: host\n"
				if tc.name == "gpus-selection" {
					content += "    gpus:\n      - driver: nvidia\n        device_ids: [GPU-example]\n"
				} else if tc.runArgs != "" {
					content += "    deploy:\n      resources:\n        reservations:\n          devices:\n            - capabilities: [gpu]\n              device_ids: [GPU-example]\n"
				}
				if err := os.WriteFile(filepath.Join(workspace, "compose.yml"), []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if tc.runArgs != "" {
				cfg += `,"runArgs":` + tc.runArgs
			}
			if tc.name == "config-requirement" {
				cfg += `,"hostRequirements":{"gpu":` + tc.requirement + `}`
			}
			cfg += `}`
			if err := os.WriteFile(filepath.Join(workspace, ".devcontainer.json"), []byte(cfg), 0600); err != nil {
				t.Fatal(err)
			}
			infoCalls, createCalls := 0, 0
			var got container.HostConfig
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case strings.HasSuffix(r.URL.Path, "/containers/json"):
					_, _ = w.Write([]byte(`[]`))
				case strings.Contains(r.URL.Path, "/images/"):
					labels := map[string]string{}
					if tc.requirement != "" && tc.name != "config-requirement" {
						labels["devcontainer.metadata"] = `[{"hostRequirements":{"gpu":` + tc.requirement + `}}]`
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"Id": "sha256:example", "Config": map[string]any{"Labels": labels}})
				case strings.HasSuffix(r.URL.Path, "/info"):
					infoCalls++
					if tc.infoError {
						http.Error(w, `{"message":"unavailable"}`, 500)
						return
					}
					record := map[string]any{"path": tc.runtime}
					if tc.name == "runtime-arguments" {
						record["runtimeArgs"] = []string{"nvidia-container-runtime"}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"Runtimes": map[string]any{"nvidia": record}})
				case strings.HasSuffix(r.URL.Path, "/containers/create"):
					createCalls++
					var body struct{ HostConfig container.HostConfig }
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					got = body.HostConfig
					if tc.createError {
						http.Error(w, `{"message":"could not select device driver with capabilities: [[gpu]]"}`, 500)
						return
					}
					_, _ = w.Write([]byte(`{"Id":"sample-container"}`))
				case strings.HasSuffix(r.URL.Path, "/containers/sample-container/json"):
					http.Error(w, `{"message":"test stopped after creation"}`, 500)
				default:
					t.Errorf("unexpected Engine call: %s %s", r.Method, r.URL.Path)
					http.Error(w, `{}`, 500)
				}
			}))
			defer srv.Close()
			t.Setenv("DOCKER_HOST", "tcp://"+srv.Listener.Addr().String())
			t.Setenv("DOCKER_API_VERSION", "1.47")
			args := []string{"up", "--workspace-folder", workspace, "--update-remote-user-uid-default=never"}
			if tc.mode != "" {
				args = append(args, "--gpu-availability="+tc.mode)
			}
			result, logs := invoke(t, 1, args...)
			wantWarning := tc.requirement != "" && tc.requirement != `false` && tc.requirement != `"optional"` && !tc.wantGPU && !tc.infoError
			if strings.Contains(logs, "No GPU support found") != wantWarning {
				t.Fatalf("warning=%q want present=%v", logs, wantWarning)
			}
			wantInfo := 0
			if tc.wantInfo {
				wantInfo = 1
			}
			wantCreate := 1
			if tc.infoError {
				wantCreate = 0
			}
			if infoCalls != wantInfo || createCalls != wantCreate {
				t.Fatalf("info/create calls %d/%d want %d/%d; result=%v", infoCalls, createCalls, wantInfo, wantCreate, result)
			}
			var want []container.DeviceRequest
			if tc.wantGPU {
				req := container.DeviceRequest{Count: -1, Capabilities: [][]string{{"gpu"}}}
				if tc.runArgs != "" {
					req.Count = 0
					req.DeviceIDs = []string{"GPU-example"}
					if compose && tc.name == "gpus-selection" {
						req.Driver = "nvidia"
					}
				}
				want = []container.DeviceRequest{req}
			}
			if !reflect.DeepEqual(got.DeviceRequests, want) {
				t.Fatalf("requests=%+v want %+v", got.DeviceRequests, want)
			}
			message, _ := result["message"].(string)
			expected := "test stopped after creation"
			if tc.infoError {
				expected = "detect GPU support"
			}
			if tc.createError {
				expected = "could not select device driver"
			}
			if !strings.Contains(message, expected) {
				t.Fatalf("result=%v want %s", result, expected)
			}
		})
	}
}

func TestGPUAvailabilityFlag(t *testing.T) {
	cmd := (&application{}).upCommand()
	flag := cmd.Flags().Lookup("gpu-availability")
	if flag == nil || flag.DefValue != "detect" || flag.Value.Type() != "string" {
		t.Fatalf("flag=%+v", flag)
	}
	for _, mode := range []string{"", "invalid"} {
		result, _ := invoke(t, 2, "up", "--gpu-availability="+mode)
		if result["message"] != "--gpu-availability must be all, detect, or none" {
			t.Fatalf("result=%v", result)
		}
	}
}
