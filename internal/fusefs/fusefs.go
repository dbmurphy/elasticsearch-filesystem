// Package fusefs adapts the logical VFS to a real FUSE mount via go-fuse. It is
// intentionally thin: all filesystem semantics live in package vfs, so this
// layer only translates kernel operations into VFS calls and maps errors to
// errno. Mounting requires fuse3 (Linux) or macFUSE/FUSE-T (macOS) at runtime;
// the package compiles regardless.
package fusefs

import (
	"context"
	"syscall"
	"time"

	"github.com/dbmurphy/elasticsearch-filesystem/internal/contract"
	"github.com/dbmurphy/elasticsearch-filesystem/internal/vfs"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

// node is a single ESFS inode identified by its mount-relative path.
type node struct {
	fs.Inode
	v   *vfs.VFS
	rel string // mount-relative slash path; "" is the root
}

var (
	_ fs.NodeGetattrer = (*node)(nil)
	_ fs.NodeLookuper  = (*node)(nil)
	_ fs.NodeReaddirer = (*node)(nil)
	_ fs.NodeOpener    = (*node)(nil)
	_ fs.NodeCreater   = (*node)(nil)
	_ fs.NodeUnlinker  = (*node)(nil)
	_ fs.NodeRenamer   = (*node)(nil)
	_ fs.NodeSetattrer = (*node)(nil)
)

func child(parent, name string) string {
	if parent == "" {
		return name
	}
	return parent + "/" + name
}

func errno(err error) syscall.Errno {
	if err == nil {
		return 0
	}
	return contract.Errno(err)
}

func (n *node) Getattr(ctx context.Context, _ fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	fi, err := n.v.Stat(ctx, n.rel)
	if err != nil {
		return errno(err)
	}
	if fi.IsDir {
		out.Mode = fuse.S_IFDIR | fi.Mode
	} else {
		out.Mode = fuse.S_IFREG | fi.Mode
		out.Size = uint64(fi.Size)
	}
	return 0
}

func (n *node) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	rel := child(n.rel, name)
	fi, err := n.v.Stat(ctx, rel)
	if err != nil {
		return nil, errno(err)
	}
	mode := uint32(fuse.S_IFREG)
	if fi.IsDir {
		mode = fuse.S_IFDIR
	}
	out.Mode = mode | fi.Mode
	out.Size = uint64(fi.Size)
	ch := n.NewInode(ctx, &node{v: n.v, rel: rel}, fs.StableAttr{Mode: mode})
	return ch, 0
}

func (n *node) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	cctx, cancel := context.WithCancel(ctx)
	ch, errf, err := n.v.ReadDirStream(cctx, n.rel)
	if err != nil {
		cancel()
		return nil, errno(err)
	}
	return &dirStream{ch: ch, errf: errf, cancel: cancel}, 0
}

func (n *node) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	writable := flags&(syscall.O_WRONLY|syscall.O_RDWR) != 0
	if !writable {
		data, err := n.v.ReadAll(ctx, n.rel)
		if err != nil {
			return nil, 0, errno(err)
		}
		return &fileHandle{data: data}, fuse.FOPEN_DIRECT_IO, 0
	}
	h, err := n.v.OpenWrite(ctx, n.rel, vfs.OpenFlags{
		Write:    true,
		Create:   flags&syscall.O_CREAT != 0,
		Excl:     flags&syscall.O_EXCL != 0,
		Truncate: flags&syscall.O_TRUNC != 0,
	})
	if err != nil {
		return nil, 0, errno(err)
	}
	return &fileHandle{w: h}, fuse.FOPEN_DIRECT_IO, 0
}

func (n *node) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	rel := child(n.rel, name)
	h, err := n.v.OpenWrite(ctx, rel, vfs.OpenFlags{
		Write:    true,
		Create:   true,
		Excl:     flags&syscall.O_EXCL != 0,
		Truncate: true,
	})
	if err != nil {
		return nil, nil, 0, errno(err)
	}
	out.Mode = fuse.S_IFREG | 0o644
	ch := n.NewInode(ctx, &node{v: n.v, rel: rel}, fs.StableAttr{Mode: fuse.S_IFREG})
	return ch, &fileHandle{w: h}, fuse.FOPEN_DIRECT_IO, 0
}

