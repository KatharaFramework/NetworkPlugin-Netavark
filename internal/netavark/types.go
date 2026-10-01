// Package netavark defines the JSON types of the netavark plugin protocol.
//
// See https://github.com/containers/netavark/blob/main/plugin-API.md
package netavark

// APIVersion is the netavark plugin API version implemented by this plugin.
const APIVersion = "1.0.0"

// Network is the network definition netavark passes to `create`, `setup` and `teardown`.
// Only the fields the plugin needs are decoded; `create` echoes the raw JSON back instead.
type Network struct {
	Name             string            `json:"name"`
	ID               string            `json:"id"`
	Driver           string            `json:"driver"`
	NetworkInterface string            `json:"network_interface,omitempty"`
	Options          map[string]string `json:"options,omitempty"`
}

// PerNetworkOptions are the per-connection options (interface name, MAC, plugin options).
type PerNetworkOptions struct {
	InterfaceName string            `json:"interface_name"`
	StaticMAC     string            `json:"static_mac,omitempty"`
	Options       map[string]string `json:"options,omitempty"`
}

// PluginExec is the stdin payload of `setup` and `teardown`.
type PluginExec struct {
	ContainerID    string            `json:"container_id"`
	ContainerName  string            `json:"container_name"`
	Network        Network           `json:"network"`
	NetworkOptions PerNetworkOptions `json:"network_options"`
}

// NetInterface describes an interface created by `setup`.
type NetInterface struct {
	MACAddress string        `json:"mac_address"`
	Subnets    []interface{} `json:"subnets"`
}

// StatusBlock is the stdout payload of `setup`.
type StatusBlock struct {
	DNSSearchDomains []string                `json:"dns_search_domains"`
	DNSServerIPs     []string                `json:"dns_server_ips"`
	Interfaces       map[string]NetInterface `json:"interfaces"`
}

// Info is the stdout payload of `info`.
type Info struct {
	Version    string `json:"version"`
	APIVersion string `json:"api_version"`
}

// Error is written to stdout, with a non-zero exit code, when an operation fails.
type Error struct {
	Error string `json:"error"`
}

// DomainName returns the name of the data plane object (VDE switch or Linux bridge) of a network:
// "kt-" plus the first 12 characters of the network ID, 15 characters in total, the kernel limit for
// interface names.
func DomainName(networkID string) string {
	if len(networkID) > 12 {
		networkID = networkID[:12]
	}
	return "kt-" + networkID
}
