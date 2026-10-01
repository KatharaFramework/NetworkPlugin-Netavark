// katharanp_vde is the netavark plugin of Kathará with VDE switches: pure L2 collision domains, one
// vde_switch per network and one vde_plug2tap per interface. The driver name netavark looks up is the
// name of this executable.
package main

import (
	"github.com/KatharaFramework/NetworkPlugin-Netavark/internal/cli"
	"github.com/KatharaFramework/NetworkPlugin-Netavark/internal/vde"
)

// version is set at build time: go build -ldflags "-X main.version=..."
var version = "dev"

func main() {
	cli.Main(version, vde.Driver{})
}
