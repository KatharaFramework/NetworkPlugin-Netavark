# NetworkPlugin-Netavark

Netavark plugins of [Kathará](https://www.kathara.org): pure L2 collision domains for the Podman backend.
Counterparts of the two Docker plugins of [NetworkPlugin](https://github.com/KatharaFramework/NetworkPlugin)
(`kathara/katharanp` and `kathara/katharanp_vde`). For netavark the driver name is the name of the
executable, so there are two binaries in the same plugin directory, and the user picks one per network:

| Driver         | Data plane                                            | Source            |
|----------------|-------------------------------------------------------|-------------------|
| `katharanp`    | Linux bridge (kernel), one veth pair per interface    | `cmd/katharanp`     |
| `katharanp_vde`| VDE switch (userspace), one `vde_plug2tap` per interface | `cmd/katharanp_vde` |

Both use the same options (per-interface `sysctl.*`), the same naming (`kt-` plus the first 12 characters
of the network ID) and create the collision domain when the first interface is attached and remove it when
the last one is detached.

## Differences between the drivers

* **`katharanp` (bridge)**: the data plane is the kernel. The bridge is created like the one of the Docker
  plugin: ageing time 0, so it forgets every MAC address at once and floods every frame like a hub, and
  `group_fwd_mask 0xfff8`, so it forwards every reserved group address the kernel allows (e.g. LLDP).
  IPv6 is disabled on the bridge (no link-local address on the segment) and the MTU of the bridge and of
  the host side of each veth is 65535, so that a container can raise its own MTU for jumbo frames. It
  needs no firewall rule and no helper program, and it is created on demand in the rootless network
  namespace of Podman, which disappears with the last container.
* **`katharanp_vde`**: the data plane is a userspace switch (`vde_switch`) that forwards every frame,
  with one `vde_plug2tap` process per interface.

**Known limit of the bridge driver: LACP.** The kernel never forwards the frames sent to
01:80:c2:00:00:00-02 (STP, pause, LACP) through a Linux bridge, and `group_fwd_mask` cannot enable them
(the kernel rejects it with EINVAL). LACP cannot cross a `katharanp` network: use `katharanp_vde`.

## Build

Requires Go, and `vdeplug4` (headers and `libvdeplug`) for the reused VDE library (`katharanp_vde` only:
`katharanp` is a static binary without cgo).

```bash
git submodule update --init
make install    # installs both drivers into ~/.local/libexec/netavark-plugins
```

Runtime dependencies: `nsenter`, `ip`, `sysctl` (both drivers), plus `vde_switch` and `vde_plug2tap`
(`katharanp_vde`).

## Layout

* `internal/plugin`: netavark logic independent of the data plane, on top of the `Driver` interface.
* `internal/bridge`, `internal/vde`: the two drivers (`internal/vde` is the only package using cgo).
* `internal/attach`: helpers shared by the drivers (commands inside the container namespace).
* `internal/cli`: subcommands and JSON I/O; each `cmd/*/main.go` only picks the driver.
