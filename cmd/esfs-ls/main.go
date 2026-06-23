// Command esfs-ls is the routed ls shim. Inside an ESFS mount it lists indices
// or document names from Elasticsearch; otherwise it execs the real system ls.
package main

import (
	"os"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/cmdshim"
)

func main() {
	os.Exit(cmdshim.Run(cmdshim.ToolLs, os.Args[1:], os.Stdout, os.Stderr))
}
