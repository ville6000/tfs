// Command tfs is a terminal client for Azure DevOps Server.
package main

import (
	"os"

	"github.com/ville6000/tfs/internal/cli"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(cli.Execute(version))
}
