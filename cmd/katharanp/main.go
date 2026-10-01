// katharanp is the netavark plugin of Kathará with Linux bridges: pure L2 collision domains, one bridge
// per network and one veth pair per interface. The driver name netavark looks up is the name of this
// executable.
package main

import (
	"github.com/KatharaFramework/NetworkPlugin-Netavark/internal/bridge"
	"github.com/KatharaFramework/NetworkPlugin-Netavark/internal/cli"
)

// version is set at build time: go build -ldflags "-X main.version=..."
var version = "dev"

func main() {
	cli.Main(version, bridge.Driver{})
}
