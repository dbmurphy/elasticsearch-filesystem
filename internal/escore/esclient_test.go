package escore

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/contract"
)

// fakeES is a minimal Elasticsearch stand-in covering the endpoints ESClient
// uses. It records whether any setup-style (mapping/template/pipeline) calls
// were made so tests can assert the no-ES-side-setup guarantee.
type fakeES struct {
	docs     map[string]json.RawMessage // id -> source
	seq      int64
	setupHit bool
}

func newFakeES() *fakeES { return &fakeES{docs: map[string]json.RawMessage{}} }

func (f *fakeES) handler() http.Handler {
	mux := http.NewServeMux()
	// Flag any setup-style endpoints.
	for _, p := range []string{"/_template/", "/_index_template/", "/_ingest/", "/_scripts/"} {
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			f.setupHit = true
			w.WriteHeader(400)
		})
	}
	mux.HandleFunc("/_resolve/index/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"indices":[{"name":"conversations"}],"aliases":[]}`))
	})
	mux.HandleFunc("/conversations/_field_caps", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"fields":{"body":{"text":{"type":"text","searchable":true,"aggregatable":false}},"@timestamp":{"date":{"type":"date","searchable":true,"aggregatable":true}}}}`))
	})
	mux.HandleFunc("/conversations/_pit", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"PIT123"}`))
	})
	mux.HandleFunc("/_pit", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"succeeded":true}`)) })
	mux.HandleFunc("/_search", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		// Return all docs once, then empty on search_after.
		if _, ok := body["search_after"]; ok {
			_, _ = w.Write([]byte(`{"pit_id":"PIT123","hits":{"hits":[]}}`))
			return
		}
		var hits []string
		for id, src := range f.docs {
			hits = append(hits, `{"_index":"conversations","_id":"`+id+`","_score":1.0,"_source":`+string(src)+`,"sort":[1,"`+id+`"]}`)
		}
		_, _ = w.Write([]byte(`{"pit_id":"PIT123","hits":{"hits":[` + strings.Join(hits, ",") + `]}}`))
	})
	mux.HandleFunc("/conversations/_doc/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/conversations/_doc/")
		switch r.Method {
		case "GET":
			src, ok := f.docs[id]
			if !ok {
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`{"found":false}`))
				return
			}
			_, _ = w.Write([]byte(`{"_index":"conversations","_id":"` + id + `","found":true,"_seq_no":` + itoa(f.seq) + `,"_primary_term":1,"_version":1,"_source":` + string(src) + `}`))
		case "PUT":
			var src json.RawMessage
			_ = json.NewDecoder(r.Body).Decode(&src)
			f.docs[id] = src
			f.seq++
			_, _ = w.Write([]byte(`{"_seq_no":` + itoa(f.seq) + `,"_primary_term":1,"_version":1}`))
		case "DELETE":
			delete(f.docs, id)
			_, _ = w.Write([]byte(`{"result":"deleted"}`))
		}
	})
	mux.HandleFunc("/conversations/_create/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/conversations/_create/")
		if _, ok := f.docs[id]; ok {
			w.WriteHeader(409)
			return
		}
		var src json.RawMessage
		_ = json.NewDecoder(r.Body).Decode(&src)
		f.docs[id] = src
		f.seq++
		w.WriteHeader(201)
		_, _ = w.Write([]byte(`{"_seq_no":` + itoa(f.seq) + `,"_primary_term":1}`))
	})
	return mux
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func newTestClient(t *testing.T, f *fakeES) *ESClient {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	cfg := DefaultConfig()
	cfg.Endpoint = srv.URL
	c, err := NewESClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestESClientGetPutDelete(t *testing.T) {
	f := newFakeES()
	c := newTestClient(t, f)
	ctx := context.Background()

	if _, err := c.Put(ctx, "conversations", "d1", json.RawMessage(`{"body":"hello"}`), PolicySafeCreateUpdate, WriteMeta{}); err != nil {
		t.Fatalf("put: %v", err)
	}
	doc, err := c.Get(ctx, "conversations", "d1")
	if err != nil || !doc.Found {
		t.Fatalf("get: %v found=%v", err, doc.Found)
	}
	if !strings.Contains(string(doc.Source), "hello") {
		t.Fatalf("unexpected source %s", doc.Source)
	}

	// Create-only conflict.
	_, err = c.Put(ctx, "conversations", "d1", json.RawMessage(`{"x":1}`), PolicyCreateOnly, WriteMeta{})
	if contract.KindOf(err) != contract.KindConflict {
		t.Fatalf("expected conflict, got %v", err)
	}

	if err := c.Delete(ctx, "conversations", "d1", WriteMeta{}); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestESClientListStreams(t *testing.T) {
	f := newFakeES()
	f.docs["a"] = json.RawMessage(`{"body":"x"}`)
	f.docs["b"] = json.RawMessage(`{"body":"y"}`)
	c := newTestClient(t, f)
	ch, errf := c.List(context.Background(), "conversations")
	var ids []string
	for it := range ch {
		ids = append(ids, it.ID)
	}
	if err := errf(); err != nil {
		t.Fatalf("list err: %v", err)
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 ids, got %v", ids)
	}
}

func TestESClientSearchAndNoSetup(t *testing.T) {
	f := newFakeES()
	f.docs["a"] = json.RawMessage(`{"body":"refund please"}`)
	c := newTestClient(t, f)
	res, err := c.Search(context.Background(), SearchRequest{Indices: []string{"conversations"}, Query: "refund"})
	if err != nil {
		t.Fatal(err)
	}
	var got int
	for range res.Hits {
		got++
	}
	if err := res.Err(); err != nil {
		t.Fatalf("search err: %v", err)
	}
	if got != 1 {
		t.Fatalf("expected 1 hit, got %d", got)
	}
	if res.Mode != ModeLexical {
		t.Fatalf("expected lexical mode, got %s", res.Mode)
	}
	if f.setupHit {
		t.Fatal("search must not hit any ES setup endpoints")
	}
}
