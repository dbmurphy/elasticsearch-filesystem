package vfs

import (
	"context"
	"strings"
	"testing"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/contract"
	"github.com/dbmurphy/elasticsearch-filesystem/internal/escore"
)

func newTestVFS(t *testing.T, deleteSync bool) (*VFS, *escore.Fake) {
	t.Helper()
	f := escore.NewFake()
	f.DeleteEnabled = deleteSync
	cfg := escore.DefaultConfig()
	cfg.DeleteSync = deleteSync
	st := escore.NewSyncTracker(nil, escore.SyncPolicy{WritePolicy: "safe-create-update", DeleteSync: deleteSync})
	v := New(Deps{Store: f, Catalog: f, Lister: f, Searcher: f, Sync: st, Config: cfg})
	return v, f
}

func TestReadDocumentPretty(t *testing.T) {
	v, f := newTestVFS(t, false)
	f.Seed("conversations", "doc1", `{"msg":"hello","n":1}`)
	name, _ := contract.EncodeID("doc1")
	b, err := v.ReadAll(context.Background(), "conversations/"+name)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "\"msg\": \"hello\"") {
		t.Fatalf("expected pretty JSON, got %q", b)
	}
}

func TestStatMissingDocument(t *testing.T) {
	v, _ := newTestVFS(t, false)
	name, _ := contract.EncodeID("nope")
	_, err := v.Stat(context.Background(), "conversations/"+name)
	if contract.KindOf(err) != contract.KindNotFound {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestEditorOpenEditSaveSyncs(t *testing.T) {
	v, f := newTestVFS(t, false)
	f.Seed("conversations", "doc1", `{"msg":"hello"}`)
	name, _ := contract.EncodeID("doc1")
	rel := "conversations/" + name

	h, err := v.OpenWrite(context.Background(), rel, OpenFlags{Write: true})
	if err != nil {
		t.Fatal(err)
	}
	// Editor truncates then writes new content.
	h.Truncate(0)
	if _, err := h.WriteAt(0, []byte(`{"msg":"updated"}`+"\n")); err != nil {
		t.Fatal(err)
	}
	if err := h.Release(context.Background()); err != nil {
		t.Fatalf("release: %v", err)
	}
	doc, err := f.Get(context.Background(), "conversations", "doc1")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc.Source), "updated") {
		t.Fatalf("write not synced: %s", doc.Source)
	}
}

func TestInvalidJSONNotSynced(t *testing.T) {
	v, f := newTestVFS(t, false)
	f.Seed("conversations", "doc1", `{"msg":"hello"}`)
	name, _ := contract.EncodeID("doc1")
	rel := "conversations/" + name

	h, _ := v.OpenWrite(context.Background(), rel, OpenFlags{Write: true, Truncate: true})
	h.WriteAt(0, []byte(`{not valid`))
	err := h.Release(context.Background())
	if contract.KindOf(err) != contract.KindInvalidJSON {
		t.Fatalf("expected InvalidJSON, got %v", err)
	}
	doc, _ := f.Get(context.Background(), "conversations", "doc1")
	if !strings.Contains(string(doc.Source), "hello") {
		t.Fatalf("invalid write must not overwrite ES, got %s", doc.Source)
	}
}

func TestCreateOnlyExcl(t *testing.T) {
	v, f := newTestVFS(t, false)
	f.Seed("conversations", "existing", `{}`)
	name, _ := contract.EncodeID("existing")
	_, err := v.OpenWrite(context.Background(), "conversations/"+name, OpenFlags{Write: true, Create: true, Excl: true})
	if contract.KindOf(err) != contract.KindConflict {
		t.Fatalf("O_EXCL on existing should conflict, got %v", err)
	}

	// New doc with safe-create-update creates it.
	newName, _ := contract.EncodeID("brand-new")
	h, err := v.OpenWrite(context.Background(), "conversations/"+newName, OpenFlags{Write: true, Create: true})
	if err != nil {
		t.Fatal(err)
	}
	h.WriteAt(0, []byte(`{"created":true}`))
	if err := h.Release(context.Background()); err != nil {
		t.Fatalf("create release: %v", err)
	}
	if _, err := f.Get(context.Background(), "conversations", "brand-new"); err != nil {
		t.Fatalf("new doc not created: %v", err)
	}
}

func TestDeleteGating(t *testing.T) {
	v, f := newTestVFS(t, false)
	f.Seed("conversations", "doc1", `{}`)
	name, _ := contract.EncodeID("doc1")
	err := v.Unlink(context.Background(), "conversations/"+name)
	if contract.KindOf(err) != contract.KindReadOnly {
		t.Fatalf("delete should be gated off by default, got %v", err)
	}

	v2, f2 := newTestVFS(t, true)
	f2.Seed("conversations", "doc1", `{}`)
	if err := v2.Unlink(context.Background(), "conversations/"+name); err != nil {
		t.Fatalf("delete with sync enabled should work: %v", err)
	}
}

func TestReadDirRootAndIndex(t *testing.T) {
	v, f := newTestVFS(t, false)
	f.Seed("conversations", "a", `{}`)
	f.Seed("conversations", "b", `{}`)
	f.Seed("logs", "x", `{}`)

	root, err := v.ReadDir(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	names := entryNames(root)
	for _, want := range []string{"profile.md", ".sync.json", "conversations", "logs"} {
		if !contains(names, want) {
			t.Fatalf("root missing %q; got %v", want, names)
		}
	}

	idx, err := v.ReadDir(context.Background(), "conversations")
	if err != nil {
		t.Fatal(err)
	}
	inames := entryNames(idx)
	for _, want := range []string{"profile.md", ".sync.json", ".mapping.json", ".fields.json"} {
		if !contains(inames, want) {
			t.Fatalf("index dir missing virtual %q; got %v", want, inames)
		}
	}
	aName, _ := contract.EncodeID("a")
	if !contains(inames, aName) {
		t.Fatalf("index dir missing document %q; got %v", aName, inames)
	}
}

func TestSyncJSONReflectsWrite(t *testing.T) {
	v, f := newTestVFS(t, false)
	f.Seed("conversations", "doc1", `{"a":1}`)
	name, _ := contract.EncodeID("doc1")
	h, _ := v.OpenWrite(context.Background(), "conversations/"+name, OpenFlags{Write: true, Truncate: true})
	h.WriteAt(0, []byte(`{"a":2}`))
	h.Release(context.Background())

	b, err := v.ReadAll(context.Background(), "conversations/.sync.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "last_write") {
		t.Fatalf(".sync.json should record last_write: %s", b)
	}
}

func entryNames(es []DirEntry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Name
	}
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
