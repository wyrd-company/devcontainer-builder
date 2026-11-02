// relationships:
//   references: [runargs, spawn, up]
//   implements: schema-conformance

package docker

import (
	"fmt"
	"net"
	"net/netip"
	"reflect"
	"strings"

	dockeropts "github.com/docker/cli/opts"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
)

// applyNetworks follows Docker CLI parseNetworkOpts: legacy endpoint options
// belong to the first network; repeated --network values are attachments.
func (p *runArgsParser) applyNetworks(cfg *container.Config, host *container.HostConfig) (*network.NetworkingConfig, error) {
	host.NetworkMode = container.NetworkMode(p.networks.NetworkMode())
	attachments := p.networks.Value()
	implicit := len(attachments) == 0
	if implicit {
		attachments = []dockeropts.NetworkAttachmentOpts{{Target: "default"}}
	}
	endpoints := map[string]*network.EndpointSettings{}
	user, builtin := false, false
	for i, n := range attachments {
		isUser := container.NetworkMode(n.Target).IsUserDefined()
		user = user || isUser
		builtin = builtin || !isUser
		if i == 0 {
			if len(n.Aliases) > 0 && len(p.aliases) > 0 || len(n.Links) > 0 && len(host.Links) > 0 || n.IPv4Address.IsValid() && p.ip != nil || n.IPv6Address.IsValid() && p.ip6 != nil || n.MacAddress != "" && cfg.MacAddress != "" || len(n.LinkLocalIPs) > 0 && len(p.linkLocal) > 0 {
				return nil, fmt.Errorf("conflicting legacy and per-network endpoint options")
			}
			n.Aliases = append(n.Aliases, p.aliases...)
			if isUser {
				n.Links = append(n.Links, host.Links...)
			}
			if p.ip != nil {
				n.IPv4Address, _ = netip.AddrFromSlice(p.ip.To4())
			}
			if p.ip6 != nil {
				n.IPv6Address, _ = netip.AddrFromSlice(p.ip6.To16())
			}
			if cfg.MacAddress != "" {
				n.MacAddress = cfg.MacAddress
			}
			for _, value := range p.linkLocal {
				// Docker's legacy list silently omits values that netip cannot parse.
				if ip, err := netip.ParseAddr(value); err == nil {
					n.LinkLocalIPs = append(n.LinkLocalIPs, ip)
				}
			}
		}
		if strings.TrimSpace(n.Target) == "" {
			return nil, fmt.Errorf("no name set for network")
		}
		if !isUser && (len(n.Aliases) > 0 || len(n.Links) > 0) {
			return nil, fmt.Errorf("network-scoped aliases and links require a user-defined network")
		}
		ep := &network.EndpointSettings{Aliases: append([]string(nil), n.Aliases...), Links: n.Links, DriverOpts: n.DriverOpts, GwPriority: n.GwPriority}
		if n.IPv4Address.IsValid() || n.IPv6Address.IsValid() || len(n.LinkLocalIPs) > 0 {
			ep.IPAMConfig = &network.EndpointIPAMConfig{}
			if n.IPv4Address.IsValid() {
				ep.IPAMConfig.IPv4Address = n.IPv4Address.String()
			}
			if n.IPv6Address.IsValid() {
				ep.IPAMConfig.IPv6Address = n.IPv6Address.String()
			}
			for _, ip := range n.LinkLocalIPs {
				ep.IPAMConfig.LinkLocalIPs = append(ep.IPAMConfig.LinkLocalIPs, ip.String())
			}
		}
		if n.MacAddress != "" {
			mac, err := net.ParseMAC(strings.TrimSpace(n.MacAddress))
			if err != nil {
				return nil, err
			}
			ep.MacAddress = mac.String()
		}
		if _, exists := endpoints[n.Target]; exists {
			return nil, fmt.Errorf("network %q is specified multiple times", n.Target)
		}
		if !implicit && len(attachments) == 1 && reflect.ValueOf(*ep).IsZero() {
			continue
		}
		endpoints[n.Target] = ep
	}
	if user && builtin {
		return nil, fmt.Errorf("cannot attach both user-defined and non-user-defined network modes")
	}
	cfg.MacAddress = ""
	return &network.NetworkingConfig{EndpointsConfig: endpoints}, nil
}
