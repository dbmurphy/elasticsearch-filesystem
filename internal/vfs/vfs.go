// Package vfs is the logical ESFS filesystem. It implements every filesystem
// semantic (stat, streaming readdir, document read, editor-save staging,
// flush-to-Elasticsearch, unlink, virtual profile/sync/diagnostic files) in
// terms of the escore interfaces. It contains no FUSE/kernel code so it is
// fully unit-testable; the FUSE adapter (package fusefs) is a thin shim over it.
package vfs

import (
	"bytes"
	"context"
	"encoding/json"
	"time"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/contract"
	"github.com/dbmurphy/elasticsearch-filesystem/internal/escore"
)

// Deps wires the VFS to the shared core.
type Deps struct {
	Store    escore.Store
	Catalog  escore.Catalog
	Lister   escore.Lister
	Searcher escore.Searcher
	Profiles escore.ProfileGenerator
	Sync     *escore.SyncTracker
	Config   escore.Config
	Now      func() time.Time
}

// VFS is the logical filesystem.
type VFS struct {
	d Deps
}

// New builds a VFS.
func New(d Deps) *VFS {
	if d.Now == nil {
		d.Now = time.Now
	}
	return &VFS{d: d}
}

// FileInfo describes a stat result.
type FileInfo struct {
	IsDir bool
	Size  int64
	Mode  uint32 // unix permission bits (without type bits)
}

// DirEntry is a single readdir entry.
type DirEntry struct {
	Name  string
	IsDir bool
}

const (
	dirMode  = 0o755
	fileMode = 0o644
	roMode   = 0o444
)

// Stat classifies and sizes a mount-relative path.
func (v *VFS) Stat(ctx context.Context, rel string) (FileInfo, error) {
	n := contract.ParsePath(rel)
	switch n.Kind {
	case contract.NodeRoot, contract.NodeIndexDir:
		if n.Kind == contract.NodeIndexDir {
			if _, err := v.d.Catalog.IndexInfo(ctx, n.Index); err != nil {
				return FileInfo{}, err
			}
		}
		return FileInfo{IsDir: true, Mode: dirMode}, nil
	case contract.NodeMountProfile, contract.NodeIndexProfile,
		contract.NodeMountSync, contract.NodeIndexSync,
		contract.NodeIndexMapping, contract.NodeIndexFields:
		b, err := v.virtualContent(ctx, n)
		if err != nil {
			return FileInfo{}, err
		}
		return FileInfo{Size: int64(len(b)), Mode: roMode}, nil
	case contract.NodeDocument:
		b, err := v.renderDocument(ctx, n)
		if err != nil {
			return FileInfo{}, err
		}
		return FileInfo{Size: int64(len(b)), Mode: fileMode}, nil
	default:
		return FileInfo{}, contract.Errf(contract.KindNotFound, "no such path %q", rel)
	}
}

// ReadAll returns the full byte content for any readable file node.
func (v *VFS) ReadAll(ctx context.Context, rel string) ([]byte, error) {
	n := contract.ParsePath(rel)
	switch n.Kind {
	case contract.NodeDocument:
		return v.renderDocument(ctx, n)
	case contract.NodeMountProfile, contract.NodeIndexProfile,
		contract.NodeMountSync, contract.NodeIndexSync,
		contract.NodeIndexMapping, contract.NodeIndexFields:
		return v.virtualContent(ctx, n)
	default:
		return nil, contract.Errf(contract.KindNotFound, "not a file: %q", rel)
	}
}

// ReadDir lists a directory by collecting the stream. Callers that must avoid
// materializing huge indices should use ReadDirStream.
func (v *VFS) ReadDir(ctx context.Context, rel string) ([]DirEntry, error) {
	ch, errf, err := v.ReadDirStream(ctx, rel)
	if err != nil {
		return nil, err
	}
	var out []DirEntry
	for e := range ch {
		out = append(out, e)
	}
	if errf != nil {
		if e := errf(); e != nil {
			return out, e
		}
	}
	return out, nil
}

