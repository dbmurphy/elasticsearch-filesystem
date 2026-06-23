package vfs

import (
	"context"
	"strings"
	"testing"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/contract"
	"github.com/dbmurphy/elasticsearch-filesystem/internal/escore"
)

// These tests replay the exact open/write/close syscall sequence the shell and
// `cat` produce for redirection, which is what the FUSE adapter forwards to the
// VFS. They demonstrate that `cat foo > /esfs/<index>/<id>` and
// `echo ... >> ...`-style flows create/overwrite Elasticsearch documents
// through the mount with no shim involvement.

// simulateRedirect mimics `> path`: open(O_WRONLY|O_CREAT|O_TRUNC), write the
// payload in chunks, close.
func simulateRedirect(t *testing.T, v *VFS, rel string, payload []byte, excl bool) error {
	t.Helper()
	h, err := v.OpenWrite(context.Background(), rel, OpenFlags{
		Write: true, Create: true, Truncate: true, Excl: excl,
	})
	if err != nil {
		return err
	}
	// cat writes in (possibly multiple) sequential chunks.
	const chunk = 8
	off := int64(0)
	for len(payload) > 0 {
		n := chunk
		if n > len(payload) {
			n = len(payload)
		}
		if _, err := h.WriteAt(off, payload[:n]); err != nil {
			return err
		}
		off += int64(n)
		payload = payload[n:]
	}
	return h.Release(context.Background())
}

func TestCatRedirectCreatesDocument(t *testing.T) {
	v, f := newTestVFS(t, false)
	// New document id that does not exist yet.
	name, _ := contract.EncodeID("new-doc")
	rel := "conversations/" + name

	// Need the index to be visible for write-target resolution; seed a sibling.
	f.Seed("conversations", "seed", `{}`)

	if err := simulateRedirect(t, v, rel, []byte(`{"from":"cat redirect"}`), false); err != nil {
		t.Fatalf("redirect write failed: %v", err)
	}
	doc, err := f.Get(context.Background(), "conversations", "new-doc")
	if err != nil || !doc.Found {
		t.Fatalf("document not created via redirect: %v", err)
	}
	if !strings.Contains(string(doc.Source), "cat redirect") {
		t.Fatalf("unexpected synced content: %s", doc.Source)
	}
}

func TestCatRedirectOverwritesDocument(t *testing.T) {
	v, f := newTestVFS(t, false)
	f.Seed("conversations", "doc1", `{"v":1}`)
	name, _ := contract.EncodeID("doc1")
	rel := "conversations/" + name

	if err := simulateRedirect(t, v, rel, []byte(`{"v":2,"note":"overwritten"}`), false); err != nil {
		t.Fatalf("overwrite redirect failed: %v", err)
	}
	doc, _ := f.Get(context.Background(), "conversations", "doc1")
	if !strings.Contains(string(doc.Source), "overwritten") {
		t.Fatalf("redirect did not overwrite document: %s", doc.Source)
	}
}

func TestCatRedirectNoclobberExcl(t *testing.T) {
	v, f := newTestVFS(t, false)
	f.Seed("conversations", "doc1", `{"v":1}`)
	name, _ := contract.EncodeID("doc1")
	rel := "conversations/" + name

	// `set -o noclobber; cat foo > existing` uses O_EXCL and must fail.
	err := simulateRedirect(t, v, rel, []byte(`{"v":2}`), true)
	if contract.KindOf(err) != contract.KindConflict {
		t.Fatalf("noclobber redirect over existing should conflict, got %v", err)
	}
}

func TestCatRedirectNonJSONRejected(t *testing.T) {
	v, f := newTestVFS(t, false)
	f.Seed("conversations", "seed", `{}`)
	name, _ := contract.EncodeID("plain")
	rel := "conversations/" + name

	// Redirecting a non-JSON-object file is rejected and not synced, because an
	// ESFS document is a JSON object.
	err := simulateRedirect(t, v, rel, []byte("just some text\n"), false)
	if contract.KindOf(err) != contract.KindInvalidJSON {
		t.Fatalf("non-JSON redirect should fail with InvalidJSON, got %v", err)
	}
	if _, gerr := f.Get(context.Background(), "conversations", "plain"); contract.KindOf(gerr) != contract.KindNotFound {
		t.Fatalf("non-JSON redirect must not create a document")
	}
}

var _ = escore.PolicySafeCreateUpdate
