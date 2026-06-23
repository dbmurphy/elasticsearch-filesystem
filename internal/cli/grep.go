package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/appctx"
	"github.com/dbmurphy/elasticsearch-filesystem/internal/cmdshim"
	"github.com/dbmurphy/elasticsearch-filesystem/internal/routing"
)

// cmdGrep is the optional `esfs grep` helper: it runs the same routed search
// core directly, for debugging/scripting and environments without shell
// integration. It activates routing in-process for the given mount and rewrites
// bare index names to mount paths so `esfs grep refund conversations` works.
func cmdGrep(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("grep", flag.ContinueOnError)
	fs.SetOutput(stderr)
	mount := fs.String("mount", defaultMount(), "ESFS mount root for path resolution")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(stderr, "usage: esfs grep [--mount ROOT] [grep-flags] PATTERN [index|esfs-path ...]")
		return 2
	}

	// Activate routing in-process for this invocation.
	os.Setenv(routing.EnvMount, *mount)

	cfg, err := appctx.LoadConfig()
	if err != nil {
		fmt.Fprintf(stderr, "esfs grep: %v\n", err)
		return 2
	}
	client, err := appctx.NewClient(cfg)
	if err != nil {
		fmt.Fprintf(stderr, "esfs grep: %v\n", err)
		return 2
	}

	// Rewrite bare index names (no slash, not a flag) to mount paths so they
	// resolve to ESFS scopes.
	rewritten := make([]string, 0, len(rest))
	for _, a := range rest {
		if !strings.HasPrefix(a, "-") && !strings.Contains(a, "/") && a != "." {
			if _, ok := routing.Resolve(*mount, filepath.Join(*mount, a)); ok {
				rewritten = append(rewritten, filepath.Join(*mount, a))
				continue
			}
		}
		rewritten = append(rewritten, a)
	}

	d := &cmdshim.Deps{Core: client, Cwd: *mount, Stdout: stdout, Stderr: stderr}
	return d.RunGrep(context.Background(), rewritten)
}