// ReadDirStream streams directory entries. For an index it streams document
// names via the Lister (PIT/search_after under the real client) plus virtual
// files; for the root it lists visible indices plus virtual files.
func (v *VFS) ReadDirStream(ctx context.Context, rel string) (<-chan DirEntry, func() error, error) {
	n := contract.ParsePath(rel)
	switch n.Kind {
	case contract.NodeRoot:
		indices, err := v.d.Catalog.VisibleIndices(ctx)
		if err != nil {
			return nil, nil, err
		}
		ch := make(chan DirEntry)
		go func() {
			defer close(ch)
			emit(ctx, ch, DirEntry{Name: contract.FileProfile})
			emit(ctx, ch, DirEntry{Name: contract.FileSync})
			for _, idx := range indices {
				if !emit(ctx, ch, DirEntry{Name: idx.Name, IsDir: true}) {
					return
				}
			}
		}()
		return ch, func() error { return nil }, nil
	case contract.NodeIndexDir:
		if _, err := v.d.Catalog.IndexInfo(ctx, n.Index); err != nil {
			return nil, nil, err
		}
		items, listErr := v.d.Lister.List(ctx, n.Index)
		ch := make(chan DirEntry)
		go func() {
			defer close(ch)
			emit(ctx, ch, DirEntry{Name: contract.FileProfile})
			emit(ctx, ch, DirEntry{Name: contract.FileSync})
			emit(ctx, ch, DirEntry{Name: contract.FileMapping})
			emit(ctx, ch, DirEntry{Name: contract.FileFields})
			for it := range items {
				name, _ := contract.EncodeID(it.ID)
				if !emit(ctx, ch, DirEntry{Name: name}) {
					return
				}
			}
		}()
		return ch, listErr, nil
	default:
		return nil, nil, contract.Errf(contract.KindNotFound, "not a directory: %q", rel)
	}
}

func emit(ctx context.Context, ch chan<- DirEntry, e DirEntry) bool {
	select {
	case <-ctx.Done():
		return false
	case ch <- e:
		return true
	}
}

// resolveDocID resolves the document ID for a document node, consulting the
// hashed reverse map when the node was exposed via a hashed short name.
func (v *VFS) resolveDocID(n contract.Node) (string, error) {
	if n.HashedName == "" {
		return n.ID, nil
	}
	id, ok := v.d.Store.ResolveHashed(n.Index, n.HashedName)
	if !ok {
		return "", contract.Errf(contract.KindNotFound, "unknown hashed name %q in %q", n.HashedName, n.Index)
	}
	return id, nil
}

// renderDocument fetches and pretty-prints a document's _source.
func (v *VFS) renderDocument(ctx context.Context, n contract.Node) ([]byte, error) {
	id, err := v.resolveDocID(n)
	if err != nil {
		return nil, err
	}
	doc, err := v.d.Store.Get(ctx, n.Index, id)
	if err != nil {
		return nil, err
	}
	if !doc.Found {
		return nil, contract.Errf(contract.KindNotFound, "document %s/%s not found", n.Index, id)
	}
	return prettyJSON(doc.Source)
}

func prettyJSON(src json.RawMessage) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Indent(&buf, src, "", "  "); err != nil {
		// Source should be valid JSON from ES; if not, return as-is.
		return append([]byte(src), '\n'), nil
	}
	buf.WriteByte('\n')
	return buf.Bytes(), nil
}

func (v *VFS) virtualContent(ctx context.Context, n contract.Node) ([]byte, error) {
	switch n.Kind {
	case contract.NodeMountProfile:
		if v.d.Profiles == nil {
			return []byte("# ESFS\n\nprofile generation disabled\n"), nil
		}
		return v.d.Profiles.MountProfile(ctx)
	case contract.NodeIndexProfile:
		if v.d.Profiles == nil {
			return []byte("# ESFS index\n\nprofile generation disabled\n"), nil
		}
		return v.d.Profiles.IndexProfile(ctx, n.Index)
	case contract.NodeMountSync:
		return v.d.Sync.MountJSON(), nil
	case contract.NodeIndexSync:
		return v.d.Sync.IndexJSON(n.Index), nil
	case contract.NodeIndexMapping:
		m, err := v.d.Catalog.Mapping(ctx, n.Index)
		if err != nil {
			return nil, err
		}
		return prettyJSON(m)
	case contract.NodeIndexFields:
		fc, err := v.d.Catalog.FieldCaps(ctx, n.Index)
		if err != nil {
			return nil, err
		}
		b, _ := json.MarshalIndent(fc, "", "  ")
		return append(b, '\n'), nil
	default:
		return nil, contract.Errf(contract.KindNotFound, "not a virtual file")
	}
}
