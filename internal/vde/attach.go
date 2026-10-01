package vde

// This file keeps container interfaces attached to a VDE switch.
//
// netavark runs the plugin once per operation and the plugin exits right after, so an interface cannot be
// held by a thread as in the Docker plugin: each interface is kept alive by its own long-running process.
// Everything about how that is done stays behind attachIface/detachIface/countIfaces, so that the mechanism
// (vde_plug2tap today; vde_ext, a per-user daemon or anything else later) can change without touching
// the rest of the driver.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/KatharaFramework/NetworkPlugin-Netavark/internal/attach"
)

// carrierPause is the pause after each carrier change.
const carrierPause = 50 * time.Millisecond

// ifaceRef locates an interface of a switch: where its control socket is and where its handle is kept.
type ifaceRef struct {
	iface     attach.Interface
	switchCtl string // control directory of the VDE switch
	handleDir string // where the handle (the pidfile) of the interface is kept
	handleID  string // unique id of the interface within handleDir
}

// attachIface creates the interface inside the container, connects it to the switch, applies MAC and
// sysctls, and returns the MAC address of the interface. On error, nothing is left behind.
//
// The interface is a tap created by vde_plug2tap, started inside the container network namespace so that
// the tap is created there, and kept alive by that process (see detachIface).
func attachIface(ref ifaceRef) (mac string, err error) {
	iface := ref.iface
	if err = os.MkdirAll(ref.handleDir, 0o700); err != nil {
		return "", fmt.Errorf("cannot create %s: %w", ref.handleDir, err)
	}

	pidFile := pidFilePath(ref.handleDir, ref.handleID)
	if _, err = attach.RunInNetns(iface.NetnsPath,
		"vde_plug2tap", "-s", ref.switchCtl, "-d", "-P", pidFile, iface.Name); err != nil {
		return "", fmt.Errorf("cannot attach %s to the switch: %w", iface.Name, err)
	}

	// From here on, any error must stop vde_plug2tap: the tap disappears with it.
	defer func() {
		if err != nil {
			_ = detachIface(ref.handleDir, ref.handleID)
		}
	}()

	if err = attach.WaitForInterface(iface.NetnsPath, iface.Name); err != nil {
		return "", err
	}
	if err = attach.SetMAC(iface.NetnsPath, iface.Name, iface.MAC); err != nil {
		return "", err
	}
	if err = attach.SetUp(iface.NetnsPath, iface.Name); err != nil {
		return "", err
	}

	// A tap already has carrier when vde_plug2tap opens it, before the interface is brought up, so the kernel
	// never gets a carrier change afterwards and the operational state stays UNKNOWN. Toggling the carrier
	// makes the kernel recompute it, and the interface shows as UP like a veth.
	for _, state := range []string{"off", "on"} {
		if _, err = attach.RunInNetns(iface.NetnsPath, "ip", "link", "set", iface.Name, "carrier", state); err != nil {
			return "", fmt.Errorf("cannot refresh carrier of %s: %w", iface.Name, err)
		}
		time.Sleep(carrierPause)
	}

	if err = attach.ApplySysctls(iface.NetnsPath, iface.Sysctls); err != nil {
		return "", err
	}
	return attach.ReadMAC(iface.NetnsPath, iface.Name)
}

// pidFilePath returns the pidfile of the process keeping an interface alive.
func pidFilePath(handleDir string, handleID string) string {
	return filepath.Join(handleDir, handleID+".pid")
}

// detachIface stops the process keeping an interface alive; the interface disappears with it.
// A missing pidfile or an already dead process are not errors.
func detachIface(handleDir string, handleID string) error {
	pidFile := pidFilePath(handleDir, handleID)

	pid, err := readPID(pidFile)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		// Corrupted pidfile: remove it anyway, otherwise every later teardown would fail the same way.
		_ = os.Remove(pidFile)
		return fmt.Errorf("invalid pidfile %s: %w", pidFile, err)
	}

	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("cannot stop interface process %d: %w", pid, err)
	}

	if err := os.Remove(pidFile); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot remove pidfile %s: %w", pidFile, err)
	}
	return nil
}

// countIfaces returns how many interfaces are still attached (i.e. have a live process) in handleDir.
func countIfaces(handleDir string) int {
	entries, err := os.ReadDir(handleDir)
	if err != nil {
		return 0
	}
	count := 0
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".pid") {
			continue
		}
		if pid, err := readPID(filepath.Join(handleDir, entry.Name())); err == nil && alive(pid) {
			count++
		}
	}
	return count
}

// readPID returns the pid stored in a pidfile.
func readPID(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(data)))
}

// alive reports whether a process exists (EPERM means it exists but belongs to someone else).
func alive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
