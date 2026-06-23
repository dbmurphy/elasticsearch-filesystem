// Command esfs is the ESFS control CLI: mount/unmount, shell activation (env),
// path encoding, diagnostics (doctor), and the optional routed-search helper.
package main

import (
	"os"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/cli"
)

func main() {
	os.Exit(cli.Main(os.Args[1:], os.Stdout, os.Stderr))
}
