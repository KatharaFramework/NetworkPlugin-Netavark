// Package bridge is the Linux bridge driver of the plugin: a collision domain is a bridge created in the
// network namespace the plugin runs in (the rootless network namespace of Podman), and a container
// interface is one end of a veth pair whose other end is enslaved to the bridge. The data plane is the
// kernel; there is no process to keep alive and no state to keep: a bridge is created on demand, and it
// disappears with the rootless namespace when the last container stops.
//
// The bridge is configured as the one of the Kathará Docker plugin: ageing time 0 (it floods every frame
// like a hub) and group_fwd_mask 0xfff8 (it forwards every reserved group address the kernel allows,
// e.g. LLDP). Known limit: the kernel never forwards 01:80:c2:00:00:00-02, so LACP cannot cross a Linux
// bridge; use the VDE driver for that.
//
// This package imports neither cgo nor VDE, and needs no iptables/nftables rule.
package bridge

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"github.com/KatharaFramework/NetworkPlugin-Netavark/internal/attach"
	"github.com/KatharaFramework/NetworkPlugin-Netavark/internal/netavark"
)

const (
	// hostMTU is the MTU of the bridge and of the host side of each veth: the maximum, so that a container
	// can raise its own MTU for jumbo frames (a veth drops the frames larger than the MTU of its peer).
	// The container side keeps the default MTU.
	hostMTU = 65535
	// hostVethPrefix and peerVethPrefix start the random names of the two ends of a veth pair; with
	// 8 hex characters they are 11 characters long, under the 15 of the kernel limit.
	hostVethPrefix = "ktv"
	peerVethPrefix = "ktc"
	// procSys is where the sysctls of the plugin namespace are.
	procSys = "/proc/sys"
)

// Indirections replaced in tests.
var (
	kern     kernel = netlinkKernel{}
	randomID        = func() (string, error) {
		b := make([]byte, 4)
		if _, err := rand.Read(b); err != nil {
			return "", err
		}
		return hex.EncodeToString(b), nil
	}
	netnsExists = func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	}
)

// Driver implements plugin.Driver with Linux bridges.
type Driver struct{}

// EnsureDomain creates the bridge of a network if it does not exist.
//
// Setup and Teardown are never concurrent for the same user: libpod serializes every netavark
// setup/teardown behind a per-user file lock (netavark.lock in the network config dir), so the
// check-then-create here needs no locking.
func (Driver) EnsureDomain(networkID string) (string, error) {
	name := netavark.DomainName(networkID)
	exists, err := kern.linkExists(name)
	if err != nil {
		return "", err
	}
	if exists {
		return name, nil
	}
	if err := createBridge(name); err != nil {
		return "", fmt.Errorf("cannot create bridge %s: %w", name, err)
	}
	return name, nil
}

// createBridge creates a bridge with the Kathará settings. IPv6 is disabled before the bridge goes up, so
// that it never gets a link-local address: the segment must carry no traffic of its own.
func createBridge(name string) (err error) {
	if err = kern.addBridge(name); err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = kern.deleteLink(name)
		}
	}()

	if err = kern.configureBridge(name); err != nil {
		return err
	}
	// Without IPv6 in the kernel (ipv6.disable=1) the sysctl does not exist and there is no address to remove.
	if err = kern.writeSysctl(ipv6SysctlPath(procSys, name), "1"); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("cannot disable IPv6: %w", err)
	}
	if err = kern.setMTU(name, hostMTU); err != nil {
		return err
	}
	return kern.setUp(name)
}

// Attach creates a veth pair, moves one end into the container as iface.Name and enslaves the other.
// On error, nothing is left behind.
func (Driver) Attach(domain string, iface attach.Interface) (mac string, err error) {
	host, peer, err := vethNames()
	if err != nil {
		return "", err
	}

	if err = kern.addVeth(host, peer); err != nil {
		return "", fmt.Errorf("cannot create veth pair for %s: %w", iface.Name, err)
	}
	// Deleting the host side removes the pair, wherever the peer is.
	defer func() {
		if err != nil {
			_ = kern.deleteLink(host)
		}
	}()

	if err = kern.setMTU(host, hostMTU); err != nil {
		return "", err
	}
	if err = kern.moveToNetns(peer, iface.NetnsPath); err != nil {
		return "", fmt.Errorf("cannot move %s into the container: %w", iface.Name, err)
	}

	if _, err = attach.RunInNetns(iface.NetnsPath, "ip", "link", "set", peer, "name", iface.Name); err != nil {
		return "", fmt.Errorf("cannot rename %s to %s: %w", peer, iface.Name, err)
	}
	if err = attach.SetMAC(iface.NetnsPath, iface.Name, iface.MAC); err != nil {
		return "", err
	}
	if err = attach.SetUp(iface.NetnsPath, iface.Name); err != nil {
		return "", err
	}
	if err = attach.ApplySysctls(iface.NetnsPath, iface.Sysctls); err != nil {
		return "", err
	}

	if err = kern.setMaster(host, domain); err != nil {
		return "", fmt.Errorf("cannot connect %s to %s: %w", iface.Name, domain, err)
	}
	if err = kern.setUp(host); err != nil {
		return "", err
	}
	return attach.ReadMAC(iface.NetnsPath, iface.Name)
}

// Detach deletes the interface inside the container: deleting one end removes the pair. A container
// namespace or an interface that is already gone is not an error.
func (Driver) Detach(domain string, netnsPath, containerID, ifname string) error {
	if !netnsExists(netnsPath) {
		return nil
	}
	if _, err := attach.RunInNetns(netnsPath, "ip", "link", "del", ifname); err != nil {
		if strings.Contains(err.Error(), "Cannot find device") {
			return nil
		}
		return fmt.Errorf("cannot delete %s: %w", ifname, err)
	}
	return nil
}

// InUse reports whether any link has the bridge as master.
func (Driver) InUse(domain string) bool {
	count, err := kern.ports(domain)
	return err == nil && count > 0
}

// DeleteDomain deletes the bridge if it exists.
func (Driver) DeleteDomain(networkID string) error {
	name := netavark.DomainName(networkID)
	exists, err := kern.linkExists(name)
	if err != nil || !exists {
		return err
	}
	return kern.deleteLink(name)
}

// vethNames returns random names for the host and the container side of a new veth pair.
func vethNames() (host string, peer string, err error) {
	id, err := randomID()
	if err != nil {
		return "", "", errors.New("cannot generate an interface name: " + err.Error())
	}
	return hostVethPrefix + id, peerVethPrefix + id, nil
}
