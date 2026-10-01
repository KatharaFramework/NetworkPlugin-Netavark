package bridge

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vishvananda/netlink/nl"

	"github.com/KatharaFramework/NetworkPlugin-Netavark/internal/attach"
)

const testNetworkID = "963ca160b022aabbccddeeff00112233"

// fakeKernel records the calls and fails the one named in failOn.
type fakeKernel struct {
	calls     []string
	links     map[string]bool
	failOn    string
	portCnt   int
	sysctlErr error
}

func (f *fakeKernel) call(name string, args ...string) error {
	f.calls = append(f.calls, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	if f.failOn == name {
		return errors.New(name + " failed")
	}
	return nil
}

func (f *fakeKernel) linkExists(name string) (bool, error) {
	return f.links[name], f.call("linkExists")
}
func (f *fakeKernel) addBridge(name string) error {
	f.links[name] = true
	return f.call("addBridge", name)
}
func (f *fakeKernel) configureBridge(name string) error { return f.call("configureBridge", name) }
func (f *fakeKernel) writeSysctl(path, value string) error {
	if err := f.call("writeSysctl", filepath.Base(filepath.Dir(path)), value); err != nil {
		return err
	}
	return f.sysctlErr
}
func (f *fakeKernel) setMTU(name string, mtu int) error {
	return f.call("setMTU", name, fmt.Sprint(mtu))
}
func (f *fakeKernel) setUp(name string) error { return f.call("setUp", name) }
func (f *fakeKernel) addVeth(name, peer string) error {
	f.links[name] = true
	return f.call("addVeth", name, peer)
}
func (f *fakeKernel) moveToNetns(name, path string) error { return f.call("moveToNetns", name, path) }
func (f *fakeKernel) setMaster(name, bridge string) error {
	return f.call("setMaster", name, bridge)
}
func (f *fakeKernel) ports(bridge string) (int, error) { return f.portCnt, f.call("ports") }
func (f *fakeKernel) deleteLink(name string) error {
	delete(f.links, name)
	return f.call("deleteLink", name)
}

// fakeEnv installs a fake kernel, a fixed random id and a recorder of the commands run in the container.
func fakeEnv(t *testing.T) (*fakeKernel, *[]string) {
	t.Helper()
	f := &fakeKernel{links: map[string]bool{}}
	var cmds []string
	origKern, origID, origRun, origNetns := kern, randomID, attach.RunInNetns, netnsExists
	t.Cleanup(func() { kern, randomID, attach.RunInNetns, netnsExists = origKern, origID, origRun, origNetns })
	kern = f
	randomID = func() (string, error) { return "0a1b2c3d", nil }
	netnsExists = func(string) bool { return true }
	attach.RunInNetns = func(netns string, args ...string) (string, error) {
		cmd := strings.Join(args, " ")
		cmds = append(cmds, cmd)
		if strings.HasPrefix(cmd, "ip -j link show") {
			return `[{"address":"02:aa:bb:cc:dd:ee"}]`, nil
		}
		if f.failOn == "run:"+cmd {
			return "", errors.New("run failed: " + cmd)
		}
		return "", nil
	}
	return f, &cmds
}

func TestNames(t *testing.T) {
	host, peer, err := vethNames()
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{host, peer} {
		if len(n) > 15 {
			t.Errorf("%q is longer than 15 characters", n)
		}
	}
	if !strings.HasPrefix(host, "ktv") || !strings.HasPrefix(peer, "ktc") || host == peer {
		t.Errorf("unexpected names %q %q", host, peer)
	}
}

func TestRandomVethNamesDiffer(t *testing.T) {
	a, _, _ := vethNames()
	b, _, _ := vethNames()
	if a == b {
		t.Errorf("two calls returned %q", a)
	}
	if len(a) != len("ktv")+8 {
		t.Errorf("unexpected length of %q", a)
	}
}

func TestEnsureDomainCreatesBridgeInOrder(t *testing.T) {
	f, _ := fakeEnv(t)
	name, err := Driver{}.EnsureDomain(testNetworkID)
	if err != nil || name != "kt-963ca160b022" || len(name) != 15 {
		t.Fatalf("EnsureDomain = %q, %v", name, err)
	}
	want := "linkExists|addBridge kt-963ca160b022|configureBridge kt-963ca160b022|" +
		"writeSysctl kt-963ca160b022 1|setMTU kt-963ca160b022 65535|setUp kt-963ca160b022"
	if got := strings.Join(f.calls, "|"); got != want {
		t.Errorf("calls:\n got %s\nwant %s", got, want)
	}

	// An existing bridge is left alone.
	f.calls = nil
	if _, err := (Driver{}).EnsureDomain(testNetworkID); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.calls, "|"); got != "linkExists" {
		t.Errorf("existing bridge: calls %s", got)
	}
}

