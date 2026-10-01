package bridge

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"
	"golang.org/x/sys/unix"
)

const (
	// bridgeAgeingTime is the MAC learning time of the bridge in centiseconds: 0 makes it forget every
	// address at once, so it floods like a hub (same as the Kathará Docker plugin).
	bridgeAgeingTime uint32 = 0
	// bridgeGroupFwdMask lets the bridge forward every reserved group address (01:80:c2:00:00:0X) the
	// kernel allows, e.g. LLDP. The kernel never forwards 01:80:c2:00:00:00-02 (STP, pause, LACP): they
	// cannot be enabled in the mask, which is why LACP cannot cross a Linux bridge.
	bridgeGroupFwdMask uint16 = 0xfff8
)

// kernel is the part of the Linux networking stack the driver uses, so that the driver logic can be
// tested without privileges. Links are designated by name, in the network namespace of the plugin.
type kernel interface {
	linkExists(name string) (bool, error)
	addBridge(name string) error
	// configureBridge sets ageing time and group_fwd_mask, without bringing the bridge up.
	configureBridge(name string) error
	writeSysctl(path string, value string) error
	setMTU(name string, mtu int) error
	setUp(name string) error
	addVeth(name string, peer string) error
	moveToNetns(name string, netnsPath string) error
	setMaster(name string, bridge string) error
	// ports counts the links that have the bridge as master.
	ports(bridge string) (int, error)
	deleteLink(name string) error
}

// netlinkKernel implements kernel with netlink and /proc/sys.
type netlinkKernel struct{}

func (netlinkKernel) linkExists(name string) (bool, error) {
	_, err := netlink.LinkByName(name)
	var notFound netlink.LinkNotFoundError
	if errors.As(err, &notFound) {
		return false, nil
	}
	return err == nil, err
}

func (netlinkKernel) addBridge(name string) error {
	return netlink.LinkAdd(&netlink.Bridge{LinkAttrs: netlink.LinkAttrs{Name: name}})
}

func (netlinkKernel) configureBridge(name string) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return err
	}
	_, err = bridgeConfigRequest(link.Attrs().Index).Execute(unix.NETLINK_ROUTE, 0)
	return err
}

// bridgeConfigRequest builds the RTM_NEWLINK request that patches ageing_time and group_fwd_mask
// (the bridge attributes the netlink library does not expose), like patchBridge in the Docker plugin.
// Unlike it, the bridge is not brought up: its IPv6 must be disabled first.
func bridgeConfigRequest(index int) *nl.NetlinkRequest {
	req := nl.NewNetlinkRequest(unix.RTM_NEWLINK, unix.NLM_F_ACK)

	msg := nl.NewIfInfomsg(unix.AF_UNSPEC)
	msg.Index = int32(index)
	req.AddData(msg)

	linkInfo := nl.NewRtAttr(unix.IFLA_LINKINFO, nil)
	linkInfo.AddRtAttr(nl.IFLA_INFO_KIND, nl.NonZeroTerminated("bridge"))
	data := linkInfo.AddRtAttr(nl.IFLA_INFO_DATA, nil)
	data.AddRtAttr(nl.IFLA_BR_AGEING_TIME, nl.Uint32Attr(bridgeAgeingTime))
	data.AddRtAttr(nl.IFLA_BR_GROUP_FWD_MASK, nl.Uint16Attr(bridgeGroupFwdMask))
	req.AddData(linkInfo)
	return req
}

func (netlinkKernel) writeSysctl(path string, value string) error {
	return os.WriteFile(path, []byte(value), 0)
}

func (netlinkKernel) setMTU(name string, mtu int) error {
	return withLink(name, func(link netlink.Link) error { return netlink.LinkSetMTU(link, mtu) })
}

func (netlinkKernel) setUp(name string) error {
	return withLink(name, netlink.LinkSetUp)
}

func (netlinkKernel) addVeth(name string, peer string) error {
	return netlink.LinkAdd(&netlink.Veth{LinkAttrs: netlink.LinkAttrs{Name: name}, PeerName: peer})
}

func (netlinkKernel) moveToNetns(name string, netnsPath string) error {
	netns, err := os.Open(netnsPath)
	if err != nil {
		return err
	}
	defer netns.Close()
	return withLink(name, func(link netlink.Link) error { return netlink.LinkSetNsFd(link, int(netns.Fd())) })
}

func (netlinkKernel) setMaster(name string, bridge string) error {
	master, err := netlink.LinkByName(bridge)
	if err != nil {
		return err
	}
	return withLink(name, func(link netlink.Link) error { return netlink.LinkSetMaster(link, master) })
}

func (netlinkKernel) ports(bridge string) (int, error) {
	master, err := netlink.LinkByName(bridge)
	if err != nil {
		return 0, err
	}
	links, err := netlink.LinkList()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, link := range links {
		if link.Attrs().MasterIndex == master.Attrs().Index {
			count++
		}
	}
	return count, nil
}

func (netlinkKernel) deleteLink(name string) error {
	return withLink(name, netlink.LinkDel)
}

func withLink(name string, fn func(netlink.Link) error) error {
	link, err := netlink.LinkByName(name)
	if err != nil {
		return fmt.Errorf("%s: %w", name, err)
	}
	return fn(link)
}

// ipv6SysctlPath is the sysctl that disables IPv6 (and so the link-local address) on an interface.
func ipv6SysctlPath(root string, name string) string {
	return filepath.Join(root, "net/ipv6/conf", name, "disable_ipv6")
}
