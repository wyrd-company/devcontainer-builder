package docker

import (
	"reflect"
	"sort"
	"testing"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"

	"github.com/wyrd-company/devcontainer-builder/pkg/spec"
)

func TestNormalizePort(t *testing.T) {
	cases := []struct {
		name    string
		in      any
		want    string
		wantErr bool
	}{
		{"int", 3000, "127.0.0.1:3000:3000", false},
		{"float64", float64(8080), "127.0.0.1:8080:8080", false},
		{"bare numeric string", "9000", "127.0.0.1:9000:9000", false},
		{"numeric string with proto", "9000/udp", "127.0.0.1:9000:9000/udp", false},
		{"port range", "8000-8002", "127.0.0.1:8000-8002:8000-8002", false},
		// Anything with a colon is left alone — explicit binding wins.
		{"host:container preserved", "8080:80", "8080:80", false},
		{"iface:host:container preserved", "127.0.0.1:8080:80", "127.0.0.1:8080:80", false},
		{"empty string skipped", "", "", false},
		{"nil skipped", nil, "", false},
		{"out-of-range numeric", 70000, "", true},
		{"zero numeric", 0, "", true},
		{"unsupported type", true, "", true},
		{"non-numeric bare", "abc", "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := normalizePort(c.in, "forwardPorts")
			if (err != nil) != c.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, c.wantErr)
			}
			if got != c.want {
				t.Errorf("got %q want %q", got, c.want)
			}
		})
	}
}

func TestApplyPorts_FromSpecForwardPorts(t *testing.T) {
	cfg := &container.Config{}
	host := &container.HostConfig{}
	if err := applyPorts([]any{3000, "9000", "127.0.0.1:8080:80"}, nil, cfg, host); err != nil {
		t.Fatalf("applyPorts: %v", err)
	}

	wantExposed := []string{"3000/tcp", "9000/tcp", "80/tcp"}
	gotExposed := []string{}
	for p := range cfg.ExposedPorts {
		gotExposed = append(gotExposed, string(p))
	}
	sort.Strings(gotExposed)
	sort.Strings(wantExposed)
	if !reflect.DeepEqual(gotExposed, wantExposed) {
		t.Errorf("exposed ports: got %v want %v", gotExposed, wantExposed)
	}

	// 3000 + 9000 should bind to 127.0.0.1; 80 should keep 127.0.0.1:8080 explicit.
	got := func(port nat.Port) []nat.PortBinding {
		return host.PortBindings[port]
	}
	if b := got("3000/tcp"); len(b) != 1 || b[0].HostIP != "127.0.0.1" || b[0].HostPort != "3000" {
		t.Errorf("3000/tcp binding: %+v", b)
	}
	if b := got("9000/tcp"); len(b) != 1 || b[0].HostIP != "127.0.0.1" || b[0].HostPort != "9000" {
		t.Errorf("9000/tcp binding: %+v", b)
	}
	if b := got("80/tcp"); len(b) != 1 || b[0].HostIP != "127.0.0.1" || b[0].HostPort != "8080" {
		t.Errorf("80/tcp binding: %+v", b)
	}
}

func TestApplyPorts_AppPortVariants(t *testing.T) {
	t.Run("scalar int", func(t *testing.T) {
		cfg, host := &container.Config{}, &container.HostConfig{}
		if err := applyPorts(nil, 4000, cfg, host); err != nil {
			t.Fatal(err)
		}
		if _, ok := cfg.ExposedPorts["4000/tcp"]; !ok {
			t.Errorf("4000/tcp not exposed: %v", cfg.ExposedPorts)
		}
	})
	t.Run("scalar string with proto", func(t *testing.T) {
		cfg, host := &container.Config{}, &container.HostConfig{}
		if err := applyPorts(nil, "5000/udp", cfg, host); err != nil {
			t.Fatal(err)
		}
		if _, ok := cfg.ExposedPorts["5000/udp"]; !ok {
			t.Errorf("5000/udp not exposed: %v", cfg.ExposedPorts)
		}
	})
	t.Run("array form", func(t *testing.T) {
		cfg, host := &container.Config{}, &container.HostConfig{}
		if err := applyPorts(nil, []any{4000, "5000"}, cfg, host); err != nil {
			t.Fatal(err)
		}
		if len(cfg.ExposedPorts) != 2 {
			t.Errorf("expected 2 exposed ports: %v", cfg.ExposedPorts)
		}
	})
}

func TestApplyPorts_ForwardPortsAppendsToRunArgs(t *testing.T) {
	dc := &spec.DevContainer{
		ForwardPorts: []any{3000},
		// User also published port 4000 explicitly via runArgs.
		RunArgs: []string{"-p", "127.0.0.1:4000:4000"},
	}
	cfg, host, err := buildContainerConfig(dc, nil, "img", "/w", "/h", "")
	if err != nil {
		t.Fatalf("buildContainerConfig: %v", err)
	}
	if _, ok := cfg.ExposedPorts["3000/tcp"]; !ok {
		t.Errorf("3000/tcp from forwardPorts missing: %v", cfg.ExposedPorts)
	}
	if _, ok := cfg.ExposedPorts["4000/tcp"]; !ok {
		t.Errorf("4000/tcp from runArgs missing: %v", cfg.ExposedPorts)
	}
	if len(host.PortBindings) != 2 {
		t.Errorf("expected 2 port bindings, got %d: %v", len(host.PortBindings), host.PortBindings)
	}
}

func TestApplyPorts_InvalidEntryReturnsError(t *testing.T) {
	cfg, host := &container.Config{}, &container.HostConfig{}
	if err := applyPorts([]any{"not-a-port"}, nil, cfg, host); err == nil {
		t.Errorf("expected error for invalid forwardPorts entry")
	}
}
