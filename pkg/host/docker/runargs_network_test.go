// relationships:
//
//	references: [runargs_network, up, schema-conformance]
package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
	"github.com/wyrd-company/devcontainer-builder/pkg/metadata"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestRunArgsNetworkEndpoints(t *testing.T) {
	cfg, host, networking := &container.Config{}, &container.HostConfig{}, &network.NetworkingConfig{}
	args := []string{"--network=sample", "--ip=192.0.2.2", "--ip=192.0.2.3", "--ip6=2001:db8::3", "--net-alias=first", "--network-alias=second", "--link-local-ip=169.254.1.2", "--link-local-ip=invalid", "--link-local-ip=fe80::3", "--mac-address=02:42:ac:11:00:02", "--network=name=other,alias=third,ip=192.0.2.4,gw-priority=2,driver-opt=sample=value"}
	if _, err := applyRunArgs(args, cfg, host, networking); err != nil {
		t.Fatal(err)
	}
	ep := networking.EndpointsConfig["sample"]
	if host.NetworkMode != "sample" || ep == nil || ep.IPAMConfig == nil {
		t.Fatalf("network=%+v host=%+v", networking, host)
	}
	if ep.IPAMConfig.IPv4Address != "192.0.2.3" {
		t.Fatalf("IPv4=%+v", ep.IPAMConfig)
	}
	if ep.IPAMConfig.IPv6Address != "2001:db8::3" {
		t.Fatalf("IPv6=%+v", ep.IPAMConfig)
	}
	if !reflect.DeepEqual(ep.Aliases, []string{"first", "second"}) {
		t.Fatalf("aliases=%v", ep.Aliases)
	}
	if !reflect.DeepEqual(ep.IPAMConfig.LinkLocalIPs, []string{"169.254.1.2", "fe80::3"}) {
		t.Fatalf("link local=%v", ep.IPAMConfig.LinkLocalIPs)
	}
	if cfg.MacAddress != "" || ep.MacAddress != "02:42:ac:11:00:02" {
		t.Fatalf("MAC cfg=%q ep=%q", cfg.MacAddress, ep.MacAddress)
	}
	other := networking.EndpointsConfig["other"]
	if other == nil || other.GwPriority != 2 || other.IPAMConfig.IPv4Address != "192.0.2.4" || !reflect.DeepEqual(other.Aliases, []string{"third"}) || other.DriverOpts["sample"] != "value" {
		t.Fatalf("other=%+v", other)
	}
}
func TestRunArgsNetworkValidation(t *testing.T) {
	for _, args := range [][]string{
		{"--ip=invalid"}, {"--ip6=invalid"}, {"--network=sample", "--network=sample"},
		{"--network=host", "--net-alias=sample"}, {"--network=sample", "--network=host"},
		{"--network=name=sample,ip=192.0.2.2", "--ip=192.0.2.3"},
		{"--network=name=sample,ip6=2001:db8::2", "--ip6=2001:db8::3"},
		{"--network=name=sample,alias=first", "--net-alias=second"},
		{"--network=name=sample,link-local-ip=fe80::2", "--link-local-ip=fe80::3"},
		{"--network=name=sample,mac-address=02:42:ac:11:00:02", "--mac-address=02:42:ac:11:00:03"},
		{"--network=name=sample,unknown=value"}, {"--network="},
	} {
		if _, err := applyRunArgs(args, &container.Config{}, &container.HostConfig{}); err == nil {
			t.Errorf("accepted %v", args)
		}
	}
}
func TestCreateNetworkRequest(t *testing.T) {
	for _, merged := range []bool{false, true} {
		calls := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !strings.HasSuffix(r.URL.Path, "/containers/create") {
				t.Errorf("unexpected %s", r.URL)
				http.Error(w, "unexpected", 404)
				return
			}
			calls++
			var body struct{ NetworkingConfig network.NetworkingConfig }
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			ep := body.NetworkingConfig.EndpointsConfig["sample"]
			if ep == nil || ep.IPAMConfig == nil || ep.IPAMConfig.IPv4Address != "192.0.2.2" {
				t.Errorf("missing endpoint: %+v", body)
			}
			_, _ = w.Write([]byte(`{"Id":"sample-id"}`))
		}))
		cli, err := dockerclient.NewClientWithOpts(dockerclient.WithHost("tcp://"+srv.Listener.Addr().String()), dockerclient.WithVersion("1.47"))
		if err != nil {
			t.Fatal(err)
		}
		dc := &spec.DevContainer{RunArgs: []string{"--net=sample", "--ip=192.0.2.2"}}
		req := UpRequest{Image: "alpine"}
		if merged {
			req.metadataEntries = []metadata.Entry{}
		}
		_, err = New(cli, Options{}).create(context.Background(), dc, req, "")
		cli.Close()
		srv.Close()
		if err != nil || calls != 1 {
			t.Fatalf("create: %v calls=%d", err, calls)
		}
	}
}

func TestRunArgsEmptyIPAssignments(t *testing.T) {
	for _, flag := range []string{"--ip", "--ip6"} {
		for _, prior := range []string{"", "192.0.2.2", "2001:db8::2"} {
			args := []string{"--net=sample"}
			if prior != "" {
				args = append(args, flag+"="+prior)
			}
			cfg, host, want := &container.Config{}, &container.HostConfig{}, &network.NetworkingConfig{}
			if _, err := applyRunArgs(args, cfg, host, want); err != nil {
				t.Fatal(err)
			}
			args = append(args, flag+"=")
			got := &network.NetworkingConfig{}
			if _, err := applyRunArgs(args, &container.Config{}, &container.HostConfig{}, got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%v: got %+v want %+v", args, got, want)
			}
		}
		if _, err := applyRunArgs([]string{flag + "= "}, &container.Config{}, &container.HostConfig{}); err == nil {
			t.Fatalf("accepted whitespace %s", flag)
		}
	}
}
