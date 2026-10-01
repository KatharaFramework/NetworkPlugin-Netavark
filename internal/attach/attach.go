// Package attach holds what every driver needs to set up a container interface: the description of the
// interface and the helpers that run commands inside the network namespace of the container.
//
// netavark plugins run inside the rootless network namespace of Podman, and the namespace of the
// container is entered with nsenter. Sysctls in particular must be applied from outside: /proc/sys is
// read-only inside a rootless container.
package attach

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	// waitTimeout is how long WaitForInterface waits for an interface to appear.
	waitTimeout = 2 * time.Second
	// waitInterval is the pause between two checks.
	waitInterval = 50 * time.Millisecond
)

// Interface is a container interface to attach to a collision domain.
type Interface struct {
	NetnsPath   string            // network namespace of the container (argument of setup)
	ContainerID string            // id of the container, unique together with Name
	Name        string            // interface name inside the container, e.g. eth1
	MAC         string            // static MAC address, empty for a random one
	Sysctls     map[string]string // per-interface sysctls, already resolved (no prefix, no IFNAME)
}

// RunInNetns runs a command inside a network namespace and returns its stdout. It is a variable so that
// tests can replace it.
// On failure, the error includes the command stderr, which is what the user ends up seeing.
var RunInNetns = func(netnsPath string, args ...string) (string, error) {
	cmd := exec.Command("nsenter", append([]string{"--net=" + netnsPath}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%s: %s", strings.Join(args, " "), msg)
		}
		return "", fmt.Errorf("%s: %w", strings.Join(args, " "), err)
	}
	return stdout.String(), nil
}

// WaitForInterface waits until an interface exists in a network namespace.
func WaitForInterface(netnsPath string, name string) error {
	deadline := time.Now().Add(waitTimeout)
	for {
		if _, err := RunInNetns(netnsPath, "ip", "link", "show", name); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("interface %s did not appear within %s", name, waitTimeout)
		}
		time.Sleep(waitInterval)
	}
}

// SetMAC sets the MAC address of an interface; an empty address keeps the current one.
func SetMAC(netnsPath string, name string, mac string) error {
	if mac == "" {
		return nil
	}
	if _, err := RunInNetns(netnsPath, "ip", "link", "set", name, "address", mac); err != nil {
		return fmt.Errorf("cannot set MAC address of %s: %w", name, err)
	}
	return nil
}

// SetUp brings an interface up.
func SetUp(netnsPath string, name string) error {
	if _, err := RunInNetns(netnsPath, "ip", "link", "set", name, "up"); err != nil {
		return fmt.Errorf("cannot bring %s up: %w", name, err)
	}
	return nil
}

// ApplySysctls applies already resolved sysctls (key=value) in a network namespace.
func ApplySysctls(netnsPath string, sysctls map[string]string) error {
	for key, value := range sysctls {
		if _, err := RunInNetns(netnsPath, "sysctl", "-w", key+"="+value); err != nil {
			return fmt.Errorf("cannot apply sysctl %s=%s: %w", key, value, err)
		}
	}
	return nil
}

// ReadMAC returns the MAC address of an interface. It goes through netlink (ip -j): /sys/class/net would
// show the namespace sysfs was mounted in, not the one nsenter entered.
func ReadMAC(netnsPath string, name string) (string, error) {
	out, err := RunInNetns(netnsPath, "ip", "-j", "link", "show", "dev", name)
	if err != nil {
		return "", fmt.Errorf("cannot read MAC address of %s: %w", name, err)
	}
	var links []struct {
		Address string `json:"address"`
	}
	if err := json.Unmarshal([]byte(out), &links); err != nil || len(links) == 0 {
		return "", fmt.Errorf("cannot parse MAC address of %s from %q", name, out)
	}
	return links[0].Address, nil
}
