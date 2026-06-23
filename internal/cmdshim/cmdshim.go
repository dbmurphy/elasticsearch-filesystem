// Package cmdshim implements the routed ls/find/grep behavior shared by the
// esfs-ls, esfs-find, and esfs-grep binaries and by `esfs grep`. It detects
// ESFS scope, runs catalog/search against the shared core, and falls back to the
// real system tools for non-ESFS scopes or unsupported flags.
package cmdshim

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"time"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/contract"
	"github.com/dbmurphy/elasticsearch-filesystem/internal/escore"
	"github.com/dbmurphy/elasticsearch-filesystem/internal/routing"
)

// Core is the subset of the shared query core the shims need.
type Core interface {
	escore.Catalog
	escore.Lister
	escore.Searcher
}

// Deps wires a shim invocation to its environment.
type Deps struct {
	Core   Core
	Cwd    string
	Stdout io.Writer
	Stderr io.Writer
	Now    func() time.Time
	// ExecReal runs a real system tool (passthrough/exact). Injectable for
	// tests; defaults to execSystem.
	ExecReal func(tool string, args []string) int
}

func (d *Deps) now() time.Time {
	if d.Now != nil {
		return d.Now()
	}
	return time.Now()
}

func (d *Deps) execReal(tool string, args []string) int {
	if d.ExecReal != nil {
		return d.ExecReal(tool, args)
	}
	return execSystem(tool, args, d.Stdout, d.Stderr)
}

// indicesForScopes resolves the set of target indices from ESFS operand scopes.
// A root-scope operand expands to all visible indices.
func (d *Deps) indicesForScopes(ctx context.Context, scopes []routing.Scope) ([]string, error) {
	seen := map[string]struct{}{}
	var out []string
	add := func(name string) {
		if _, ok := seen[name]; !ok {
			seen[name] = struct{}{}
			out = append(out, name)
		}
	}
	for _, s := range scopes {
		switch s.Node.Kind {
		case contract.NodeRoot:
			infos, err := d.Core.VisibleIndices(ctx)
			if err != nil {
				return nil, err
			}
			for _, i := range infos {
				add(i.Name)
			}
		case contract.NodeIndexDir, contract.NodeDocument,
			contract.NodeIndexProfile, contract.NodeIndexSync,
			contract.NodeIndexMapping, contract.NodeIndexFields:
			add(s.Node.Index)
		}
	}
	sort.Strings(out)
	return out, nil
}

// classifyOperands splits operands into ESFS scopes and plain (non-ESFS) paths.
func classifyOperands(cwd string, operands []string) (esfs []routing.Scope, plain []string) {
	if len(operands) == 0 {
		if s, ok := routing.Resolve(cwd, "."); ok {
			return []routing.Scope{s}, nil
		}
		return nil, []string{"."}
	}
	for _, op := range operands {
		if s, ok := routing.Resolve(cwd, op); ok {
			esfs = append(esfs, s)
		} else {
			plain = append(plain, op)
		}
	}
	return esfs, plain
}

func mountPath(s routing.Scope, index, encodedName string) string {
	return s.Root + "/" + index + "/" + encodedName
}

// execSystem runs a resolved system tool, wiring stdio, returning its exit code.
func execSystem(tool string, args []string, stdout, stderr io.Writer) int {
	path := systemToolPath(tool)
	cmd := exec.Command(path, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		var ee *exec.ExitError
		if asExit(err, &ee) {
			return ee.ExitCode()
		}
		fmt.Fprintf(stderr, "esfs: failed to exec %s: %v\n", tool, err)
		return 2
	}
	return 0
}

func asExit(err error, target **exec.ExitError) bool {
	if ee, ok := err.(*exec.ExitError); ok {
		*target = ee
		return true
	}
	return false
}

// systemToolPath resolves the real system tool, preferring canonical absolute
// locations and otherwise PATH lookup. ESFS shim directories are expected to be
// prepended to PATH, so we bias to absolute system paths to avoid recursion.
func systemToolPath(tool string) string {
	for _, p := range []string{"/usr/bin/" + tool, "/bin/" + tool} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	if p, err := exec.LookPath(tool); err == nil {
		return p
	}
	return tool
}
