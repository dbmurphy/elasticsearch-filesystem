// Command esfs-find is the routed find shim. Inside an ESFS mount it streams
// the virtual document tree from Elasticsearch; otherwise it execs the real
// system find.
package main

import (
	"os"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/cmdshim"
)

func main() {
	os.Exit(cmdshim.Run(cmdshim.ToolFind, os.Args[1:], os.Stdout, os.Stderr))
}
