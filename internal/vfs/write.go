package vfs

import (
	"context"
	"encoding/json"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/contract"
	"github.com/dbmurphy/elasticsearch-filesystem/internal/escore"
)

// OpenFlags is a backend-neutral subset of POSIX open flags the VFS honors.
type OpenFlags struct {
	Write    bool
	Create   bool
	Excl     bool // O_EXCL: create-only
	Truncate bool // O_TRUNC: start from empty buffer
}

// Handle is an open writable document handle holding a local staging buffer.
// Content is validated and pushed to Elasticsearch only on Flush/Release, per
// the editor-save contract; invalid intermediate JSON never reaches ES.
type Handle struct {
	v          *VFS
	index, id  string
	buf        []byte
	dirty      bool
	createOnly bool
	meta       escore.WriteMeta
	policy     escore.WritePolicy
	flushedErr error
}

// OpenWrite opens a document path for writing, staging current content unless
// truncating or creating.
func (v *VFS) OpenWrite(ctx context.Context, rel string, fl OpenFlags) (*Handle, error) {
	n := contract.ParsePath(rel)
	if n.Kind != contract.NodeDocument {
		return nil, contract.Errf(contract.KindUnsupported, "%q is not a writable document path", rel)
	}
	id, err := v.resolveDocID(n)
	if err != nil {
		// A brand-new document referenced by a raw name is allowed when creating.
		if n.HashedName != "" || !fl.Create {
			return nil, err
		}
		id = n.ID
	}
	ri := v.d.Config.ForIndex(n.Index)
	h := &Handle{v: v, index: n.Index, id: id, policy: ri.WritePolicy}

	// Verify the index accepts writes (single concrete write target).
	info, err := v.d.Catalog.IndexInfo(ctx, n.Index)
	if err != nil {
		return nil, err
	}
	if info.IsAlias && info.WriteTarget == "" {
		return nil, contract.Errf(contract.KindUnsupported, "alias %q has no single write index; writes are not allowed", n.Index)
	}

	existing, getErr := v.d.Store.Get(ctx, n.Index, id)
	switch {
	case fl.Excl:
		if getErr == nil && existing.Found {
			return nil, contract.Errf(contract.KindConflict, "document %s/%s already exists", n.Index, id)
		}
		h.createOnly = true
	case getErr == nil && existing.Found && !fl.Truncate:
		// open-edit-save: stage current pretty content and capture OCC metadata.
		b, perr := prettyJSON(existing.Source)
		if perr == nil {
			h.buf = b
		}
		h.meta = escore.WriteMeta{SeqNo: existing.SeqNo, PrimaryTerm: existing.PrimaryTerm}
	case getErr == nil && existing.Found && fl.Truncate:
		h.meta = escore.WriteMeta{SeqNo: existing.SeqNo, PrimaryTerm: existing.PrimaryTerm}
	default:
		// Absent path: safe-create-update creates; create-only enforced below.
		if fl.Create && ri.WritePolicy == escore.PolicySafeCreateUpdate {
			h.createOnly = true
		}
	}
	v.d.Sync.SetPending(n.Index, pendingDelta(1))
	return h, nil
}

// pendingDelta is a placeholder hook; pending tracking is best-effort here.
func pendingDelta(n int) int { return n }

// Truncate resizes the staging buffer.
func (h *Handle) Truncate(size int64) {
	if size < 0 {
		size = 0
	}
	if int64(len(h.buf)) > size {
		h.buf = h.buf[:size]
	} else {
		for int64(len(h.buf)) < size {
			h.buf = append(h.buf, 0)
		}
	}
	h.dirty = true
}

// WriteAt writes data at off into the staging buffer.
func (h *Handle) WriteAt(off int64, data []byte) (int, error) {
	if off < 0 {
		return 0, contract.Errf(contract.KindInvalidJSON, "negative offset")
	}
	end := off + int64(len(data))
	for int64(len(h.buf)) < end {
		h.buf = append(h.buf, 0)
	}
	copy(h.buf[off:end], data)
	h.dirty = true
	return len(data), nil
}

// Bytes exposes the current staging buffer (for tests).
func (h *Handle) Bytes() []byte { return h.buf }

// Flush validates and pushes the staged content to Elasticsearch. It is safe to
// call multiple times; only a dirty buffer is pushed.
func (h *Handle) Flush(ctx context.Context) error {
	if !h.dirty {
		return nil
	}
	src := normalizeJSON(h.buf)
	if !json.Valid(src) {
		err := contract.Errf(contract.KindInvalidJSON, "staged content for %s/%s is not valid JSON; not synced", h.index, h.id)
		h.v.d.Sync.RecordError(h.index, err)
		h.flushedErr = err
		return err
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(src, &obj); err != nil {
		e := contract.Errf(contract.KindInvalidJSON, "staged content for %s/%s must be a JSON object; not synced", h.index, h.id)
		h.v.d.Sync.RecordError(h.index, e)
		h.flushedErr = e
		return e
	}
	pol := h.policy
	if h.createOnly {
		pol = escore.PolicyCreateOnly
	}
	doc, err := h.v.d.Store.Put(ctx, h.index, h.id, json.RawMessage(src), pol, h.meta)
	if err != nil {
		h.v.d.Sync.RecordError(h.index, err)
		h.flushedErr = err
		return err
	}
	// Re-capture OCC metadata so a subsequent write in the same handle succeeds.
	h.meta = escore.WriteMeta{SeqNo: doc.SeqNo, PrimaryTerm: doc.PrimaryTerm}
	h.createOnly = false
	h.dirty = false
	h.flushedErr = nil
	h.v.d.Sync.RecordWrite(h.index)
	return nil
}

// Release finalizes the handle, flushing any pending content.
func (h *Handle) Release(ctx context.Context) error {
	defer h.v.d.Sync.SetPending(h.index, 0)
	return h.Flush(ctx)
}

// Unlink deletes a document (gated by delete sync).
func (v *VFS) Unlink(ctx context.Context, rel string) error {
	n := contract.ParsePath(rel)
	if n.Kind != contract.NodeDocument {
		return contract.Errf(contract.KindUnsupported, "%q is not a deletable document", rel)
	}
	if !v.d.Config.ForIndex(n.Index).DeleteSync {
		return contract.Errf(contract.KindReadOnly, "delete sync is disabled for index %q; enable it per profile to remove documents", n.Index)
	}
	id, err := v.resolveDocID(n)
	if err != nil {
		return err
	}
	if err := v.d.Store.Delete(ctx, n.Index, id, escore.WriteMeta{}); err != nil {
		v.d.Sync.RecordError(n.Index, err)
		return err
	}
	v.d.Sync.RecordWrite(n.Index)
	return nil
}

// Rename rejects document-ID mutation; only an identical-path no-op is allowed,
// which supports some editors' same-target save dance.
func (v *VFS) Rename(_ context.Context, oldRel, newRel string) error {
	if oldRel == newRel {
		return nil
	}
	return contract.Errf(contract.KindUnsupported,
		"rename to a different document ID is unsupported; create the new ID and delete the old one")
}

// normalizeJSON trims trailing NULs introduced by truncate/seek padding and
// surrounding whitespace so editor saves with trailing newlines validate.
func normalizeJSON(b []byte) []byte {
	// Trim trailing NUL bytes.
	end := len(b)
	for end > 0 && b[end-1] == 0 {
		end--
	}
	return b[:end]
}
