package cmdshim

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/contract"
)

// RunLs implements routed `ls`: lists indices at the mount root or document
// names within an index. Non-ESFS operands pass through to the real ls.
func (d *Deps) RunLs(ctx context.Context, args []string) int {
	flags, operands := splitFlags(args)
	esfs, plain := classifyOperands(d.Cwd, operands)
	if len(esfs) == 0 {
		return d.execReal("ls", args)
	}
	if len(plain) > 0 {
		fmt.Fprintln(d.Stderr, "esfs ls: mixing ESFS and non-ESFS paths is not supported; run them separately")
		return 2
	}
	_ = flags
	rc := 0
	for _, s := range esfs {
		switch s.Node.Kind {
		case contract.NodeRoot:
			infos, err := d.Core.VisibleIndices(ctx)
			if err != nil {
				fmt.Fprintf(d.Stderr, "esfs ls: %v\n", err)
				rc = 2
				continue
			}
			fmt.Fprintln(d.Stdout, "profile.md")
			fmt.Fprintln(d.Stdout, ".sync.json")
			for _, i := range infos {
				fmt.Fprintln(d.Stdout, i.Name)
			}
		case contract.NodeIndexDir:
			if _, err := d.Core.IndexInfo(ctx, s.Node.Index); err != nil {
				fmt.Fprintf(d.Stderr, "esfs ls: %v\n", err)
				rc = 2
				continue
			}
			fmt.Fprintln(d.Stdout, "profile.md")
			fmt.Fprintln(d.Stdout, ".sync.json")
			fmt.Fprintln(d.Stdout, ".mapping.json")
			fmt.Fprintln(d.Stdout, ".fields.json")
			items, errf := d.Core.List(ctx, s.Node.Index)
			for it := range items {
				name, _ := contract.EncodeID(it.ID)
				fmt.Fprintln(d.Stdout, name)
			}
			if err := errf(); err != nil {
				fmt.Fprintf(d.Stderr, "esfs ls: %v\n", err)
				rc = 2
			}
		case contract.NodeDocument:
			name, _ := contract.EncodeID(s.Node.ID)
			fmt.Fprintln(d.Stdout, s.Root+"/"+s.Node.Index+"/"+name)
		default:
			fmt.Fprintf(d.Stderr, "esfs ls: cannot list %q\n", s.Rel)
			rc = 2
		}
	}
	return rc
}

// RunFind implements routed `find`: recursively prints ESFS paths under the
// given scope. Non-ESFS operands pass through to the real find.
func (d *Deps) RunFind(ctx context.Context, args []string) int {
	// find's first operands are paths, then expression primaries (-name, etc.).
	paths, rest := splitFindPaths(args)
	esfs, plain := classifyOperands(d.Cwd, paths)
	if len(esfs) == 0 {
		return d.execReal("find", args)
	}
	if len(plain) > 0 {
		fmt.Fprintln(d.Stderr, "esfs find: mixing ESFS and non-ESFS paths is not supported; run them separately")
		return 2
	}
	if len(rest) > 0 {
		fmt.Fprintf(d.Stderr, "esfs find: expression primaries %v are not supported in routed mode; use --exact grep or real find\n", rest)
		return 2
	}
	rc := 0
	for _, s := range esfs {
		switch s.Node.Kind {
		case contract.NodeRoot:
			fmt.Fprintln(d.Stdout, s.Root)
			infos, err := d.Core.VisibleIndices(ctx)
			if err != nil {
				fmt.Fprintf(d.Stderr, "esfs find: %v\n", err)
				rc = 2
				continue
			}
			for _, i := range infos {
				fmt.Fprintln(d.Stdout, s.Root+"/"+i.Name)
				d.findIndex(ctx, s.Root, i.Name, &rc)
			}
		case contract.NodeIndexDir:
			fmt.Fprintln(d.Stdout, s.Root+"/"+s.Node.Index)
			d.findIndex(ctx, s.Root, s.Node.Index, &rc)
		case contract.NodeDocument:
			name, _ := contract.EncodeID(s.Node.ID)
			fmt.Fprintln(d.Stdout, s.Root+"/"+s.Node.Index+"/"+name)
		default:
			rc = 2
		}
	}
	return rc
}

func (d *Deps) findIndex(ctx context.Context, root, index string, rc *int) {
	items, errf := d.Core.List(ctx, index)
	for it := range items {
		name, _ := contract.EncodeID(it.ID)
		fmt.Fprintln(d.Stdout, root+"/"+index+"/"+name)
	}
	if err := errf(); err != nil {
		fmt.Fprintf(d.Stderr, "esfs find: %v\n", err)
		*rc = 2
	}
}

func splitFlags(args []string) (flags, operands []string) {
	for _, a := range args {
		if strings.HasPrefix(a, "-") && a != "-" {
			flags = append(flags, a)
		} else {
			operands = append(operands, a)
		}
	}
	return
}

func splitFindPaths(args []string) (paths, rest []string) {
	i := 0
	for i < len(args) {
		if strings.HasPrefix(args[i], "-") || args[i] == "!" || args[i] == "(" {
			break
		}
		paths = append(paths, args[i])
		i++
	}
	rest = args[i:]
	if len(paths) == 0 {
		paths = []string{"."}
	}
	return
}

// captureSystem runs a system tool capturing combined output, returning the
// exit code and the captured text.
func captureSystem(tool string, args []string) (int, string) {
	var buf bytes.Buffer
	cmd := exec.Command(systemToolPath(tool), args...)
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	if err != nil {
		var ee *exec.ExitError
		if asExit(err, &ee) {
			return ee.ExitCode(), buf.String()
		}
		return 2, buf.String() + fmt.Sprintf("\nesfs: exec %s: %v", tool, err)
	}
	return 0, buf.String()
}
