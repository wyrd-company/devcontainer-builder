// relationships:
//   references: [runargs, create_controls]

package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dockeropts "github.com/docker/cli/opts"
	"github.com/docker/docker/api/types/container"
	dockerclient "github.com/docker/docker/client"
	mobyclient "github.com/moby/moby/client"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestRunArgsUmask(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want uint32
	}{
		{[]string{"--umask=0022"}, 18}, {[]string{"--umask", "0"}, 0},
		{[]string{"--umask=0077", "--umask=0002"}, 2}, {[]string{"--umask=37777777777"}, 4294967295},
	} {
		var controls runControls
		if _, err := applyRunArgsControls(tc.args, &container.Config{}, &container.HostConfig{}, &controls); err != nil {
			t.Fatal(err)
		}
		value := controls.Umask
		if value == nil || *value != tc.want {
			t.Fatalf("%v: %v want %d", tc.args, value, tc.want)
		}
	}
	for _, value := range []string{"", "888", "0o22", "-1", "40000000000", " 22"} {
		if _, err := applyRunArgs([]string{"--umask=" + value}, &container.Config{}, &container.HostConfig{}); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
}

func TestRunArgsKernelMemoryStub(t *testing.T) {
	for _, value := range []string{"64m", "0"} {
		_, host := applyForTest(t, []string{"--kernel-memory=" + value})
		if host.KernelMemory != 0 {
			t.Fatalf("deprecated kernel memory applied: %+v", host)
		}
	}
	if _, _, err := tryApply([]string{"--kernel-memory=invalid"}); err == nil {
		t.Fatal("accepted invalid memory size")
	}
	if _, _, err := tryApply([]string{"--kernel-memory"}); err == nil {
		t.Fatal("accepted missing value")
	}
}

func TestCreateUmaskRequest(t *testing.T) {
	for _, merged := range []bool{false, true} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("API-Version", "1.56")
			if r.URL.Path == "/_ping" {
				return
			}
			if strings.HasSuffix(r.URL.Path, "/sample-id/json") {
				_, _ = w.Write([]byte(`{"Id":"sample-id","HostConfig":{"Umask":18}}`))
				return
			}
			if !strings.HasSuffix(r.URL.Path, "/containers/create") {
				t.Errorf("unexpected %s", r.URL)
				http.Error(w, "unexpected", 404)
				return
			}
			calls++
			var body struct {
				HostConfig struct {
					Umask  *uint32
					Memory int64
				}
				NetworkingConfig json.RawMessage
				Image            string
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body.HostConfig.Umask == nil || *body.HostConfig.Umask != 18 || body.HostConfig.Memory != 67108864 || body.Image != "alpine" || !strings.Contains(string(body.NetworkingConfig), "sample") {
				t.Errorf("lost creation fields: %+v", body)
			}
			_, _ = w.Write([]byte(`{"Id":"sample-id"}`))
		}))
		cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost("tcp://" + srv.Listener.Addr().String()))
		if err != nil {
			t.Fatal(err)
		}
		req := UpRequest{Image: "alpine"}
		if merged {
			req.metadataEntries = []metadata.Entry{}
		}
		dc := &spec.DevContainer{RunArgs: []string{"--umask=0022", "--memory=64m", "--network=name=sample,ip=192.0.2.2"}}
		native, err := mobyclient.New(mobyclient.WithHost("tcp://" + srv.Listener.Addr().String()))
		if err != nil {
			t.Fatal(err)
		}
		defer native.Close()
		id, err := New(cli, Options{NewCreateClient: func() (*mobyclient.Client, error) { return native, nil }}).create(context.Background(), dc, req, "")
		cli.Close()
		srv.Close()
		if err != nil || id != "sample-id" || calls != 1 {
			t.Fatalf("create %s %v calls %d", id, err, calls)
		}
	}
}

