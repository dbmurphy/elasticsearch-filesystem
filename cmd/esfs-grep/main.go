// Command esfs-grep is the routed grep shim. When ESFS routing is active and a
// path operand (or the cwd) is inside an ESFS mount, it performs ranked
// Elasticsearch search; otherwise it execs the real system grep unchanged.
package main

import (
	"os"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/cmdshim"
)

func main() {
	os.Exit(cmdshim.Run(cmdshim.ToolGrep, os.Args[1:], os.Stdout, os.Stderr))
}
