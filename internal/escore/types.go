// Package escore is the ESFS shared query core: Elasticsearch client, index
// catalog, document store with optimistic concurrency, query planner, bounded
// caches, profile generation, and diagnostics. The FUSE daemon, routed command
// shims, and CLI all sit on top of these types.
package escore

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Document is a single Elasticsearch document plus the concurrency/identity
// metadata ESFS needs for safe writes and cache validation.
type Document struct {
	Index       string
	ID          string
	Source      json.RawMessage // raw _source object
	SeqNo       *int64          // _seq_no, nil if unknown
	PrimaryTerm *int64          // _primary_term, nil if unknown
	Version     *int64          // _version, nil if unknown
	Found       bool
}

// IndexInfo describes a visible index or alias.
type IndexInfo struct {
	Name string
	// WriteTarget is the concrete index that writes resolve to. For a plain
	// index it equals Name. For an alias it is the single write index, or empty
	// when the alias has no unambiguous write target (writes then rejected).
	WriteTarget string
	IsAlias     bool
	UUID        string // index UUID when known; "" for aliases spanning many
	DocCount    int64  // approximate; -1 when unknown
}

// FieldCap describes one field's capabilities, distilled from the field_caps
// API for query planning.
type FieldCap struct {
	Name         string
	Type         string // es field type, e.g. text, keyword, date, semantic_text
	Searchable   bool
	Aggregatable bool
}

// FieldCaps is the per-index field capability map.
type FieldCaps struct {
	Index  string
	Fields map[string]FieldCap
}

// TextFields returns full-text searchable field names (text/match_only_text).
func (fc FieldCaps) TextFields() []string {
	var out []string
	for _, f := range fc.Fields {
		if f.Searchable && (f.Type == "text" || f.Type == "match_only_text") {
			out = append(out, f.Name)
		}
	}
	return out
}

// SemanticFields returns fields capable of semantic retrieval without ES-side
// setup (already-mapped semantic_text or dense/sparse vector fields).
func (fc FieldCaps) SemanticFields() []string {
	var out []string
	for _, f := range fc.Fields {
		switch f.Type {
		case "semantic_text", "sparse_vector", "dense_vector":
			out = append(out, f.Name)
		}
	}
	return out
}

// DateFields returns date-typed field names eligible for --since filters.
func (fc FieldCaps) DateFields() []string {
	var out []string
	for _, f := range fc.Fields {
		if f.Type == "date" || f.Type == "date_nanos" {
			out = append(out, f.Name)
		}
	}
	return out
}

// Hit is a single search result with the metadata ESFS surfaces to grep.
type Hit struct {
	Index    string
	ID       string
	Score    float64
	Snippets []string
	Source   json.RawMessage // present only when requested
}

// ListItem is one entry emitted by a streaming directory traversal.
type ListItem struct {
	Index string
	ID    string
}

// SearchMode records how a search was actually executed, so routed grep can
// honestly label semantic vs lexical behavior.
type SearchMode string

const (
	ModeAuto     SearchMode = "auto"     // semantic when available, else lexical
	ModeSemantic SearchMode = "semantic" // used existing semantic-capable fields
	ModeLexical  SearchMode = "lexical"  // ranked lexical (simple_query_string)
	ModeExact    SearchMode = "exact"    // byte-level fallback (handled by shim)
)

// Label returns the user-facing name for a search mode.
func (m SearchMode) Label() string {
	switch m {
	case ModeSemantic:
		return "semantic"
	case ModeLexical:
		return "ranked-lexical"
	case ModeExact:
		return "exact"
	case ModeAuto:
		return "auto"
	default:
		return string(m)
	}
}