// A kernel booted with ipv6.disable=1 has no IPv6 sysctls: the bridge must still be created.
func TestEnsureDomainWithoutIPv6(t *testing.T) {
	f, _ := fakeEnv(t)
	f.sysctlErr = fmt.Errorf("open disable_ipv6: %w", fs.ErrNotExist)
	if _, err := (Driver{}).EnsureDomain(testNetworkID); err != nil {
		t.Fatalf("EnsureDomain without IPv6: %v", err)
	}
	if !f.links["kt-963ca160b022"] {
		t.Error("bridge not created")
	}
}

func TestEnsureDomainFailureRemovesBridge(t *testing.T) {
	for _, step := range []string{"configureBridge", "writeSysctl", "setMTU", "setUp"} {
		f, _ := fakeEnv(t)
		f.failOn = step
		if _, err := (Driver{}).EnsureDomain(testNetworkID); err == nil {
			t.Errorf("%s: want an error", step)
		}
		if f.links["kt-963ca160b022"] {
			t.Errorf("%s: bridge left behind", step)
		}
	}
	f, _ := fakeEnv(t)
	f.failOn = "addBridge"
	if _, err := (Driver{}).EnsureDomain(testNetworkID); err == nil {
		t.Error("addBridge: want an error")
	}
}

func TestAttach(t *testing.T) {
	f, cmds := fakeEnv(t)
	iface := attach.Interface{
		NetnsPath: "/run/netns/c1", ContainerID: "c1", Name: "eth1", MAC: "02:00:00:00:00:01",
		Sysctls: map[string]string{"net.ipv4.conf.eth1.rp_filter": "0"},
	}
	mac, err := Driver{}.Attach("kt-963ca160b022", iface)
	if err != nil || mac != "02:aa:bb:cc:dd:ee" {
		t.Fatalf("Attach = %q, %v", mac, err)
	}
	wantKernel := "addVeth ktv0a1b2c3d ktc0a1b2c3d|setMTU ktv0a1b2c3d 65535|" +
		"moveToNetns ktc0a1b2c3d /run/netns/c1|setMaster ktv0a1b2c3d kt-963ca160b022|setUp ktv0a1b2c3d"
	if got := strings.Join(f.calls, "|"); got != wantKernel {
		t.Errorf("kernel calls:\n got %s\nwant %s", got, wantKernel)
	}
	wantCmds := []string{
		"ip link set ktc0a1b2c3d name eth1",
		"ip link set eth1 address 02:00:00:00:00:01",
		"ip link set eth1 up",
		"sysctl -w net.ipv4.conf.eth1.rp_filter=0",
		"ip -j link show dev eth1",
	}
	if got := strings.Join(*cmds, "|"); got != strings.Join(wantCmds, "|") {
		t.Errorf("container commands:\n got %s\nwant %s", got, strings.Join(wantCmds, "|"))
	}
}

// Whatever step fails, the host side of the veth pair (and so the pair) is deleted.
func TestAttachFailureDeletesVeth(t *testing.T) {
	iface := attach.Interface{NetnsPath: "/run/netns/c1", Name: "eth1", MAC: "02:00:00:00:00:01",
		Sysctls: map[string]string{"a": "1"}}
	for _, step := range []string{"setMTU", "moveToNetns", "run:ip link set ktc0a1b2c3d name eth1", "run:ip link set eth1 address 02:00:00:00:00:01",
		"run:ip link set eth1 up", "run:sysctl -w a=1", "setMaster", "setUp"} {
		f, _ := fakeEnv(t)
		f.failOn = step
		if _, err := (Driver{}).Attach("kt-x", iface); err == nil {
			t.Errorf("%s: want an error", step)
		}
		if f.links["ktv0a1b2c3d"] {
			t.Errorf("%s: veth left behind", step)
		}
	}
	f, _ := fakeEnv(t)
	f.failOn = "addVeth"
	if _, err := (Driver{}).Attach("kt-x", iface); err == nil {
		t.Error("addVeth: want an error")
	}
	if contains(f.calls, "deleteLink") {
		t.Error("addVeth failed: nothing to delete")
	}
}