func (n *node) Setattr(ctx context.Context, fh fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	if sz, ok := in.GetSize(); ok {
		if h, ok := fh.(*fileHandle); ok && h.w != nil {
			h.w.Truncate(int64(sz))
			out.Size = sz
			return 0
		}
	}
	// Permit no-op metadata setattr so editors don't fail; ESFS ignores it.
	return 0
}

func (n *node) Unlink(ctx context.Context, name string) syscall.Errno {
	return errno(n.v.Unlink(ctx, child(n.rel, name)))
}

func (n *node) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, _ uint32) syscall.Errno {
	np, ok := newParent.(*node)
	if !ok {
		return syscall.EXDEV
	}
	return errno(n.v.Rename(ctx, child(n.rel, name), child(np.rel, newName)))
}

// fileHandle backs both read-only (data) and writable (w) opens.
type fileHandle struct {
	data []byte
	w    *vfs.Handle
}

var (
	_ fs.FileReader   = (*fileHandle)(nil)
	_ fs.FileWriter   = (*fileHandle)(nil)
	_ fs.FileFlusher  = (*fileHandle)(nil)
	_ fs.FileReleaser = (*fileHandle)(nil)
)

func (h *fileHandle) Read(_ context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	if off >= int64(len(h.data)) {
		return fuse.ReadResultData(nil), 0
	}
	end := off + int64(len(dest))
	if end > int64(len(h.data)) {
		end = int64(len(h.data))
	}
	return fuse.ReadResultData(h.data[off:end]), 0
}

func (h *fileHandle) Write(_ context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	if h.w == nil {
		return 0, syscall.EBADF
	}
	nn, err := h.w.WriteAt(off, data)
	if err != nil {
		return 0, errno(err)
	}
	return uint32(nn), 0
}

func (h *fileHandle) Flush(ctx context.Context) syscall.Errno {
	if h.w == nil {
		return 0
	}
	return errno(h.w.Flush(ctx))
}

func (h *fileHandle) Release(ctx context.Context) syscall.Errno {
	if h.w == nil {
		return 0
	}
	return errno(h.w.Release(ctx))
}

// dirStream adapts a VFS entry channel to fs.DirStream with backpressure.
type dirStream struct {
	ch     <-chan vfs.DirEntry
	errf   func() error
	cancel context.CancelFunc
	next   *vfs.DirEntry
	done   bool
}

func (d *dirStream) HasNext() bool {
	if d.next != nil {
		return true
	}
	if d.done {
		return false
	}
	e, ok := <-d.ch
	if !ok {
		d.done = true
		return false
	}
	d.next = &e
	return true
}

func (d *dirStream) Next() (fuse.DirEntry, syscall.Errno) {
	e := d.next
	d.next = nil
	mode := uint32(fuse.S_IFREG)
	if e.IsDir {
		mode = fuse.S_IFDIR
	}
	return fuse.DirEntry{Name: e.Name, Mode: mode}, 0
}

func (d *dirStream) Close() {
	d.cancel()
}

// Mount mounts the VFS at mountpoint and blocks until unmounted. It returns the
// server so callers can request unmount; on success it serves in the calling
// goroutine.
func Mount(v *vfs.VFS, mountpoint string, debug bool) (*fuse.Server, error) {
	root := &node{v: v}
	opts := &fs.Options{}
	opts.MountOptions.Name = "esfs"
	opts.MountOptions.FsName = "esfs"
	opts.MountOptions.Debug = debug
	opts.EntryTimeout = durptr(time.Second)
	opts.AttrTimeout = durptr(time.Second)
	server, err := fs.Mount(mountpoint, root, opts)
	if err != nil {
		return nil, contract.Wrap(contract.KindInternal, err, "mount %s", mountpoint)
	}
	return server, nil
}

func durptr(d time.Duration) *time.Duration { return &d }