func TestUmaskConfirmation(t *testing.T) {
	for _, tc := range []struct {
		name, body                  string
		inspectStatus, removeStatus int
		wantOK                      bool
	}{
		{"zero", `{"HostConfig":{"Umask":0}}`, 200, 204, true},
		{"absent", `{"HostConfig":{}}`, 200, 204, false},
		{"mismatch", `{"HostConfig":{"Umask":18}}`, 200, 204, false},
		{"inspect-error", `{"message":"sample failure"}`, 500, 204, false},
		{"cleanup-error", `{"HostConfig":{}}`, 200, 500, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			removed := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					var req struct{ HostConfig struct{ Umask *uint32 } }
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
						t.Error(err)
					}
					if req.HostConfig.Umask == nil || *req.HostConfig.Umask != 0 {
						t.Error("explicit zero lost")
					}
					_, _ = w.Write([]byte(`{"Id":"sample-id"}`))
					return
				}
				if r.Method == "DELETE" {
					removed++
					w.WriteHeader(tc.removeStatus)
					_, _ = w.Write([]byte(`{"message":"remove failure"}`))
					return
				}
				w.WriteHeader(tc.inspectStatus)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost("tcp://"+srv.Listener.Addr().String()), dockerclient.WithVersion("1.47"))
			if err != nil {
				t.Fatal(err)
			}
			defer cli.Close()
			zero := uint32(0)
			native, err := mobyclient.New(mobyclient.WithHost("tcp://"+srv.Listener.Addr().String()), mobyclient.WithAPIVersion("1.56"))
			if err != nil {
				t.Fatal(err)
			}
			h := New(cli, Options{NewCreateClient: func() (*mobyclient.Client, error) { return native, nil }})
			response, err := h.createWithRunControls(context.Background(), &runControls{Umask: &zero}, &container.Config{Image: "alpine"}, &container.HostConfig{}, nil, "")
			if tc.wantOK {
				if err != nil || response.ID != "sample-id" || removed != 0 {
					t.Fatalf("%+v %v removed %d", response, err, removed)
				}
			} else {
				if err == nil || removed != 1 || response.ID != "" {
					t.Fatalf("silent umask loss: %+v %v removed %d", response, err, removed)
				}
				if tc.removeStatus == 500 && !strings.Contains(err.Error(), "sample-id") {
					t.Fatal("cleanup error omitted container ID")
				}
			}
		})
	}
}

func TestUmaskClientPolicy(t *testing.T) {
	for _, tlsEnabled := range []bool{false, true} {
		calls := 0
		pings := 0
		handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Sample") != "keep" || r.UserAgent() != "sample-agent" {
				t.Errorf("lost headers on %s: %v", r.URL, r.Header)
			}
			w.Header().Set("API-Version", "1.56")
			if r.URL.Path == "/_ping" {
				pings++
				return
			}
			if !strings.HasPrefix(r.URL.Path, "/v1.56/") {
				t.Errorf("lost fixed API policy: %s", r.URL)
			}
			if strings.HasSuffix(r.URL.Path, "/containers/create") {
				calls++
				_, _ = w.Write([]byte(`{"Id":"sample-id"}`))
				return
			}
			if strings.HasSuffix(r.URL.Path, "/sample-id/json") {
				_, _ = w.Write([]byte(`{"Id":"sample-id","HostConfig":{"Umask":18}}`))
				return
			}
			t.Errorf("unexpected request %s", r.URL)
		})
		srv := httptest.NewUnstartedServer(handler)
		if tlsEnabled {
			srv.StartTLS()
		} else {
			srv.Start()
		}
		headers := map[string]string{"X-Sample": "keep"}
		cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost("tcp://"+srv.Listener.Addr().String()), dockerclient.WithHTTPClient(srv.Client()), dockerclient.WithHTTPHeaders(headers), dockerclient.WithUserAgent("sample-agent"), dockerclient.WithVersion("1.56"))
		if err != nil {
			t.Fatal(err)
		}
		scheme := "http"
		if tlsEnabled {
			scheme = "https"
		}
		native, err := mobyclient.New(mobyclient.WithHost("tcp://"+srv.Listener.Addr().String()), mobyclient.WithHTTPClient(srv.Client()), mobyclient.WithScheme(scheme), mobyclient.WithHTTPHeaders(headers), mobyclient.WithUserAgent("sample-agent"), mobyclient.WithAPIVersion("1.56"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = New(cli, Options{NewCreateClient: func() (*mobyclient.Client, error) { return native, nil }}).create(context.Background(), &spec.DevContainer{RunArgs: []string{"--umask=0022"}}, UpRequest{Image: "alpine"}, "")
		if err != nil || calls != 1 || pings != 0 {
			t.Fatalf("TLS %v: %v calls %d pings %d", tlsEnabled, err, calls, pings)
		}
		if _, err := cli.Ping(context.Background()); err != nil {
			t.Fatal("legacy client no longer usable", err)
		}
		native.Close()
		cli.Close()
		srv.Close()
	}
}

