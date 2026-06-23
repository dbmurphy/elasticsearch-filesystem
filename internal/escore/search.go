package escore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/contract"
)

// pitKeepAlive is short, renewed only while a traversal is active, to avoid
// pinning segments on Elasticsearch.
const pitKeepAlive = "1m"

type searchHit struct {
	Index     string              `json:"_index"`
	ID        string              `json:"_id"`
	Score     float64             `json:"_score"`
	Source    json.RawMessage     `json:"_source"`
	Sort      []json.RawMessage   `json:"sort"`
	Highlight map[string][]string `json:"highlight"`
}

type searchResp struct {
	PitID string `json:"pit_id"`
	Hits  struct {
		Hits []searchHit `json:"hits"`
	} `json:"hits"`
}

// openPIT opens a point-in-time for an index and returns its id.
func (c *ESClient) openPIT(ctx context.Context, index string) (string, error) {
	path := fmt.Sprintf("/%s/_pit?keep_alive=%s", url.PathEscape(index), pitKeepAlive)
	r, err := c.hc.do(ctx, "POST", path, nil)
	if err != nil {
		return "", err
	}
	if r.status/100 != 2 {
		return "", statusError(r, "open pit")
	}
	var pr struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(r.body, &pr); err != nil {
		return "", contract.Wrap(contract.KindUpstream, err, "decode pit")
	}
	return pr.ID, nil
}

func (c *ESClient) closePIT(pitID string) {
	if pitID == "" {
		return
	}
	// Best-effort close with a fresh short context.
	ctx, cancel := context.WithTimeout(context.Background(), c.hc.hc.Timeout)
	defer cancel()
	_, _ = c.hc.do(ctx, "DELETE", "/_pit", map[string]any{"id": pitID})
}

// streamPIT runs a PIT + search_after loop, invoking emit for each hit until
// exhausted, the limit is reached, or the context is canceled. The query is the
// ES query body; sort defines deterministic ordering.
func (c *ESClient) streamPIT(ctx context.Context, index string, query map[string]any, sort []any, includeSource bool, limit int, emit func(searchHit) bool) error {
	pit, err := c.openPIT(ctx, index)
	if err != nil {
		return err
	}
	defer c.closePIT(pit)

	var searchAfter []json.RawMessage
	count := 0
	for {
		body := map[string]any{
			"size":             pageSize,
			"track_total_hits": false,
			"sort":             sort,
			"pit":              map[string]any{"id": pit, "keep_alive": pitKeepAlive},
			"_source":          includeSource,
		}
		if query != nil {
			body["query"] = query
		}
		if searchAfter != nil {
			body["search_after"] = searchAfter
		}
		r, err := c.hc.do(ctx, "POST", "/_search", body)
		if err != nil {
			return err
		}
		if r.status/100 != 2 {
			return statusError(r, "search")
		}
		var sr searchResp
		if err := json.Unmarshal(r.body, &sr); err != nil {
			return contract.Wrap(contract.KindUpstream, err, "decode search")
		}
		if sr.PitID != "" {
			pit = sr.PitID // ES may return a refreshed id
		}
		if len(sr.Hits.Hits) == 0 {
			return nil
		}
		for _, h := range sr.Hits.Hits {
			if !emit(h) {
				return nil
			}
			count++
			if limit > 0 && count >= limit {
				return nil
			}
			searchAfter = h.Sort
		}
	}
}

// List implements Lister: streams every document ID in an index via PIT +
// search_after with a stable _shard_doc sort and no _source.
func (c *ESClient) List(ctx context.Context, index string) (<-chan ListItem, func() error) {
	ch := make(chan ListItem)
	var ferr error
	done := make(chan struct{})
	go func() {
		defer close(ch)
		defer close(done)
		sort := []any{map[string]any{"_shard_doc": "asc"}}
		ferr = c.streamPIT(ctx, index, nil, sort, false, 0, func(h searchHit) bool {
			c.rememberHashed(index, h.ID)
			select {
			case <-ctx.Done():
				return false
			case ch <- ListItem{Index: index, ID: h.ID}:
				return true
			}
		})
	}()
	return ch, func() error { <-done; return ferr }
}

// Search implements Searcher: plans the query per index, then streams ranked
// hits. Ranked search sorts by _score desc with an _id tiebreaker for stable
// pagination; ls/find-style match_all sorts by _shard_doc.
func (c *ESClient) Search(ctx context.Context, req SearchRequest) (*SearchResult, error) {
	if len(req.Indices) == 0 {
		return nil, contract.Errf(contract.KindUsage, "no target index for search")
	}
	// Plan against the first index's caps; ESFS searches one scope at a time and
	// iterates indices for multi-index scopes.
	mode := req.Mode
	resolvedTimeField := ""
	type plannedIndex struct {
		index string
		query map[string]any
	}
	var planned []plannedIndex
	for _, idx := range req.Indices {
		caps, err := c.FieldCaps(ctx, idx)
		if err != nil {
			return nil, err
		}
		// Apply per-index configuration as defaults when the request did not
		// specify them explicitly.
		ri := c.cfg.ForIndex(idx)
		reqCopy := req
		if reqCopy.Mode == "" {
			reqCopy.Mode = ri.GrepMode
		}
		if reqCopy.TimeField == "" {
			reqCopy.TimeField = ri.TimeField
		}
		if len(reqCopy.SearchFields) == 0 {
			reqCopy.SearchFields = ri.SearchFields
		}
		if len(reqCopy.SemanticFields) == 0 {
			reqCopy.SemanticFields = ri.SemanticFields
		}
		pq, err := planQuery(reqCopy, caps, c.now())
		if err != nil {
			return nil, err
		}
		mode = pq.Mode
		resolvedTimeField = pq.TimeField
		planned = append(planned, plannedIndex{index: idx, query: pq.Query})
	}

	out := make(chan Hit)
	var ferr error
	done := make(chan struct{})
	go func() {
		defer close(out)
		defer close(done)
		sort := []any{map[string]any{"_score": "asc"}, map[string]any{"_shard_doc": "asc"}}
		if req.Query == "" {
			sort = []any{map[string]any{"_shard_doc": "asc"}}
		}
		for _, p := range planned {
			err := c.streamPIT(ctx, p.index, p.query, sort, req.WantSource, req.Limit, func(h searchHit) bool {
				c.rememberHashed(p.index, h.ID)
				var snips []string
				for _, v := range h.Highlight {
					snips = append(snips, v...)
				}
				hit := Hit{Index: p.index, ID: h.ID, Score: h.Score, Snippets: snips}
				if req.WantSource {
					hit.Source = h.Source
				}
				select {
				case <-ctx.Done():
					return false
				case out <- hit:
					return true
				}
			})
			if err != nil {
				ferr = err
				return
			}
		}
	}()

	return &SearchResult{
		Mode:      mode,
		TimeField: resolvedTimeField,
		Hits:      out,
		Err:       func() error { <-done; return ferr },
	}, nil
}
