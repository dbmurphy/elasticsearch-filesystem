package escore

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/contract"
)

// Fake is an in-memory implementation of Store, Catalog, Lister, and Searcher.
// It backs unit tests and the FUSE spike without a live Elasticsearch cluster,
// and it mirrors the optimistic-concurrency and write-policy semantics of the
// real client closely enough to validate filesystem behavior.
type Fake struct {
	mu      sync.Mutex
	seq     int64
	docs    map[string]map[string]*Document // index -> id -> doc
	caps    map[string]FieldCaps
	aliases map[string]IndexInfo         // alias name -> info
	hashed  map[string]map[string]string // index -> hashedName -> id
	// DeleteEnabled gates Delete to mirror profile delete-sync gating.
	DeleteEnabled bool
}

// NewFake builds an empty Fake.
func NewFake() *Fake {
	return &Fake{
		docs:    map[string]map[string]*Document{},
		caps:    map[string]FieldCaps{},
		aliases: map[string]IndexInfo{},
		hashed:  map[string]map[string]string{},
	}
}

// Seed inserts a document and records hashed-name mappings as needed.
func (f *Fake) Seed(index, id string, source string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seedLocked(index, id, json.RawMessage(source))
}

func (f *Fake) seedLocked(index, id string, source json.RawMessage) {
	if f.docs[index] == nil {
		f.docs[index] = map[string]*Document{}
	}
	f.seq++
	seq := f.seq
	pt := int64(1)
	ver := int64(1)
	f.docs[index][id] = &Document{
		Index: index, ID: id, Source: source,
		SeqNo: &seq, PrimaryTerm: &pt, Version: &ver, Found: true,
	}
	if name, hashed := contract.EncodeID(id); hashed {
		if f.hashed[index] == nil {
			f.hashed[index] = map[string]string{}
		}
		f.hashed[index][name] = id
	}
}

// SetCaps configures field capabilities for an index.
func (f *Fake) SetCaps(index string, fields map[string]FieldCap) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.caps[index] = FieldCaps{Index: index, Fields: fields}
}

func (f *Fake) Get(_ context.Context, index, id string) (*Document, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d := f.docs[index][id]
	if d == nil {
		return &Document{Index: index, ID: id, Found: false}, contract.Errf(contract.KindNotFound, "document %s/%s not found", index, id)
	}
	cp := *d
	return &cp, nil
}

func (f *Fake) Put(_ context.Context, index, id string, source json.RawMessage, pol WritePolicy, meta WriteMeta) (*Document, error) {
	if !json.Valid(source) {
		return nil, contract.Errf(contract.KindInvalidJSON, "document body is not valid JSON")
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(source, &probe); err != nil {
		return nil, contract.Errf(contract.KindInvalidJSON, "document body must be a JSON object")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	existing := f.docs[index][id]
	switch pol {
	case PolicyCreateOnly:
		if existing != nil {
			return nil, contract.Errf(contract.KindConflict, "document %s/%s already exists", index, id)
		}
	case PolicySafeCreateUpdate, PolicyUpsert:
		if existing != nil && meta.SeqNo != nil && existing.SeqNo != nil && *existing.SeqNo != *meta.SeqNo {
			return nil, contract.Errf(contract.KindConflict, "version conflict for %s/%s", index, id)
		}
	}
	f.seedLocked(index, id, source)
	cp := *f.docs[index][id]
	return &cp, nil
}

func (f *Fake) Delete(_ context.Context, index, id string, _ WriteMeta) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.DeleteEnabled {
		return contract.Errf(contract.KindReadOnly, "delete sync is disabled for this profile")
	}
	if f.docs[index][id] == nil {
		return contract.Errf(contract.KindNotFound, "document %s/%s not found", index, id)
	}
	delete(f.docs[index], id)
	return nil
}

func (f *Fake) MGet(_ context.Context, index string, ids []string) ([]*Document, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]*Document, 0, len(ids))
	for _, id := range ids {
		if d := f.docs[index][id]; d != nil {
			cp := *d
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (f *Fake) ResolveHashed(index, hashedName string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.hashed[index][hashedName]
	return id, ok
}

func (f *Fake) VisibleIndices(_ context.Context) ([]IndexInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []IndexInfo
	for name, docs := range f.docs {
		out = append(out, IndexInfo{Name: name, WriteTarget: name, DocCount: int64(len(docs))})
	}
	for name, info := range f.aliases {
		out = append(out, info)
		_ = name
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (f *Fake) IndexInfo(_ context.Context, name string) (IndexInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if a, ok := f.aliases[name]; ok {
		return a, nil
	}
	if docs, ok := f.docs[name]; ok {
		return IndexInfo{Name: name, WriteTarget: name, DocCount: int64(len(docs))}, nil
	}
	return IndexInfo{}, contract.Errf(contract.KindNotFound, "index %q not visible", name)
}

func (f *Fake) FieldCaps(_ context.Context, index string) (FieldCaps, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.caps[index]; ok {
		return c, nil
	}
	return FieldCaps{Index: index, Fields: map[string]FieldCap{}}, nil
}

func (f *Fake) Mapping(_ context.Context, index string) (json.RawMessage, error) {
	return json.RawMessage(`{"mappings":{}}`), nil
}

func (f *Fake) List(ctx context.Context, index string) (<-chan ListItem, func() error) {
	ch := make(chan ListItem)
	go func() {
		defer close(ch)
		f.mu.Lock()
		ids := make([]string, 0, len(f.docs[index]))
		for id := range f.docs[index] {
			ids = append(ids, id)
		}
		f.mu.Unlock()
		sort.Strings(ids)
		for _, id := range ids {
			select {
			case <-ctx.Done():
				return
			case ch <- ListItem{Index: index, ID: id}:
			}
		}
	}()
	return ch, func() error { return nil }
}

// Search is a naive substring matcher used for tests. It runs the real query
// planner for mode resolution and validation (so semantic-forced and --since
// diagnostics behave like production) but matches documents by substring,
// ignoring the produced DSL filters.
func (f *Fake) Search(ctx context.Context, req SearchRequest) (*SearchResult, error) {
	var mode SearchMode
	for _, idx := range req.Indices {
		caps, _ := f.FieldCaps(ctx, idx)
		pq, err := planQuery(req, caps, time.Now())
		if err != nil {
			return nil, err
		}
		mode = pq.Mode
	}
	if mode == "" {
		mode = ModeLexical
	}
	out := make(chan Hit)
	go func() {
		defer close(out)
		needle := strings.ToLower(req.Query)
		for _, idx := range req.Indices {
			f.mu.Lock()
			docs := make([]*Document, 0, len(f.docs[idx]))
			for _, d := range f.docs[idx] {
				docs = append(docs, d)
			}
			f.mu.Unlock()
			sort.Slice(docs, func(i, j int) bool { return docs[i].ID < docs[j].ID })
			for _, d := range docs {
				if needle != "" && !strings.Contains(strings.ToLower(string(d.Source)), needle) {
					continue
				}
				h := Hit{Index: idx, ID: d.ID, Score: 1.0}
				if req.WantSource {
					h.Source = d.Source
				}
				select {
				case <-ctx.Done():
					return
				case out <- h:
				}
			}
		}
	}()
	return &SearchResult{Mode: mode, TimeField: req.TimeField, Hits: out, Err: func() error { return nil }}, nil
}