func TestUmaskAssemblyReadsFilesOnce(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "sample.env")
	if err := os.WriteFile(file, []byte("SAMPLE=retained\n"), 0600); err != nil {
		t.Fatal(err)
	}
	controls := &runControls{}
	cfg, host, name, err := buildContainerConfigControls(&spec.DevContainer{RunArgs: []string{"--umask=0022", "--env-file=" + file}}, nil, "alpine", "", "", "", controls)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			var req struct{ Env []string }
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Error(err)
			}
			if len(req.Env) != 1 || req.Env[0] != "SAMPLE=retained" {
				t.Error("assembled environment lost", req.Env)
			}
		}
		_, _ = w.Write([]byte(`{"Id":"sample-id","HostConfig":{"Umask":18}}`))
	}))
	defer srv.Close()
	cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost("tcp://"+srv.Listener.Addr().String()), dockerclient.WithVersion("1.56"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	native, err := mobyclient.New(mobyclient.WithHost("tcp://"+srv.Listener.Addr().String()), mobyclient.WithAPIVersion("1.56"))
	if err != nil {
		t.Fatal(err)
	}
	h := New(cli, Options{NewCreateClient: func() (*mobyclient.Client, error) { return native, nil }})
	if _, err := h.createWithRunControls(context.Background(), controls, cfg, host, nil, name); err != nil {
		t.Fatal(err)
	}
}

func TestNativeControlGrammar(t *testing.T) {
	for _, input := range []string{"", "0", "0022", "0777", "7777", "37777777777", "40000000000", "0o22", "-1", "+22", " 22", "888", "64m", "invalid"} {
		var mask dockeropts.UmaskOpt
		nativeErr := mask.Set(input)
		var controls runControls
		_, err := applyRunArgsControls([]string{"--umask=" + input}, &container.Config{}, &container.HostConfig{}, &controls)
		if (err == nil) != (nativeErr == nil) {
			t.Fatalf("umask %q DCB %v native %v", input, err, nativeErr)
		}
		if err == nil && (controls.Umask == nil || *controls.Umask != *mask.Value()) {
			t.Fatalf("umask %q value lost", input)
		}
		var stub dockeropts.MemBytes
		nativeErr = stub.Set(input)
		_, err = applyRunArgs([]string{"--kernel-memory=" + input}, &container.Config{}, &container.HostConfig{})
		if (err == nil) != (nativeErr == nil) {
			t.Fatalf("kernel memory %q DCB %v native %v", input, err, nativeErr)
		}
	}
}

func TestNativeCreateNameConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"message":"name in use"}`))
	}))
	defer srv.Close()
	cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost("tcp://"+srv.Listener.Addr().String()), dockerclient.WithVersion("1.56"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	factory := func() (*mobyclient.Client, error) {
		return mobyclient.New(mobyclient.WithHost("tcp://"+srv.Listener.Addr().String()), mobyclient.WithAPIVersion("1.56"))
	}
	_, err = New(cli, Options{NewCreateClient: factory}).create(context.Background(), &spec.DevContainer{RunArgs: []string{"--name=sample", "--umask=0022"}}, UpRequest{Image: "alpine"}, "")
	if err == nil || !strings.Contains(err.Error(), `container name "sample" is already in use`) {
		t.Fatalf("lost name conflict classification: %v", err)
	}
}

func TestUmaskNeedsNativeFactory(t *testing.T) {
	cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost("tcp://127.0.0.1:1"), dockerclient.WithVersion("1.56"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	_, err = New(cli, Options{}).create(context.Background(), &spec.DevContainer{RunArgs: []string{"--umask=0022"}}, UpRequest{Image: "alpine"}, "")
	if err == nil || !strings.Contains(err.Error(), "Options.NewCreateClient") {
		t.Fatalf("missing factory: %v", err)
	}
}
