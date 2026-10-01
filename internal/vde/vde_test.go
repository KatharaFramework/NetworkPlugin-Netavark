package vde

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/KatharaFramework/NetworkPlugin-Netavark/internal/netavark"
)

const testNetworkID = "963ca160b022aabbccddeeff00112233"

// fakeSwitches replaces the process-starting operations with fakes that only touch the state directory
// (a temporary XDG_RUNTIME_DIR). It returns a counter of deleteSwitch calls.
func fakeSwitches(t *testing.T) *int {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	deleted := 0
	origCreate, origDelete := createSwitch, deleteSwitch
	t.Cleanup(func() { createSwitch, deleteSwitch = origCreate, origDelete })

	createSwitch = func(networkID string) (string, error) {
		name := netavark.DomainName(networkID)
		if err := os.MkdirAll(stateDir()+name, 0o700); err != nil {
			return "", err
		}
		// switchRunning only checks that the pidfile exists.
		return name, os.WriteFile(stateDir()+name+"/pid", []byte("1\n"), 0o600)
	}
	deleteSwitch = func(networkID string) error {
		deleted++
		return os.RemoveAll(stateDir() + netavark.DomainName(networkID))
	}
	return &deleted
}

func TestDomainLifecycle(t *testing.T) {
	deleted := fakeSwitches(t)
	d := Driver{}

	name, err := d.EnsureDomain(testNetworkID)
	if err != nil || name != "kt-963ca160b022" {
		t.Fatalf("EnsureDomain = %q, %v", name, err)
	}
	if !switchRunning(name) {
		t.Fatal("switch not running after EnsureDomain")
	}
	if d.InUse(name) {
		t.Error("InUse with no interface")
	}

	// Another interface kept alive by a live process: the test process itself.
	if err := os.MkdirAll(handleDir(name), 0o700); err != nil {
		t.Fatal(err)
	}
	pidFile := pidFilePath(handleDir(name), handleID("c0", "eth0"))
	if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	if !d.InUse(name) {
		t.Error("InUse false with a live interface")
	}

	// Detach of a dead handle is fine; the live one is not ours to stop here.
	if err := d.Detach(name, "", "nobody", "eth9"); err != nil {
		t.Errorf("Detach of a missing handle: %v", err)
	}
	if filepath.Base(pidFile) != "c0-eth0.pid" {
		t.Errorf("unexpected handle name %s", pidFile)
	}

	if err := d.DeleteDomain(testNetworkID); err != nil || *deleted != 1 {
		t.Errorf("DeleteDomain: err %v, deleted %d", err, *deleted)
	}
	// A second delete finds no switch and does nothing.
	if err := d.DeleteDomain(testNetworkID); err != nil || *deleted != 1 {
		t.Errorf("second DeleteDomain: err %v, deleted %d", err, *deleted)
	}
}
