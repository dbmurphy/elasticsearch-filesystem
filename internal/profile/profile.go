// Package profile generates SMFS-style virtual profile.md digests for the mount
// root and each index. Digests are derived only from read APIs (cluster info,
// catalog, field capabilities, counts, and a bounded document sample) and are
// cached locally; ESFS never writes profile data back to Elasticsearch.
package profile

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/escore"
)

// pinger is optionally implemented by the source to add cluster info.
type pinger interface {
	Ping(ctx context.Context) (escore.ClusterInfo, error)
}

// Source is the read-only data the generator needs.
type Source interface {
	escore.Catalog
	escore.Searcher
}

// Generator renders bounded, redaction-aware profiles and caches them.
// Redaction and profile opt-out are resolved per index via the config.
type Generator struct {
	src        Source
	cfg        escore.Config
	now        func() time.Time
	mountCache *ttlBytes
	idxCache   map[string]*ttlBytes
	sampleSize int
}

// New builds a profile generator.
func New(src Source, cfg escore.Config) *Generator {
	ttl := cfg.CacheTTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &Generator{
		src:        src,
		cfg:        cfg,
		now:        time.Now,
		mountCache: newTTLBytes(ttl),
		idxCache:   map[string]*ttlBytes{},
		sampleSize: 5,
	}
}

var _ escore.ProfileGenerator = (*Generator)(nil)

// MountProfile renders the mount-scope digest.
func (g *Generator) MountProfile(ctx context.Context) ([]byte, error) {
	if b, ok := g.mountCache.get(); ok {
		return b, nil
	}
	var buf bytes.Buffer
	buf.WriteString("# ESFS\n\n")
	buf.WriteString("Elasticsearch mounted as an SMFS-style filesystem.\n\n")

	if p, ok := g.src.(pinger); ok {
		if info, err := p.Ping(ctx); err == nil {
			fmt.Fprintf(&buf, "- Cluster: %s (v%s)\n", info.ClusterName, info.Version.Number)
		}
	}
	fmt.Fprintf(&buf, "- Write policy: %s\n", g.cfg.WritePolicy)
	fmt.Fprintf(&buf, "- Delete sync: %v\n", g.cfg.DeleteSync)
	fmt.Fprintf(&buf, "- Profiles: local-only, no Elasticsearch-side setup\n\n")

	buf.WriteString("## Visible indices\n\n")
	idx, err := g.src.VisibleIndices(ctx)
	if err != nil {
		return nil, err
	}
	if len(idx) == 0 {
		buf.WriteString("_none visible_\n\n")
	}
	for _, i := range idx {
		kind := "index"
		if i.IsAlias {
			kind = "alias"
		}
		fmt.Fprintf(&buf, "- `%s` (%s)\n", i.Name, kind)
	}

	buf.WriteString("\n## Examples\n\n")
	buf.WriteString("```sh\n")
	buf.WriteString("ls /esfs\n")
	buf.WriteString("cat /esfs/<index>/<id>\n")
	buf.WriteString("cp updated.json /esfs/<index>/<id>   # writes back to Elasticsearch\n")
	buf.WriteString("eval \"$(esfs env)\"\n")
	buf.WriteString("grep \"red shoes\" --since 7d /esfs/<index>\n")
	buf.WriteString("```\n")

	out := buf.Bytes()
	g.mountCache.put(out)
	return out, nil
}

// IndexProfile renders the index-scope digest.
func (g *Generator) IndexProfile(ctx context.Context, index string) ([]byte, error) {
	if !g.cfg.ForIndex(index).Profile {
		return []byte(fmt.Sprintf("# %s\n\nProfile generation is disabled for this index.\n", index)), nil
	}
	if c := g.idxCache[index]; c != nil {
		if b, ok := c.get(); ok {
			return b, nil
		}
	}
	var buf bytes.Buffer
	fmt.Fprintf(&buf, "# %s\n\n", index)

	info, err := g.src.IndexInfo(ctx, index)
	if err != nil {
		return nil, err
	}
	if info.DocCount >= 0 {
		fmt.Fprintf(&buf, "- Approx documents: %d\n", info.DocCount)
	}

	caps, err := g.src.FieldCaps(ctx, index)
	if err != nil {
		return nil, err
	}

	// Resolve effective per-index configuration.
	ri := g.cfg.ForIndex(index)
	semCfg := ri.SemanticFields
	semCaps := semanticFields(caps)
	semActive := semCfg
	if len(semActive) == 0 {
		semActive = semCaps
	}
	effectiveMode := resolveProfileMode(ri.GrepMode, len(semActive) > 0)
	timeField := ri.TimeField
	if timeField == "" && len(caps.DateFields()) == 1 {
		timeField = caps.DateFields()[0]
	}

	// Search section leads with the effective mode so users instantly know how
	// `grep` behaves for THIS index.
	buf.WriteString("\n## Search\n\n")
	fmt.Fprintf(&buf, "- Mode: %s\n", effectiveMode)
	if len(semActive) > 0 {
		fmt.Fprintf(&buf, "- Semantic fields: %s\n", strings.Join(semActive, ", "))
	} else {
		buf.WriteString("- No semantic_text fields; `grep` uses ranked lexical search\n")
	}
	if len(ri.SearchFields) > 0 {
		fmt.Fprintf(&buf, "- Lexical search fields (configured): %s\n", strings.Join(ri.SearchFields, ", "))
	} else if tf := caps.TextFields(); len(tf) > 0 {
		sort.Strings(tf)
		fmt.Fprintf(&buf, "- Lexical search fields (all text): %s\n", strings.Join(tf, ", "))
	}
	if timeField != "" {
		fmt.Fprintf(&buf, "- Time filtering (`--since`) via: `%s`\n", timeField)
	} else {
		buf.WriteString("- `--since` needs a `time_field` (none configured or uniquely inferable)\n")
	}
	fmt.Fprintf(&buf, "- Writes: %s; delete sync: %v\n", ri.WritePolicy, ri.DeleteSync)

	writeFieldSummary(&buf, caps)

	// Bounded, redacted sample document shape.
	redact := toSet(ri.RedactFields)
	g.writeSampleShape(ctx, &buf, index, redact)

	buf.WriteString("\n## Try it\n\n```sh\n")
	fmt.Fprintf(&buf, "grep <term> /esfs/%s\n", index)
	if timeField != "" {
		fmt.Fprintf(&buf, "grep <term> --since 7d /esfs/%s\n", index)
	}
	buf.WriteString("```\n")

	if len(redact) > 0 {
		fmt.Fprintf(&buf, "\n_redacted fields: %s_\n", strings.Join(sortedKeys(redact), ", "))
	}

	out := buf.Bytes()
	if g.idxCache[index] == nil {
		g.idxCache[index] = newTTLBytes(g.mountCache.ttl)
	}
	g.idxCache[index].put(out)
	return out, nil
}

