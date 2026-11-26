// Command ce is the command line client of Checkend: stats, events, funnels,
// changes and exports of a Checkend server, with an API token.
package main

import (
	"os"
	"runtime/debug"

	"github.com/checkend/checkend-cli/internal/cli"
)

// version is set by the release build (-ldflags "-X main.version=...").
var version = "dev"

func main() {
	if version == "dev" {
		// go install …@v0.1.0 records the module version.
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			version = bi.Main.Version
		}
	}
	os.Exit(cli.Main(version))
}
