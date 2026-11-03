// relationships:
//
//	references: [dockerfile, gateway, connect]
package buildkit

import (
	"context"
	"fmt"
	"net"
	"strings"

	"github.com/moby/buildkit/client"
)

// formatExtraHosts mirrors buildx's host syntax without normalizing authored IPs.
// Gateway lookup is deferred until the selected worker connection is available.
func formatExtraHosts(entries []string, gateway func() (net.IP, error)) (string, error) {
	var hosts []string
	for _, entry := range entries {
		host, addresses, ok := strings.Cut(entry, "=")
		if !ok {
			host, addresses, ok = strings.Cut(entry, ":")
		}
		if !ok || host == "" || addresses == "" {
			return "", fmt.Errorf("build.options: invalid host %s", entry)
		}
		if addresses == "host-gateway" {
			ip, err := gateway()
			if err != nil {
				return "", fmt.Errorf("unable to derive the IP value for host-gateway: %w", err)
			}
			hosts = append(hosts, host+"="+ip.String())
			continue
		}
		for _, ip := range strings.Split(addresses, ",") {
			if len(ip) > 2 && ip[0] == '[' && ip[len(ip)-1] == ']' {
				ip = ip[1 : len(ip)-1]
			}
			if net.ParseIP(ip) == nil {
				return "", fmt.Errorf("build.options: invalid host %s", entry)
			}
			hosts = append(hosts, host+"="+ip)
		}
	}
	return strings.Join(hosts, ","), nil
}

func resolveExtraHosts(ctx context.Context, entries []string, conn *Connection) (string, error) {
	return formatExtraHosts(entries, func() (net.IP, error) {
		if !conn.Daemon {
			return nil, fmt.Errorf("host-gateway is not supported by the remote driver")
		}
		workers, err := conn.BuildKit.ListWorkers(ctx)
		if err != nil {
			return nil, fmt.Errorf("listing workers: %w", err)
		}
		return workerHostGateway(workers)
	})
}

func workerHostGateway(workers []*client.WorkerInfo) (net.IP, error) {
	for _, worker := range workers {
		if value := worker.Labels["org.mobyproject.buildkit.worker.moby.host-gateway-ip"]; value != "" {
			ip := net.ParseIP(value)
			if ip == nil {
				return nil, fmt.Errorf("failed to parse host-gateway IP: %s", value)
			}
			return ip, nil
		}
	}
	return nil, fmt.Errorf("host-gateway IP not found")
}
