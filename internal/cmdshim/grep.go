package cmdshim

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/contract"
	"github.com/dbmurphy/elasticsearch-filesystem/internal/escore"
	"github.com/dbmurphy/elasticsearch-filesystem/internal/routing"
)

// grepOpts is the parsed routed-grep invocation.
type grepOpts struct {
	pattern   string
	operands  []string
	recursive bool
	pathsOnly bool
	count     bool
	exact     bool
	since     time.Duration
	limit     int
	mode      escore.SearchMode
	help      bool
	// unsupported holds flags ESFS cannot represent as routed search.
	unsupported []string
	rawArgs     []string
}

func parseGrep(args []string) grepOpts {
	o := grepOpts{rawArgs: args}
	patternSet := false
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--":
			i++
			for ; i < len(args); i++ {
				if !patternSet {
					o.pattern, patternSet = args[i], true
				} else {
					o.operands = append(o.operands, args[i])
				}
			}
		case a == "-r" || a == "-R" || a == "--recursive":
			o.recursive = true
		case a == "-l" || a == "--files-with-matches" || a == "--paths":
			o.pathsOnly = true
		case a == "-c" || a == "--count":
			o.count = true
		case a == "--exact":
			o.exact = true
		case a == "-i" || a == "--ignore-case":
			// Accepted; ES analysis is already case-folding for text fields.
		case a == "-n" || a == "--line-number":
			// Accepted but not meaningful for document-level results.
		case a == "-h" || a == "--help":
			o.help = true
		case a == "--since":
			if i+1 < len(args) {
				o.since = parseSince(args[i+1])
				i++
			}
		case strings.HasPrefix(a, "--since="):
			o.since = parseSince(strings.TrimPrefix(a, "--since="))
		case a == "--limit":
			if i+1 < len(args) {
				o.limit, _ = strconv.Atoi(args[i+1])
				i++
			}
		case strings.HasPrefix(a, "--limit="):
			o.limit, _ = strconv.Atoi(strings.TrimPrefix(a, "--limit="))
		case a == "--mode":
			if i+1 < len(args) {
				o.mode = escore.SearchMode(args[i+1])
				i++
			}
		case strings.HasPrefix(a, "--mode="):
			o.mode = escore.SearchMode(strings.TrimPrefix(a, "--mode="))
		case strings.HasPrefix(a, "-") && a != "-":
			o.unsupported = append(o.unsupported, a)
		default:
			if !patternSet {
				o.pattern, patternSet = a, true
			} else {
				o.operands = append(o.operands, a)
			}
		}
		i++
	}
	return o
}

func parseSince(s string) time.Duration {
	if d, err := time.ParseDuration(s); err == nil {
		return d
	}
	// Support bare day/week suffixes like 7d, 2w.
	if len(s) >= 2 {
		unit := s[len(s)-1]
		if n, err := strconv.Atoi(s[:len(s)-1]); err == nil {
			switch unit {
			case 'd':
				return time.Duration(n) * 24 * time.Hour
			case 'w':
				return time.Duration(n) * 7 * 24 * time.Hour
			}
		}
	}
	return 0
}

// RunGrep executes a routed grep. Exit codes follow grep: 0 match, 1 no match,
// 2 error.
func (d *Deps) RunGrep(ctx context.Context, args []string) int {
	o := parseGrep(args)
	if o.help {
		d.printGrepHelp()
		return 0
	}
	if o.pattern == "" && !o.help {
		fmt.Fprintln(d.Stderr, "esfs grep: missing pattern")
		return 2
	}

	esfs, plain := classifyOperands(d.Cwd, o.operands)

	// No ESFS scope: pass through to the real grep unchanged.
	if len(esfs) == 0 {
		return d.execReal("grep", args)
	}
	// Mixed scope is ambiguous to merge; require homogeneous operands.
	if len(plain) > 0 {
		fmt.Fprintln(d.Stderr, "esfs grep: mixing ESFS and non-ESFS paths is not supported; run them separately")
		return 2
	}

	// Mode resolution from flag or environment.
	mode := o.mode
	if mode == "" {
		mode = escore.SearchMode(os.Getenv(routing.EnvGrepMode))
	}
	strict := os.Getenv(routing.EnvStrictGrep) == "1"

	// Exact byte-level behavior: requested, strict, or unsupported flags.
	if o.exact || mode == escore.ModeExact || strict || len(o.unsupported) > 0 {
		return d.grepExact(ctx, o, esfs)
	}

	indices, err := d.indicesForScopes(ctx, esfs)
	if err != nil {
		fmt.Fprintf(d.Stderr, "esfs grep: %v\n", err)
		return 2
	}
	if len(indices) == 0 {
		return 1
	}

	req := escore.SearchRequest{
		Indices:   indices,
		Query:     o.pattern,
		Since:     o.since,
		TimeField: "",
		Mode:      mode,
		Limit:     o.limit,
		WantSnips: !o.pathsOnly && !o.count,
	}
	res, err := d.Core.Search(ctx, req)
	if err != nil {
		fmt.Fprintf(d.Stderr, "esfs grep: %v\n", err)
		return 2
	}

	// Disclose the effective search mode BEFORE results so users know how the
	// query was interpreted as output streams in.
	fmt.Fprintf(d.Stderr, "esfs: grep mode=%s\n", res.Mode.Label())

	root := esfs[0].Root
	matches := 0
	for h := range res.Hits {
		matches++
		name, _ := contract.EncodeID(h.ID)
		path := root + "/" + h.Index + "/" + name
		switch {
		case o.count:
			// counting; defer printing until end
		case o.pathsOnly:
			fmt.Fprintln(d.Stdout, path)
		default:
			line := fmt.Sprintf("%s\t%.3f", path, h.Score)
			if len(h.Snippets) > 0 {
				line += "\t" + sanitizeSnippet(h.Snippets[0])
			}
			fmt.Fprintln(d.Stdout, line)
		}
	}
	if err := res.Err(); err != nil {
		fmt.Fprintf(d.Stderr, "esfs grep: %v\n", err)
		return 2
	}
	if o.count {
		fmt.Fprintln(d.Stdout, matches)
	}
	if matches == 0 {
		return 1
	}
	return 0
}

