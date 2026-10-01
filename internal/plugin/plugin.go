// Package plugin implements the operations of the Kathará netavark plugin that do not depend on the data
// plane: validating networks, computing sysctls, and deciding when a collision domain is created and
// removed. The data plane itself (a VDE switch, a Linux bridge) sits behind the Driver interface.
//
// A collision domain is created when the first interface is attached and removed when the last one is
// detached: netavark has no "network removed" hook.
package plugin

import (
	"fmt"
	"strings"

	"github.com/KatharaFramework/NetworkPlugin-Netavark/internal/attach"
	"github.com/KatharaFramework/NetworkPlugin-Netavark/internal/netavark"
)

// SysctlOptionPrefix marks per-interface sysctls in network and per-connection options.
// IFNAME in the key is replaced with the interface name (same convention as Docker endpoint sysctls).
const SysctlOptionPrefix = "sysctl."

// Driver is the data plane of a collision domain. A domain is named after its network
// (netavark.DomainName); drivers must tolerate being called for a domain that does not exist.
type Driver interface {
	// EnsureDomain creates the switch/bridge of a network if it is missing and returns its name.
	EnsureDomain(networkID string) (domain string, err error)
	// Attach creates the interface in the container and connects it to the domain; it returns the MAC
	// address of the interface. On error, nothing is left behind.
	Attach(domain string, iface attach.Interface) (mac string, err error)
	// Detach removes a container interface from the domain. An interface that is already gone is not an error.
	Detach(domain string, netnsPath, containerID, ifname string) error
	// InUse reports whether any interface is still attached to the domain.
	InUse(domain string) bool
	// DeleteDomain removes the domain of a network, if it exists.
	DeleteDomain(networkID string) error
}

// Plugin runs the netavark operations on top of a Driver.
type Plugin struct {
	Driver Driver
}

// Validate checks the options of a network definition at `create` time.
func Validate(network netavark.Network) error {
	for key := range network.Options {
		if !strings.HasPrefix(key, SysctlOptionPrefix) {
			return fmt.Errorf("unsupported network option %q", key)
		}
	}
	return nil
}

// Setup attaches a container interface to the collision domain of its network.
func (p Plugin) Setup(netnsPath string, payload netavark.PluginExec) (*netavark.StatusBlock, error) {
	ifname := payload.NetworkOptions.InterfaceName
	if ifname == "" {
		return nil, fmt.Errorf("interface_name is required")
	}

	domain, err := p.Driver.EnsureDomain(payload.Network.ID)
	if err != nil {
		return nil, fmt.Errorf("cannot create collision domain: %w", err)
	}

	iface := attach.Interface{
		NetnsPath:   netnsPath,
		ContainerID: payload.ContainerID,
		Name:        ifname,
		MAC:         payload.NetworkOptions.StaticMAC,
		Sysctls:     sysctls(payload.Network.Options, payload.NetworkOptions.Options, ifname),
	}

	mac, err := p.Driver.Attach(domain, iface)
	if err != nil {
		p.deleteDomainIfUnused(payload.Network.ID, domain)
		return nil, err
	}

	return &netavark.StatusBlock{
		DNSSearchDomains: []string{},
		DNSServerIPs:     []string{},
		Interfaces: map[string]netavark.NetInterface{
			ifname: {MACAddress: mac, Subnets: []interface{}{}},
		},
	}, nil
}

// Teardown detaches a container interface and removes the domain if it has no interfaces left.
func (p Plugin) Teardown(netnsPath string, payload netavark.PluginExec) error {
	domain := netavark.DomainName(payload.Network.ID)
	err := p.Driver.Detach(domain, netnsPath, payload.ContainerID, payload.NetworkOptions.InterfaceName)
	p.deleteDomainIfUnused(payload.Network.ID, domain)
	return err
}

// Setup and Teardown are never concurrent for the same user: libpod serializes every netavark
// setup/teardown behind a per-user file lock (netavark.lock in the network config dir), so the
// check-then-delete below needs no locking.
func (p Plugin) deleteDomainIfUnused(networkID string, domain string) {
	if !p.Driver.InUse(domain) {
		_ = p.Driver.DeleteDomain(networkID)
	}
}

// sysctls merges network-level and per-connection sysctl options (the latter take precedence),
// strips the prefix and replaces the IFNAME placeholder.
func sysctls(networkOpts, connectionOpts map[string]string, ifname string) map[string]string {
	result := map[string]string{}
	for _, source := range []map[string]string{networkOpts, connectionOpts} {
		for key, value := range source {
			if strings.HasPrefix(key, SysctlOptionPrefix) {
				key = strings.ReplaceAll(strings.TrimPrefix(key, SysctlOptionPrefix), "IFNAME", ifname)
				result[key] = value
			}
		}
	}
	return result
}
