package docker

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
)

// applyPorts publishes the spec's appPort entries on the container
// Config / HostConfig.
//
// Numeric appPort entries bind 127.0.0.1:N:N. String entries retain Docker
// publishing syntax, including dynamically allocated host ports. forwardPorts
// are forwarded by a connected tool and are not daemon port publications.
func applyPorts(app any, cfg *container.Config, host *container.HostConfig) error {
	specs, err := portSpecs(app)
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

// portSpecs flattens appPort into a docker -p style list, normalizing
// bare numeric entries to the loopback-bound form.
func portSpecs(app any) ([]string, error) {
	out := []string{}
	switch v := app.(type) {
	case nil:
		// nothing
	case []any:
		for _, e := range v {
			s, err := normalizePort(e, "appPort")
			if err != nil {
				return nil, err
			}
			out = append(out, s)
		}
	default:
		s, err := normalizePort(v, "appPort")
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

// normalizePort accepts a number or string appPort entry and returns a
// docker -p compatible spec.
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
		return v, nil
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
