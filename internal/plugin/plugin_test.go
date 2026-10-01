package plugin

import (
	"errors"
	"testing"

	"github.com/KatharaFramework/NetworkPlugin-Netavark/internal/attach"
	"github.com/KatharaFramework/NetworkPlugin-Netavark/internal/netavark"
)

const testNetworkID = "963ca160b022aabbccddeeff00112233"

// fakeDriver keeps the set of attached interfaces and whether the domain exists in memory.
type fakeDriver struct {
	exists    bool
	attached  map[string]bool
	attachErr error
	created   int
	deleted   int
}

func newFakeDriver() *fakeDriver {
	return &fakeDriver{attached: map[string]bool{}}
}

func (f *fakeDriver) EnsureDomain(networkID string) (string, error) {
	if !f.exists {
		f.exists = true
		f.created++
	}
	return netavark.DomainName(networkID), nil
}

func (f *fakeDriver) Attach(domain string, iface attach.Interface) (string, error) {
	if f.attachErr != nil {
		return "", f.attachErr
	}
	f.attached[iface.ContainerID+"-"+iface.Name] = true
	return "aa:bb:cc:dd:ee:ff", nil
}

func (f *fakeDriver) Detach(domain string, netnsPath, containerID, ifname string) error {
	delete(f.attached, containerID+"-"+ifname)
	return nil
}

func (f *fakeDriver) InUse(domain string) bool { return len(f.attached) > 0 }

func (f *fakeDriver) DeleteDomain(networkID string) error {
	if f.exists {
		f.exists = false
		f.deleted++
	}
	return nil
}

func payload(containerID string, ifname string) netavark.PluginExec {
	return netavark.PluginExec{
		ContainerID:    containerID,
		Network:        netavark.Network{ID: testNetworkID},
		NetworkOptions: netavark.PerNetworkOptions{InterfaceName: ifname},
	}
}

// A failed attach of the first interface must not leave the domain it created behind.
func TestSetupFirstInterfaceFailureDeletesDomain(t *testing.T) {
	driver := newFakeDriver()
	driver.attachErr = errors.New("attach failed")

	if _, err := (Plugin{driver}).Setup("/proc/self/ns/net", payload("c1", "eth0")); err == nil {
		t.Fatal("Setup succeeded, want the attach error")
	}
	if driver.deleted != 1 || driver.exists {
		t.Errorf("deleted %d times, exists %v, want the domain removed once", driver.deleted, driver.exists)
	}
}

// A failed attach must not remove a domain that still has other interfaces attached.
func TestSetupFailureKeepsDomainInUse(t *testing.T) {
	driver := newFakeDriver()
	p := Plugin{driver}
	if _, err := p.Setup("/proc/self/ns/net", payload("c0", "eth0")); err != nil {
		t.Fatal(err)
	}

	driver.attachErr = errors.New("attach failed")
	if _, err := p.Setup("/proc/self/ns/net", payload("c1", "eth1")); err == nil {
		t.Fatal("Setup succeeded, want the attach error")
	}
	if driver.deleted != 0 || !driver.exists {
		t.Errorf("deleted %d times, exists %v, want the domain kept", driver.deleted, driver.exists)
	}
}

// The domain lives as long as one interface is attached, and the status block reports the MAC.
func TestDomainLifecycle(t *testing.T) {
	driver := newFakeDriver()
	p := Plugin{driver}

	status, err := p.Setup("/proc/self/ns/net", payload("c1", "eth0"))
	if err != nil {
		t.Fatal(err)
	}
	if got := status.Interfaces["eth0"].MACAddress; got != "aa:bb:cc:dd:ee:ff" {
		t.Errorf("MAC = %q", got)
	}
	if _, err := p.Setup("/proc/self/ns/net", payload("c2", "eth0")); err != nil {
		t.Fatal(err)
	}
	if driver.created != 1 {
		t.Errorf("domain created %d times, want 1", driver.created)
	}

	if err := p.Teardown("/proc/self/ns/net", payload("c1", "eth0")); err != nil {
		t.Fatal(err)
	}
	if !driver.exists {
		t.Error("domain removed while c2 is still attached")
	}
	if err := p.Teardown("/proc/self/ns/net", payload("c2", "eth0")); err != nil {
		t.Fatal(err)
	}
	if driver.exists {
		t.Error("domain kept after the last interface was detached")
	}
}

func TestSetupRequiresInterfaceName(t *testing.T) {
	if _, err := (Plugin{newFakeDriver()}).Setup("/x", payload("c1", "")); err == nil {
		t.Fatal("want an error for an empty interface name")
	}
}

func TestSysctls(t *testing.T) {
	got := sysctls(
		map[string]string{"sysctl.net.ipv4.conf.IFNAME.rp_filter": "0", "sysctl.a": "1"},
		map[string]string{"sysctl.a": "2", "other": "x"},
		"eth3")
	want := map[string]string{"net.ipv4.conf.eth3.rp_filter": "0", "a": "2"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, want %q", k, got[k], v)
		}
	}
}

func TestValidate(t *testing.T) {
	if err := Validate(netavark.Network{Options: map[string]string{"sysctl.a": "1"}}); err != nil {
		t.Error(err)
	}
	if err := Validate(netavark.Network{Options: map[string]string{"mtu": "1"}}); err == nil {
		t.Error("want an error for an unsupported option")
	}
}