func writeFieldSummary(buf *bytes.Buffer, caps escore.FieldCaps) {
	buf.WriteString("\n## Fields\n\n")
	names := make([]string, 0, len(caps.Fields))
	for n := range caps.Fields {
		names = append(names, n)
	}
	sort.Strings(names)
	max := 40
	for i, n := range names {
		if i >= max {
			fmt.Fprintf(buf, "- _...and %d more_\n", len(names)-max)
			break
		}
		fmt.Fprintf(buf, "- `%s`: %s\n", n, caps.Fields[n].Type)
	}
	if len(names) == 0 {
		buf.WriteString("_no fields reported_\n")
	}
}

func (g *Generator) writeSampleShape(ctx context.Context, buf *bytes.Buffer, index string, redact map[string]struct{}) {
	res, err := g.src.Search(ctx, escore.SearchRequest{
		Indices: []string{index}, Query: "", Limit: g.sampleSize, WantSource: true,
	})
	if err != nil {
		return
	}
	var sample json.RawMessage
	for h := range res.Hits {
		if sample == nil {
			sample = h.Source
		}
	}
	_ = res.Err()
	if sample == nil {
		return
	}
	var obj map[string]json.RawMessage
	if json.Unmarshal(sample, &obj) != nil {
		return
	}
	for f := range redact {
		if _, ok := obj[f]; ok {
			obj[f] = json.RawMessage(`"<redacted>"`)
		}
	}
	var pretty bytes.Buffer
	enc := json.NewEncoder(&pretty)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	_ = enc.Encode(obj)
	buf.WriteString("\n## Sample document shape\n\n```json\n")
	buf.Write(bytes.TrimRight(pretty.Bytes(), "\n"))
	buf.WriteString("\n```\n")
}

// resolveProfileMode describes how `grep` will behave for an index given its
// configured mode and whether semantic fields are available.
func resolveProfileMode(mode escore.SearchMode, hasSemantic bool) string {
	switch mode {
	case escore.ModeSemantic:
		if hasSemantic {
			return "semantic"
		}
		return "semantic (configured, but no semantic_text fields — queries will error)"
	case escore.ModeLexical:
		return "ranked-lexical"
	default: // auto / unset
		if hasSemantic {
			return "semantic (auto; falls back to ranked-lexical if unavailable)"
		}
		return "ranked-lexical (auto; no semantic_text fields)"
	}
}

func toSet(ss []string) map[string]struct{} {
	m := make(map[string]struct{}, len(ss))
	for _, s := range ss {
		m[s] = struct{}{}
	}
	return m
}

func semanticFields(caps escore.FieldCaps) []string {
	var out []string
	for _, f := range caps.Fields {
		if f.Type == "semantic_text" {
			out = append(out, f.Name)
		}
	}
	sort.Strings(out)
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ttlBytes is a tiny single-value TTL cache.
type ttlBytes struct {
	ttl time.Duration
	val []byte
	exp time.Time
	now func() time.Time
}

func newTTLBytes(ttl time.Duration) *ttlBytes { return &ttlBytes{ttl: ttl, now: time.Now} }

func (c *ttlBytes) get() ([]byte, bool) {
	if c.val == nil || c.now().After(c.exp) {
		return nil, false
	}
	return c.val, true
}

func (c *ttlBytes) put(b []byte) {
	c.val = b
	c.exp = c.now().Add(c.ttl)
}
