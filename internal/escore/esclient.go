package escore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/contract"
)

// ESClient is the live Elasticsearch implementation of Store, Catalog, Lister,
// and Searcher. It layers bounded caches over the HTTP client and maintains an
// in-memory hashed-name reverse map populated as IDs are encoded.
type ESClient struct {
	hc  *httpClient
	cfg Config
	now func() time.Time

	capsCache *ttlCache[FieldCaps]
	mapCache  *ttlCache[json.RawMessage]
	idxCache  *ttlCache[[]IndexInfo]
	docCache  *ttlCache[*Document]

	hmu    sync.Mutex
	hashed map[string]map[string]string // index -> hashedName -> id
}

const pageSize = 1000

var (
	_ Store    = (*ESClient)(nil)
	_ Catalog  = (*ESClient)(nil)
	_ Lister   = (*ESClient)(nil)
	_ Searcher = (*ESClient)(nil)
)

// NewESClient builds a live client from config.
func NewESClient(cfg Config) (*ESClient, error) {
	hc, err := newHTTPClient(cfg)
	if err != nil {
		return nil, err
	}
	return &ESClient{
		hc:        hc,
		cfg:       cfg,
		now:       time.Now,
		capsCache: newTTLCache[FieldCaps](256, cfg.CacheTTL),
		mapCache:  newTTLCache[json.RawMessage](256, cfg.CacheTTL),
		idxCache:  newTTLCache[[]IndexInfo](4, cfg.CacheTTL),
		docCache:  newTTLCache[*Document](4096, cfg.CacheTTL),
		hashed:    map[string]map[string]string{},
	}, nil
}

// ClusterInfo is a subset of the ES root response used by diagnostics.
type ClusterInfo struct {
	Name        string `json:"name"`
	ClusterName string `json:"cluster_name"`
	Version     struct {
		Number string `json:"number"`
	} `json:"version"`
}

// Ping queries the cluster root to verify connectivity and auth.
func (c *ESClient) Ping(ctx context.Context) (ClusterInfo, error) {
	var info ClusterInfo
	r, err := c.hc.do(ctx, "GET", "/", nil)
	if err != nil {
		return info, err
	}
	if r.status/100 != 2 {
		return info, statusError(r, "cluster info")
	}
	if err := json.Unmarshal(r.body, &info); err != nil {
		return info, contract.Wrap(contract.KindUpstream, err, "decode cluster info")
	}
	return info, nil
}

func (c *ESClient) rememberHashed(index, id string) {
	name, hashed := contract.EncodeID(id)
	if !hashed {
		return
	}
	c.hmu.Lock()
	if c.hashed[index] == nil {
		c.hashed[index] = map[string]string{}
	}
	c.hashed[index][name] = id
	c.hmu.Unlock()
}

// ResolveHashed implements Store.
func (c *ESClient) ResolveHashed(index, hashedName string) (string, bool) {
	c.hmu.Lock()
	defer c.hmu.Unlock()
	id, ok := c.hashed[index][hashedName]
	return id, ok
}

// ---- Store ----

type getResp struct {
	Index       string          `json:"_index"`
	ID          string          `json:"_id"`
	Found       bool            `json:"found"`
	SeqNo       *int64          `json:"_seq_no"`
	PrimaryTerm *int64          `json:"_primary_term"`
	Version     *int64          `json:"_version"`
	Source      json.RawMessage `json:"_source"`
}

