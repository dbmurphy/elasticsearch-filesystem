// Command esfsd is the ESFS mount daemon. It is a thin wrapper over `esfs
// mount`: it builds the shared core and serves the FUSE mount in the
// foreground. Most users invoke `esfs mount`; esfsd exists for service managers
// (systemd user units, launchd) that prefer a dedicated daemon binary.
package main

import (
	"os"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/cli"
)

func main() {
	args := append([]string{"mount"}, os.Args[1:]...)
	os.Exit(cli.Main(args, os.Stdout, os.Stderr))
}
