// Package vde is the VDE driver of the plugin: a collision domain is a VDE switch (created with the
// Kathará NetworkPlugin library), started when the first interface is attached and stopped when the
// last one is detached: netavark has no "network removed" hook. Each container interface is a tap
// created by vde_plug2tap (see attach.go).
//
// This is the only package importing the cgo NetworkPluginLib.
package vde

import (
	"os"
	"path/filepath"

	katnplib "github.com/KatharaFramework/NetworkPluginLib"

	"github.com/KatharaFramework/NetworkPlugin-Netavark/internal/attach"
	"github.com/KatharaFramework/NetworkPlugin-Netavark/internal/netavark"
)

// Indirections over the operations that start or stop real processes, replaced in tests.
var (
	createSwitch = katnplib.CreateSwitch
	deleteSwitch = katnplib.DeleteSwitch
	attachTap    = attachIface
)

// stateDir is where switches and interface handles are kept: per user, cleaned at logout.
func stateDir() string {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "katharanp") + "/"
}

func init() {
	// Requires the upstream change making pluginPath configurable (default: /hosttmp/katharanp/).
	katnplib.SetPluginPath(stateDir())
}

// Driver implements plugin.Driver with VDE switches.
type Driver struct{}

// EnsureDomain starts the switch of a network if it is not running.
//
// Setup and Teardown are never concurrent for the same user: libpod serializes every netavark
// setup/teardown behind a per-user file lock (netavark.lock in the network config dir), so the
// check-then-create here and the count-then-delete in the plugin need no locking.
func (Driver) EnsureDomain(networkID string) (string, error) {
	name := netavark.DomainName(networkID)
	if switchRunning(name) {
		return name, nil
	}
	return createSwitch(networkID)
}

// Attach starts vde_plug2tap in the container namespace, connected to the switch.
func (Driver) Attach(domain string, iface attach.Interface) (string, error) {
	return attachTap(ifaceRef{
		iface:     iface,
		switchCtl: switchCtlPath(domain),
		handleDir: handleDir(domain),
		handleID:  handleID(iface.ContainerID, iface.Name),
	})
}

// Detach stops the vde_plug2tap process of the interface.
func (Driver) Detach(domain string, netnsPath, containerID, ifname string) error {
	return detachIface(handleDir(domain), handleID(containerID, ifname))
}

// InUse reports whether an interface of the switch is still alive.
func (Driver) InUse(domain string) bool {
	return countIfaces(handleDir(domain)) > 0
}

// DeleteDomain stops the switch if it is running.
func (Driver) DeleteDomain(networkID string) error {
	if !switchRunning(netavark.DomainName(networkID)) {
		return nil
	}
	return deleteSwitch(networkID)
}

// The helpers below mirror the (unexported) naming of katnplib's switch_utils.go.

func switchCtlPath(switchName string) string {
	return stateDir() + switchName + "/ctl"
}

func handleDir(switchName string) string {
	return stateDir() + switchName + "/ifaces"
}

func handleID(containerID string, ifname string) string {
	return containerID + "-" + ifname
}

func switchRunning(switchName string) bool {
	_, err := os.Stat(stateDir() + switchName + "/pid")
	return err == nil
}