// SearchRequest is a planned, scope-bound search.
type SearchRequest struct {
	Indices    []string
	Query      string
	Since      time.Duration // >0 enables a relative time filter
	TimeField  string        // explicit timestamp field; "" => infer
	Mode       SearchMode    // requested mode; "" => auto
	Limit      int           // 0 => stream until exhausted (bounded by caller)
	WantSource bool
	WantSnips  bool
	// SearchFields, when set, restricts lexical search to these fields instead
	// of all eligible text fields (per-index configuration).
	SearchFields []string
	// SemanticFields, when set, names the semantic_text fields to use instead of
	// auto-detecting from field capabilities (per-index configuration).
	SemanticFields []string
}

// SearchResult carries the resolved execution details alongside the hit stream.
type SearchResult struct {
	Mode      SearchMode
	TimeField string // resolved time field when Since was set
	Hits      <-chan Hit
	// Err is set after Hits closes if the search failed mid-stream.
	Err func() error
}

// Store is the document-level interface the FUSE layer and shims depend on. It
// hides Elasticsearch behind a testable seam.
type Store interface {
	// Get fetches a document with concurrency metadata.
	Get(ctx context.Context, index, id string) (*Document, error)
	// Put writes a whole-document _source under the configured write policy.
	Put(ctx context.Context, index, id string, source json.RawMessage, pol WritePolicy, meta WriteMeta) (*Document, error)
	// Delete removes a document (only when delete sync is enabled).
	Delete(ctx context.Context, index, id string, meta WriteMeta) error
	// MGet batches reads by ID for exact-grep verification.
	MGet(ctx context.Context, index string, ids []string) ([]*Document, error)
	// ResolveHashed maps a hashed short name back to its document ID.
	ResolveHashed(index, hashedName string) (string, bool)
}

// Catalog resolves visible indices/aliases and field capabilities.
type Catalog interface {
	VisibleIndices(ctx context.Context) ([]IndexInfo, error)
	IndexInfo(ctx context.Context, name string) (IndexInfo, error)
	FieldCaps(ctx context.Context, index string) (FieldCaps, error)
	Mapping(ctx context.Context, index string) (json.RawMessage, error)
}

// Lister streams directory entries for an index using PIT + search_after.
type Lister interface {
	List(ctx context.Context, index string) (<-chan ListItem, func() error)
}

// Searcher executes planned searches and streams ranked hits.
type Searcher interface {
	Search(ctx context.Context, req SearchRequest) (*SearchResult, error)
}

// WritePolicy controls create-vs-update behavior for new paths.
type WritePolicy int

const (
	// PolicySafeCreateUpdate is the default: update existing, create-only for
	// absent paths.
	PolicySafeCreateUpdate WritePolicy = iota
	// PolicyCreateOnly forces op_type=create (used for O_EXCL).
	PolicyCreateOnly
	// PolicyUpsert allows upsert for new paths (opt-in per profile).
	PolicyUpsert
)

// String returns a stable human name for a write policy.
func (p WritePolicy) String() string {
	switch p {
	case PolicyCreateOnly:
		return "create-only"
	case PolicyUpsert:
		return "upsert-new-files"
	default:
		return "safe-create-update"
	}
}

// ParseWritePolicy parses a human write-policy name.
func ParseWritePolicy(s string) (WritePolicy, error) {
	switch s {
	case "safe-create-update", "":
		return PolicySafeCreateUpdate, nil
	case "create-only":
		return PolicyCreateOnly, nil
	case "upsert-new-files", "upsert":
		return PolicyUpsert, nil
	default:
		return PolicySafeCreateUpdate, fmt.Errorf("unknown write_policy %q (use safe-create-update|create-only|upsert-new-files)", s)
	}
}

// UnmarshalJSON accepts a write policy as a human string or a legacy integer.
func (p *WritePolicy) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		parsed, perr := ParseWritePolicy(s)
		if perr != nil {
			return perr
		}
		*p = parsed
		return nil
	}
	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*p = WritePolicy(n)
	return nil
}

// MarshalJSON renders the write policy as its human name.
func (p WritePolicy) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.String())
}

// WriteMeta carries optimistic-concurrency hints for a write/delete.
type WriteMeta struct {
	SeqNo       *int64
	PrimaryTerm *int64
}