// grepExact implements the slow path: materialize in-scope documents to a temp
// tree and run the real grep over it, translating temp paths back to ESFS
// paths. Bounded by maxExactDocs with progress to stderr.
const maxExactDocs = 5000

func (d *Deps) grepExact(ctx context.Context, o grepOpts, esfs []routing.Scope) int {
	indices, err := d.indicesForScopes(ctx, esfs)
	if err != nil {
		fmt.Fprintf(d.Stderr, "esfs grep: %v\n", err)
		return 2
	}
	tmp, err := os.MkdirTemp("", "esfs-exact-*")
	if err != nil {
		fmt.Fprintf(d.Stderr, "esfs grep: %v\n", err)
		return 2
	}
	defer os.RemoveAll(tmp)

	root := esfs[0].Root
	count := 0
	for _, idx := range indices {
		items, lerr := d.Core.List(ctx, idx)
		for it := range items {
			doc, gerr := d.fetchSource(ctx, idx, it.ID)
			if gerr != nil {
				continue
			}
			name, _ := contract.EncodeID(it.ID)
			dir := filepath.Join(tmp, idx)
			_ = os.MkdirAll(dir, 0o755)
			_ = os.WriteFile(filepath.Join(dir, name), doc, 0o644)
			count++
			if count%1000 == 0 {
				fmt.Fprintf(d.Stderr, "esfs: exact fallback materialized %d documents...\n", count)
			}
			if count >= maxExactDocs {
				fmt.Fprintf(d.Stderr, "esfs: exact fallback hit max %d documents; results may be partial (raise with care)\n", maxExactDocs)
				break
			}
		}
		if e := lerr(); e != nil {
			fmt.Fprintf(d.Stderr, "esfs grep: %v\n", e)
		}
	}

	// Build a real-grep argv: forced recursive over the temp tree.
	grepArgs := []string{"-r"}
	if o.pathsOnly {
		grepArgs = append(grepArgs, "-l")
	}
	if o.count {
		grepArgs = append(grepArgs, "-c")
	}
	for _, u := range o.unsupported {
		grepArgs = append(grepArgs, u)
	}
	grepArgs = append(grepArgs, o.pattern, tmp)

	// Capture output to translate temp paths back to ESFS paths.
	rc, out := captureSystem("grep", grepArgs)
	translated := strings.ReplaceAll(out, tmp+"/", root+"/")
	fmt.Fprint(d.Stdout, translated)
	fmt.Fprintf(d.Stderr, "esfs: grep mode=exact (materialized %d docs)\n", count)
	return rc
}

func (d *Deps) fetchSource(ctx context.Context, index, id string) ([]byte, error) {
	// The Searcher with WantSource could be used, but for exact materialization
	// a direct read keeps it simple and uses the doc cache.
	st, ok := d.Core.(escore.Store)
	if !ok {
		return nil, contract.Errf(contract.KindInternal, "core does not support direct reads")
	}
	doc, err := st.Get(ctx, index, id)
	if err != nil {
		return nil, err
	}
	return append([]byte(doc.Source), '\n'), nil
}

func sanitizeSnippet(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}

func (d *Deps) printGrepHelp() {
	active := routing.Active()
	fmt.Fprintf(d.Stdout, `esfs routed grep
  ESFS routing active: %v
  When a path is inside an ESFS mount, grep performs ranked Elasticsearch search
  (semantic when the index has semantic_text fields, otherwise ranked lexical).
  Outside ESFS, the real system grep runs unchanged.

  Flags: -r/-R recursive, -l/--paths paths only, -c count, -i, -n,
         --since DUR (e.g. 7d), --limit N, --mode auto|semantic|lexical|exact,
         --exact (byte-level fallback). Unsupported flags trigger exact fallback.
`, active)
}
