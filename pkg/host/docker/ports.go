package docker

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
)

// applyPorts folds the spec's forwardPorts and appPort entries into the
// container Config / HostConfig.
//
// Upstream's IDE-in-the-loop CLI treats forwardPorts as metadata and
// only translates appPort to docker -p. We don't have an IDE, so the
// only way for either to actually surface to the user is via daemon
// port publishing — so we publish both, using the same convention
// upstream uses for appPort: a bare port number binds 127.0.0.1:N:N
// so listeners stay on loopback by default. Strings already in
// "host:container" or "iface:host:container" form are honored verbatim.
func applyPorts(forward []any, app any, cfg *container.Config, host *container.HostConfig) error {
	specs, err := portSpecs(forward, app)
	if err != nil {
		return err
	}
	if len(specs) == 0 {
		return nil
	}
	exposed, bindings, err := nat.ParsePortSpecs(specs)
	if err != nil {
		return fmt.Errorf("port publishing: %w", err)
	}
	if cfg.ExposedPorts == nil {
		cfg.ExposedPorts = nat.PortSet{}
	}
	for p := range exposed {
		cfg.ExposedPorts[p] = struct{}{}
	}
	if host.PortBindings == nil {
		host.PortBindings = nat.PortMap{}
	}
	for p, b := range bindings {
		host.PortBindings[p] = append(host.PortBindings[p], b...)
	}
	return nil
}

// portSpecs flattens forwardPorts and appPort into a docker -p style
// list, normalizing bare numeric entries to the loopback-bound form.
func portSpecs(forward []any, app any) ([]string, error) {
	out := []string{}
	for _, v := range forward {
		s, err := normalizePort(v, "forwardPorts")
		if err != nil {
			return nil, err
		}
		if s != "" {
			out = append(out, s)
		}
	}
	switch v := app.(type) {
	case nil:
		// nothing
	case []any:
		for _, e := range v {
			s, err := normalizePort(e, "appPort")
			if err != nil {
				return nil, err
			}
			if s != "" {
				out = append(out, s)
			}
		}
	default:
		s, err := normalizePort(v, "appPort")
		if err != nil {
			return nil, err
		}
		if s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// normalizePort accepts a number or string entry from forwardPorts /
// appPort and returns a docker -p compatible spec.
//
// Rules:
//   - Numeric N or "N"          → "127.0.0.1:N:N"
//   - "N/proto"                 → "127.0.0.1:N:N/proto"
//   - "N-M" or "N-M/proto"      → "127.0.0.1:N-M:N-M[/proto]"
//   - "host:container[/proto]"  → as-is (binds all interfaces)
//   - "iface:host:container..." → as-is
func normalizePort(raw any, field string) (string, error) {
	switch v := raw.(type) {
	case nil:
		return "", nil
	case float64:
		return numericPortSpec(int(v), field)
	case int:
		return numericPortSpec(v, field)
	case int64:
		return numericPortSpec(int(v), field)
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return "", fmt.Errorf("%s: %q: %w", field, v.String(), err)
		}
		return numericPortSpec(int(n), field)
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return "", nil
		}
		// Already an "iface:host:container" or "host:container" form?
		// Anything with a colon is left alone — the user has been
		// explicit about the binding shape.
		if strings.ContainsRune(s, ':') {
			return s, nil
		}
		// Bare "N", "N/proto", "N-M", "N-M/proto" — split off the proto.
		port, proto, hasProto := strings.Cut(s, "/")
		if _, err := parsePortRange(port); err != nil {
			return "", fmt.Errorf("%s: %q: %w", field, v, err)
		}
		spec := "127.0.0.1:" + port + ":" + port
		if hasProto {
			spec += "/" + proto
		}
		return spec, nil
	default:
		return "", fmt.Errorf("%s: unsupported entry %T", field, raw)
	}
}

func numericPortSpec(n int, field string) (string, error) {
	if n < 1 || n > 65535 {
		return "", fmt.Errorf("%s: port %d out of range", field, n)
	}
	s := strconv.Itoa(n)
	return "127.0.0.1:" + s + ":" + s, nil
}

// parsePortRange validates "N" or "N-M". Returns "" on success or an
// error describing the malformation.
func parsePortRange(s string) (string, error) {
	if s == "" {
		return "", fmt.Errorf("empty port")
	}
	low, high, hasRange := strings.Cut(s, "-")
	if _, err := strconv.Atoi(low); err != nil {
		return "", fmt.Errorf("invalid port %q", s)
	}
	if hasRange {
		if _, err := strconv.Atoi(high); err != nil {
			return "", fmt.Errorf("invalid port range %q", s)
		}
	}
	return s, nil
}
