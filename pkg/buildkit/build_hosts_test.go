// relationships:
//
//	references: [build_hosts, dockerfile, gateway]
package buildkit

import (
	"context"
	"errors"
	"github.com/moby/buildkit/client"
	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBuildExtraHostsFormat(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries []string
		want    string
		invalid bool
	}{
		{name: "empty"},
		{name: "ipv4", entries: []string{"sample:127.0.0.1"}, want: "sample=127.0.0.1"},
		{name: "ipv6", entries: []string{"sample=[2001:0db8::1]"}, want: "sample=2001:0db8::1"},
		{name: "multiple", entries: []string{"sample=127.0.0.1,[::1]", "sample:127.0.0.2"}, want: "sample=127.0.0.1,sample=::1,sample=127.0.0.2"},
		{name: "gateway", entries: []string{"sample:host-gateway"}, want: "sample=192.0.2.1"},
		{name: "bad-ip", entries: []string{"sample:no-ip"}, invalid: true},
		{name: "bad-host", entries: []string{":127.0.0.1"}, invalid: true},
		{name: "empty-entry", entries: []string{""}, invalid: true},
		{name: "empty-ip", entries: []string{"sample="}, invalid: true},
		{name: "unclosed-bracket", entries: []string{"sample=[::1"}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := formatExtraHosts(tc.entries, func() (net.IP, error) { return net.ParseIP("192.0.2.1"), nil })
			if (err != nil) != tc.invalid || got != tc.want {
				t.Fatalf("got=%q err=%v want=%q invalid=%v", got, err, tc.want, tc.invalid)
			}
		})
	}
	_, err := formatExtraHosts([]string{"sample:host-gateway"}, func() (net.IP, error) { return nil, errors.New("lookup failed") })
	if err == nil {
		t.Fatal("gateway failure discarded")
	}
	if _, err := resolveExtraHosts(context.Background(), []string{"sample:host-gateway"}, &Connection{}); err == nil {
		t.Fatal("remote gateway accepted")
	}
}

func TestDockerfileExtraHostsFrontend(t *testing.T) {
	for _, hosts := range []string{"", "sample=127.0.0.1,sample=::1"} {
		spec := buildSpecWithUpstream()
		bs := buildSettings{extraHosts: hosts}
		direct := dockerfileFrontendAttrs(spec.Upstream, bs)
		gc := &fakeGateway{baseConfig: baseConfigJSON(t)}
		if _, _, err := buildOnePlatform(context.Background(), gc, spec, amd64, bs); err != nil {
			t.Fatal(err)
		}
		if len(gc.frontends) != 1 {
			t.Fatalf("frontends=%v", gc.frontends)
		}
		for route, attrs := range map[string]map[string]string{"standalone": direct, "nested": gc.frontends[0].FrontendOpt} {
			got, present := attrs["add-hosts"]
			if got != hosts || present != (hosts != "") {
				t.Fatalf("%s hosts=%q present=%v want=%q", route, got, present, hosts)
			}
		}
	}
}

func TestWorkerHostGateway(t *testing.T) {
	for _, tc := range []struct {
		name    string
		labels  []map[string]string
		want    string
		invalid bool
	}{
		{name: "absent", invalid: true},
		{name: "empty", labels: []map[string]string{{"org.mobyproject.buildkit.worker.moby.host-gateway-ip": ""}}, invalid: true},
		{name: "invalid", labels: []map[string]string{{"org.mobyproject.buildkit.worker.moby.host-gateway-ip": "bad"}}, invalid: true},
		{name: "first", labels: []map[string]string{{}, {"org.mobyproject.buildkit.worker.moby.host-gateway-ip": "192.0.2.1"}, {"org.mobyproject.buildkit.worker.moby.host-gateway-ip": "192.0.2.2"}}, want: "192.0.2.1"},
		{name: "ipv6", labels: []map[string]string{{"org.mobyproject.buildkit.worker.moby.host-gateway-ip": "2001:0db8::1"}}, want: "2001:db8::1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var workers []*client.WorkerInfo
			for _, labels := range tc.labels {
				workers = append(workers, &client.WorkerInfo{Labels: labels})
			}
			ip, err := workerHostGateway(workers)
			if (err != nil) != tc.invalid || (!tc.invalid && ip.String() != tc.want) {
				t.Fatalf("ip=%v err=%v", ip, err)
			}
		})
	}
}

// Exercise the public routes with a live caller context. Cancellation is only
// test teardown on failure; it must not be needed to return validation errors.
func TestExtraHostsFailureReturns(t *testing.T) {
	t.Setenv("BUILDKIT_HOST", "")
	for _, nested := range []bool{false, true} {
		name := "standalone"
		if nested {
			name = "nested"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0600); err != nil {
				t.Fatal(err)
			}
			b := DockerfileBuild{ContextDir: dir, ExtraHosts: []string{"sample:bad"}}
			opt := SolveOptions{Docker: unusedNetworkEngine(t), Output: "type=cacheonly", Stderr: io.Discard, Progress: "plain"}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var err error
				if nested {
					_, err = SolveDevcontainer(ctx, BuildSpec{Upstream: &b, Graph: GraphInputs{Spec: &spec.DevContainer{}}}, opt)
				} else {
					_, err = SolveDockerfile(ctx, b, opt)
				}
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "invalid host sample:bad") {
					t.Fatalf("error=%v", err)
				}
			case <-time.After(2 * time.Second):
				cancel()
				<-done
				t.Fatal("host validation error did not return with a live caller context")
			}
		})
	}
}