func (c *ESClient) Get(ctx context.Context, index, id string) (*Document, error) {
	key := index + "\x00" + id
	if d, ok := c.docCache.Get(key); ok {
		return d, nil
	}
	path := fmt.Sprintf("/%s/_doc/%s", url.PathEscape(index), url.PathEscape(id))
	r, err := c.hc.do(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	if r.status == 404 {
		return &Document{Index: index, ID: id, Found: false}, contract.Errf(contract.KindNotFound, "document %s/%s not found", index, id)
	}
	if r.status/100 != 2 {
		return nil, statusError(r, "get document")
	}
	var gr getResp
	if err := json.Unmarshal(r.body, &gr); err != nil {
		return nil, contract.Wrap(contract.KindUpstream, err, "decode get response")
	}
	doc := &Document{
		Index: index, ID: id, Source: gr.Source,
		SeqNo: gr.SeqNo, PrimaryTerm: gr.PrimaryTerm, Version: gr.Version, Found: gr.Found,
	}
	c.docCache.Put(key, doc)
	c.rememberHashed(index, id)
	return doc, nil
}

func (c *ESClient) Put(ctx context.Context, index, id string, source json.RawMessage, pol WritePolicy, meta WriteMeta) (*Document, error) {
	if !json.Valid(source) {
		return nil, contract.Errf(contract.KindInvalidJSON, "document body is not valid JSON")
	}
	var path string
	switch pol {
	case PolicyCreateOnly:
		path = fmt.Sprintf("/%s/_create/%s", url.PathEscape(index), url.PathEscape(id))
	default:
		path = fmt.Sprintf("/%s/_doc/%s", url.PathEscape(index), url.PathEscape(id))
		if pol == PolicySafeCreateUpdate && meta.SeqNo != nil && meta.PrimaryTerm != nil {
			path += fmt.Sprintf("?if_seq_no=%d&if_primary_term=%d", *meta.SeqNo, *meta.PrimaryTerm)
		}
	}
	r, err := c.hc.do(ctx, "PUT", path, json.RawMessage(source))
	if err != nil {
		return nil, err
	}
	if r.status == 409 {
		return nil, contract.Errf(contract.KindConflict, "version conflict writing %s/%s", index, id)
	}
	if r.status/100 != 2 {
		return nil, statusError(r, "write document")
	}
	var wr struct {
		SeqNo       *int64 `json:"_seq_no"`
		PrimaryTerm *int64 `json:"_primary_term"`
		Version     *int64 `json:"_version"`
	}
	_ = json.Unmarshal(r.body, &wr)
	doc := &Document{Index: index, ID: id, Source: source, SeqNo: wr.SeqNo, PrimaryTerm: wr.PrimaryTerm, Version: wr.Version, Found: true}
	c.docCache.Put(index+"\x00"+id, doc)
	c.rememberHashed(index, id)
	return doc, nil
}

func (c *ESClient) Delete(ctx context.Context, index, id string, meta WriteMeta) error {
	path := fmt.Sprintf("/%s/_doc/%s", url.PathEscape(index), url.PathEscape(id))
	if meta.SeqNo != nil && meta.PrimaryTerm != nil {
		path += fmt.Sprintf("?if_seq_no=%d&if_primary_term=%d", *meta.SeqNo, *meta.PrimaryTerm)
	}
	r, err := c.hc.do(ctx, "DELETE", path, nil)
	if err != nil {
		return err
	}
	if r.status == 404 {
		return contract.Errf(contract.KindNotFound, "document %s/%s not found", index, id)
	}
	if r.status == 409 {
		return contract.Errf(contract.KindConflict, "version conflict deleting %s/%s", index, id)
	}
	if r.status/100 != 2 {
		return statusError(r, "delete document")
	}
	c.docCache.Invalidate(index + "\x00" + id)
	return nil
}

func (c *ESClient) MGet(ctx context.Context, index string, ids []string) ([]*Document, error) {
	body := map[string]any{"ids": ids}
	path := fmt.Sprintf("/%s/_mget", url.PathEscape(index))
	r, err := c.hc.do(ctx, "POST", path, body)
	if err != nil {
		return nil, err
	}
	if r.status/100 != 2 {
		return nil, statusError(r, "mget")
	}
	var mr struct {
		Docs []getResp `json:"docs"`
	}
	if err := json.Unmarshal(r.body, &mr); err != nil {
		return nil, contract.Wrap(contract.KindUpstream, err, "decode mget")
	}
	out := make([]*Document, 0, len(mr.Docs))
	for _, d := range mr.Docs {
		if !d.Found {
			continue
		}
		out = append(out, &Document{Index: index, ID: d.ID, Source: d.Source, SeqNo: d.SeqNo, PrimaryTerm: d.PrimaryTerm, Version: d.Version, Found: true})
	}
	return out, nil
}

// ---- Catalog ----

func (c *ESClient) VisibleIndices(ctx context.Context) ([]IndexInfo, error) {
	if v, ok := c.idxCache.Get("all"); ok {
		return c.filterVisible(v), nil
	}
	r, err := c.hc.do(ctx, "GET", "/_resolve/index/*?expand_wildcards=open", nil)
	if err != nil {
		return nil, err
	}
	if r.status/100 != 2 {
		return nil, statusError(r, "resolve indices")
	}
	var rr struct {
		Indices []struct {
			Name    string   `json:"name"`
			Aliases []string `json:"aliases"`
		} `json:"indices"`
		Aliases []struct {
			Name    string   `json:"name"`
			Indices []string `json:"indices"`
		} `json:"aliases"`
	}
	if err := json.Unmarshal(r.body, &rr); err != nil {
		return nil, contract.Wrap(contract.KindUpstream, err, "decode resolve")
	}
	var out []IndexInfo
	for _, i := range rr.Indices {
		if strings.HasPrefix(i.Name, ".") {
			continue
		}
		out = append(out, IndexInfo{Name: i.Name, WriteTarget: i.Name})
	}
	for _, a := range rr.Aliases {
		if strings.HasPrefix(a.Name, ".") {
			continue
		}
		wt := ""
		if len(a.Indices) == 1 {
			wt = a.Indices[0]
		}
		out = append(out, IndexInfo{Name: a.Name, IsAlias: true, WriteTarget: wt})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	c.idxCache.Put("all", out)
	return c.filterVisible(out), nil
}

func (c *ESClient) filterVisible(in []IndexInfo) []IndexInfo {
	if len(c.cfg.VisibleIndices) == 0 {
		return in
	}
	allow := map[string]struct{}{}
	for _, n := range c.cfg.VisibleIndices {
		allow[n] = struct{}{}
	}
	var out []IndexInfo
	for _, i := range in {
		if _, ok := allow[i.Name]; ok {
			out = append(out, i)
		}
	}
	return out
}

func (c *ESClient) IndexInfo(ctx context.Context, name string) (IndexInfo, error) {
	all, err := c.VisibleIndices(ctx)
	if err != nil {
		return IndexInfo{}, err
	}
	for _, i := range all {
		if i.Name == name {
			return i, nil
		}
	}
	return IndexInfo{}, contract.Errf(contract.KindNotFound, "index %q not visible", name)
}

func (c *ESClient) FieldCaps(ctx context.Context, index string) (FieldCaps, error) {
	if v, ok := c.capsCache.Get(index); ok {
		return v, nil
	}
	path := fmt.Sprintf("/%s/_field_caps?fields=*", url.PathEscape(index))
	r, err := c.hc.do(ctx, "GET", path, nil)
	if err != nil {
		return FieldCaps{}, err
	}
	if r.status/100 != 2 {
		return FieldCaps{}, statusError(r, "field caps")
	}
	var fr struct {
		Fields map[string]map[string]struct {
			Type         string `json:"type"`
			Searchable   bool   `json:"searchable"`
			Aggregatable bool   `json:"aggregatable"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(r.body, &fr); err != nil {
		return FieldCaps{}, contract.Wrap(contract.KindUpstream, err, "decode field_caps")
	}
	fc := FieldCaps{Index: index, Fields: map[string]FieldCap{}}
	for name, byType := range fr.Fields {
		for typ, cap := range byType {
			fc.Fields[name] = FieldCap{Name: name, Type: typ, Searchable: cap.Searchable, Aggregatable: cap.Aggregatable}
			break // first capability is representative for v1
		}
	}
	c.capsCache.Put(index, fc)
	return fc, nil
}

func (c *ESClient) Mapping(ctx context.Context, index string) (json.RawMessage, error) {
	if v, ok := c.mapCache.Get(index); ok {
		return v, nil
	}
	path := fmt.Sprintf("/%s/_mapping", url.PathEscape(index))
	r, err := c.hc.do(ctx, "GET", path, nil)
	if err != nil {
		return nil, err
	}
	if r.status/100 != 2 {
		return nil, statusError(r, "mapping")
	}
	m := json.RawMessage(r.body)
	c.mapCache.Put(index, m)
	return m, nil
}
