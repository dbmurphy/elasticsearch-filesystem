package cmdshim

import (
	"context"
	"io"
	"os"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/appctx"
	"github.com/dbmurphy/elasticsearch-filesystem/internal/routing"
)

func ctxBackground() context.Context { return context.Background() }

// Tool identifies which routed command a shim implements.
type Tool string

const (
	ToolGrep Tool = "grep"
	ToolLs   Tool = "ls"
	ToolFind Tool = "find"
)

// Run is the entry point for the routed shims and `esfs grep`. It applies the
// activation and passthrough rules before building an Elasticsearch client:
//
//   - If routing is not activated, or the client cannot be built, it execs the
//     real system tool so the shim is transparent.
//   - Otherwise it dispatches to the routed runner, which itself passes through
//     non-ESFS operands.
func Run(tool Tool, args []string, stdout, stderr io.Writer) int {
	if !routing.Active() {
		return execSystem(string(tool), args, stdout, stderr)
	}
	cwd, _ := os.Getwd()
	cfg, err := appctx.LoadConfig()
	if err == nil && cfg.Endpoint == "" {
		// Activated but unconfigured: behave like the real tool.
		return execSystem(string(tool), args, stdout, stderr)
	}
	client, err := appctx.NewClient(cfg)
	if err != nil {
		return execSystem(string(tool), args, stdout, stderr)
	}
	d := &Deps{Core: client, Cwd: cwd, Stdout: stdout, Stderr: stderr}
	switch tool {
	case ToolGrep:
		return d.RunGrep(ctxBackground(), args)
	case ToolLs:
		return d.RunLs(ctxBackground(), args)
	case ToolFind:
		return d.RunFind(ctxBackground(), args)
	default:
		return execSystem(string(tool), args, stdout, stderr)
	}
}