func contains(calls []string, prefix string) bool {
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func TestDetach(t *testing.T) {
	_, cmds := fakeEnv(t)
	if err := (Driver{}).Detach("kt-x", "/run/netns/c1", "c1", "eth1"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(*cmds, "|"); got != "ip link del eth1" {
		t.Errorf("commands: %s", got)
	}

	// A missing interface is not an error.
	attach.RunInNetns = func(string, ...string) (string, error) {
		return "", errors.New(`ip link del eth1: Cannot find device "eth1"`)
	}
	if err := (Driver{}).Detach("kt-x", "/run/netns/c1", "c1", "eth1"); err != nil {
		t.Errorf("missing interface: %v", err)
	}

	// A missing netns is not an error and runs nothing.
	attach.RunInNetns = func(string, ...string) (string, error) { t.Fatal("command run"); return "", nil }
	netnsExists = func(string) bool { return false }
	if err := (Driver{}).Detach("kt-x", "/run/netns/gone", "c1", "eth1"); err != nil {
		t.Errorf("missing netns: %v", err)
	}

	// Other failures are reported.
	netnsExists = func(string) bool { return true }
	attach.RunInNetns = func(string, ...string) (string, error) { return "", errors.New("permission denied") }
	if err := (Driver{}).Detach("kt-x", "/run/netns/c1", "c1", "eth1"); err == nil {
		t.Error("want the error")
	}
}

func TestInUseAndDeleteDomain(t *testing.T) {
	f, _ := fakeEnv(t)
	if (Driver{}).InUse("kt-x") {
		t.Error("InUse with no port")
	}
	f.portCnt = 2
	if !(Driver{}).InUse("kt-x") {
		t.Error("InUse false with ports")
	}
	f.failOn = "ports"
	if (Driver{}).InUse("kt-x") {
		t.Error("InUse true when the bridge cannot be read")
	}

	f, _ = fakeEnv(t)
	if err := (Driver{}).DeleteDomain(testNetworkID); err != nil || contains(f.calls, "deleteLink") {
		t.Errorf("missing bridge: err %v, calls %v", err, f.calls)
	}
	f.links["kt-963ca160b022"] = true
	if err := (Driver{}).DeleteDomain(testNetworkID); err != nil || f.links["kt-963ca160b022"] {
		t.Errorf("DeleteDomain: err %v, links %v", err, f.links)
	}
}

// The bridge must flood like a hub and forward the reserved groups: check the attributes that go on the wire.
func TestBridgeConfigRequest(t *testing.T) {
	wire := bridgeConfigRequest(7).Serialize()
	// nlmsghdr (16 bytes) + ifinfomsg (16 bytes), then the attributes.
	const headers = 32
	if len(wire) <= headers {
		t.Fatalf("short message: %d bytes", len(wire))
	}
	if flags := nativeUint32(wire[headers-8 : headers-4]); flags != 0 {
		t.Errorf("ifi_flags = %#x: the bridge must not be brought up yet", flags)
	}
	if index := nativeUint32(wire[headers-12 : headers-8]); index != 7 {
		t.Errorf("ifi_index = %d", index)
	}

	attrs, err := nl.ParseRouteAttr(wire[headers:])
	if err != nil || len(attrs) != 1 {
		t.Fatalf("attributes: %v, %v", attrs, err)
	}
	info, _ := nl.ParseRouteAttr(attrs[0].Value)
	var data []byte
	for _, a := range info {
		switch a.Attr.Type {
		case nl.IFLA_INFO_KIND:
			if string(a.Value) != "bridge" {
				t.Errorf("kind = %q", a.Value)
			}
		case nl.IFLA_INFO_DATA:
			data = a.Value
		}
	}
	values := map[uint16][]byte{}
	parsed, _ := nl.ParseRouteAttr(data)
	for _, a := range parsed {
		values[a.Attr.Type] = a.Value
	}
	if v := values[nl.IFLA_BR_AGEING_TIME]; len(v) != 4 || nativeUint32(v) != 0 {
		t.Errorf("ageing time = %v, want 0", v)
	}
	if v := values[nl.IFLA_BR_GROUP_FWD_MASK]; len(v) != 2 || nativeUint16(v) != 0xfff8 {
		t.Errorf("group_fwd_mask = %v, want 0xfff8", v)
	}
}

func nativeUint32(b []byte) uint32 { return uint32(nl.NativeEndian().Uint32(b)) }
func nativeUint16(b []byte) uint16 { return nl.NativeEndian().Uint16(b) }

func TestIPv6SysctlPath(t *testing.T) {
	if got := ipv6SysctlPath("/proc/sys", "kt-963ca160b022"); got != "/proc/sys/net/ipv6/conf/kt-963ca160b022/disable_ipv6" {
		t.Error(got)
	}
	dir := t.TempDir()
	path := ipv6SysctlPath(dir, "kt-x")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("0"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := (netlinkKernel{}).writeSysctl(path, "1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "1" {
		t.Errorf("sysctl = %q", got)
	}
}
