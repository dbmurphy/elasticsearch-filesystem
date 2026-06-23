package cli

import (
	"flag"
	"fmt"
	"io"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/contract"
)

// cmdPath prints the canonical ESFS path for an index/document id, applying the
// reversible filename encoding. It is the authoritative resolver for ids that
// encode to hashed short names.
func cmdPath(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("path", flag.ContinueOnError)
	fs.SetOutput(stderr)
	mount := fs.String("mount", defaultMount(), "ESFS mount root")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) != 2 {
		fmt.Fprintln(stderr, "usage: esfs path [--mount ROOT] <index> <id>")
		return 2
	}
	index, id := rest[0], rest[1]
	name, hashed := contract.EncodeID(id)
	fmt.Fprintf(stdout, "%s/%s/%s\n", *mount, index, name)
	if hashed {
		fmt.Fprintf(stderr, "esfs: note: id exceeds filename limits; exposed via hashed name %q\n", name)
	}
	return 0
}
