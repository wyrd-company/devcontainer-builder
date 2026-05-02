package kubernetes

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
)

// applyPorts translates devcontainer.json's forwardPorts and appPort
// entries into corev1.ContainerPort entries on the projected
// container.
//
// Kubernetes pods don't have a "publish to localhost" model, so
// forwardPorts and appPort collapse to the same shape — declared
// container ports. The embedder is responsible for fronting them with
// a Service if external reachability is desired; the docker host's
// 127.0.0.1:N:N convention has no meaning on a cluster.
//
// The "host:container" form that pkg/host/docker honors verbatim is
// rejected here: declaring an explicit host:port assumes a cluster
// node mapping (hostPort), which is rarely what an embedder wants and
// silently picks node-local resources. If the user explicitly wants
// hostPort, --publish in runArgs is the place to spell that out.
func applyPorts(c *corev1.Container, forward []any, app any) error {
	for _, v := range forward {
		ports, err := normalizePort(v, "forwardPorts")
		if err != nil {
			return err
		}
		c.Ports = append(c.Ports, ports...)
	}
	switch v := app.(type) {
	case nil:
		// nothing
	case []any:
		for _, e := range v {
			ports, err := normalizePort(e, "appPort")
			if err != nil {
				return err
			}
			c.Ports = append(c.Ports, ports...)
		}
	default:
		ports, err := normalizePort(v, "appPort")
		if err != nil {
			return err
		}
		c.Ports = append(c.Ports, ports...)
	}
	return nil
}

// normalizePort accepts the same scalar forms pkg/host/docker honors
// and returns one or more ContainerPort entries (a "N-M" range
// expands to one entry per port). Any "host:container" or
// "iface:host:container" form returns an error pointing the user at
// runArgs.
func normalizePort(raw any, field string) ([]corev1.ContainerPort, error) {
	switch v := raw.(type) {
	case nil:
		return nil, nil
	case float64:
		return numericContainerPorts(int(v), corev1.ProtocolTCP, field)
	case int:
		return numericContainerPorts(v, corev1.ProtocolTCP, field)
	case int64:
		return numericContainerPorts(int(v), corev1.ProtocolTCP, field)
	case json.Number:
		n, err := v.Int64()
		if err != nil {
			return nil, fmt.Errorf("%s: %q: %w", field, v.String(), err)
		}
		return numericContainerPorts(int(n), corev1.ProtocolTCP, field)
	case string:
		return stringPortSpec(v, field)
	default:
		return nil, fmt.Errorf("%s: unsupported entry %T", field, raw)
	}
}

func stringPortSpec(s, field string) ([]corev1.ContainerPort, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if strings.ContainsRune(s, ':') {
		return nil, fmt.Errorf("%s: host:container form %q is not supported on Kubernetes (declare a Service or use runArgs --publish for hostPort)", field, s)
	}
	port, proto, hasProto := strings.Cut(s, "/")
	low, high, err := parsePortRange(port)
	if err != nil {
		return nil, fmt.Errorf("%s: %q: %w", field, s, err)
	}
	pp := corev1.ProtocolTCP
	if hasProto {
		switch strings.ToLower(proto) {
		case "tcp":
			pp = corev1.ProtocolTCP
		case "udp":
			pp = corev1.ProtocolUDP
		case "sctp":
			pp = corev1.ProtocolSCTP
		default:
			return nil, fmt.Errorf("%s: %q: unknown protocol", field, s)
		}
	}
	return numericContainerPortRange(low, high, pp, field)
}

func numericContainerPorts(n int, proto corev1.Protocol, field string) ([]corev1.ContainerPort, error) {
	if n < 1 || n > 65535 {
		return nil, fmt.Errorf("%s: port %d out of range", field, n)
	}
	return []corev1.ContainerPort{{ContainerPort: int32(n), Protocol: proto}}, nil
}

func numericContainerPortRange(low, high int, proto corev1.Protocol, field string) ([]corev1.ContainerPort, error) {
	if low < 1 || high > 65535 || low > high {
		return nil, fmt.Errorf("%s: port range %d-%d out of range", field, low, high)
	}
	out := make([]corev1.ContainerPort, 0, high-low+1)
	for p := low; p <= high; p++ {
		out = append(out, corev1.ContainerPort{ContainerPort: int32(p), Protocol: proto})
	}
	return out, nil
}

func parsePortRange(s string) (low, high int, err error) {
	if s == "" {
		return 0, 0, fmt.Errorf("empty port")
	}
	lo, hi, hasRange := strings.Cut(s, "-")
	low, err = strconv.Atoi(lo)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid port %q", s)
	}
	if !hasRange {
		return low, low, nil
	}
	high, err = strconv.Atoi(hi)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid port range %q", s)
	}
	return low, high, nil
}
