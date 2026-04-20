//go:build linux && substrate_fuse_v2

// hanwen/go-fuse/v2 Linux backend for the substrate's process broker
// projection.
//
// Compiled when the `substrate_fuse_v2` build tag is set. Otherwise the
// bazil/fuse backend in process_broker_linux_bazil.go is used.
//
// All FUSE-library-agnostic Linux broker code lives in
// process_broker_linux_common.go; this file contains only the parts
// that talk to the FUSE library directly.
//
// Why two backends:
//   - bazil/fuse is the legacy implementation. The repository has been
//     using it stably for some time but the upstream is functionally
//     unmaintained (last release activity ~16 months stale at the time
//     of this migration). It will be retired one release after
//     substrate_fuse_v2 becomes the default.
//   - hanwen/go-fuse/v2 is the active upstream, tracks the FUSE wire
//     protocol through 7.12.28, ships passthrough mode for near-native
//     read perf, and is in production at Tailscale, GCSFuse, and others.
//
// The interface exposed to the rest of the broker is identical between
// backends: mountExecutionRoot returns a mountedExecutionRoot whose
// Root() and Close() the broker uses.

package purevfs

import (
	"context"
	"errors"
	"io"
	stdfs "io/fs"
	"os"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

const (
	// fuseDirMode is the StableAttr.Mode value for directory inodes.
	// Hanwen uses the standard S_IFDIR bit pattern (0o040000) to
	// distinguish directories at the inode level; this is independent
	// of the access-permission bits set per-attr.
	fuseDirMode = syscall.S_IFDIR

	// fuseFileMode marks regular file inodes. Hanwen's default (0)
	// already means S_IFREG, but being explicit avoids a future-self
	// "wait, why is this zero" moment.
	fuseFileMode = syscall.S_IFREG
)

// linuxHanwenRoot is the mountedExecutionRoot returned by
// mountExecutionRoot. It owns the mountpoint and the fuse.Server, and
// tears both down on Close.
type linuxHanwenRoot struct {
	mountpoint string
	server     *fuse.Server
}

// linuxHanwenNode is the per-inode embedder. One instance backs each
// Inode the kernel sees: the FUSE root (path="/"), per-directory nodes
// (path="/some/dir"), and per-file nodes (path="/some/file.txt").
//
// Both directory and file nodes use the same struct; the inode's
// StableAttr.Mode (set when the inode is materialized) carries the
// type. Operation interfaces (NodeMkdirer, NodeOpener, etc.) are
// attached as methods on this single type — the Inode's stable mode
// makes the runtime dispatch correct without splitting the type.
type linuxHanwenNode struct {
	fs.Inode

	root *projectedRoot
	path string
}

// linuxHanwenHandle is one open FileHandle. It is returned from Open
// and Create, and the kernel routes Read/Write/Flush/Release on the
// open file directly to it.
type linuxHanwenHandle struct {
	root *projectedRoot
	id   uint64
}

// Compile-time interface checks. Each interface listed here is one the
// hanwen library dispatches on; missing one silently falls back to the
// library's default behavior (which for many operations is "return
// ENOSYS"), so the assertions catch accidental method-signature drift
// at build time.
var (
	_ fs.NodeGetattrer = (*linuxHanwenNode)(nil)
	_ fs.NodeLookuper  = (*linuxHanwenNode)(nil)
	_ fs.NodeReaddirer = (*linuxHanwenNode)(nil)
	_ fs.NodeMkdirer   = (*linuxHanwenNode)(nil)
	_ fs.NodeUnlinker  = (*linuxHanwenNode)(nil)
	_ fs.NodeRmdirer   = (*linuxHanwenNode)(nil)
	_ fs.NodeRenamer   = (*linuxHanwenNode)(nil)
	_ fs.NodeOpener    = (*linuxHanwenNode)(nil)
	_ fs.NodeCreater   = (*linuxHanwenNode)(nil)
	_ fs.NodeSetattrer = (*linuxHanwenNode)(nil)

	_ fs.FileReader   = (*linuxHanwenHandle)(nil)
	_ fs.FileWriter   = (*linuxHanwenHandle)(nil)
	_ fs.FileFlusher  = (*linuxHanwenHandle)(nil)
	_ fs.FileReleaser = (*linuxHanwenHandle)(nil)
)

// mountExecutionRoot mounts a hanwen-backed FUSE filesystem against the
// given projectedRoot and returns a mountedExecutionRoot that wraps the
// resulting mountpoint and fuse.Server.
//
// The mountpoint is allocated under /dev/shm (per the existing
// no-disk-tmpfs invariant; /dev/shm is the only RAM-backed filesystem
// the broker accepts as a mountpoint host).
func mountExecutionRoot(_ context.Context, _ BrokerRunRequest, root *projectedRoot) (mountedExecutionRoot, error) {
	mountpoint, err := newLinuxMountpoint()
	if err != nil {
		return nil, err
	}
	server, err := mountHanwenServer(mountpoint, root)
	if err != nil {
		_ = os.Remove(mountpoint)
		return nil, err
	}
	return &linuxHanwenRoot{
		mountpoint: mountpoint,
		server:     server,
	}, nil
}

func mountHanwenServer(mountpoint string, root *projectedRoot) (*fuse.Server, error) {
	rootNode := &linuxHanwenNode{root: root, path: "/"}
	return fs.Mount(mountpoint, rootNode, hanwenMountOptions())
}

func hanwenMountOptions() *fs.Options {
	entryTimeout := defaultHanwenEntryTimeout
	attrTimeout := defaultHanwenAttrTimeout
	return &fs.Options{
		EntryTimeout: &entryTimeout,
		AttrTimeout:  &attrTimeout,
		MountOptions: fuse.MountOptions{
			FsName:        "sylk-purevfs",
			Name:          "sylk",
			AllowOther:    false,
			DirectMount:   false,
			DisableXAttrs: true,
		},
	}
}

// Hanwen's entry/attr cache timeouts. We want short timeouts because
// the substrate's projection layer can mutate underneath us (workspace
// writes, manifest attaches, view recompositions); a stale cache could
// surface phantom files to the running process. 100ms is short enough
// to feel "fresh" to a typical exec workload while amortizing the
// per-attr lookup cost across short bursts of identical reads.
const (
	defaultHanwenEntryTimeout = 100 * time.Millisecond
	defaultHanwenAttrTimeout  = 100 * time.Millisecond
)

func (m *linuxHanwenRoot) Root() string { return m.mountpoint }

// Close unmounts the filesystem and removes the mountpoint directory.
func (m *linuxHanwenRoot) Close() error {
	var firstErr error
	if err := m.server.Unmount(); err != nil && !errors.Is(err, syscall.EINVAL) {
		firstErr = err
	}
	if err := os.Remove(m.mountpoint); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

// ── Node operations ────────────────────────────────────────────────

func (n *linuxHanwenNode) Getattr(ctx context.Context, _ fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	info, err := n.root.Stat(ctx, n.path)
	if err != nil {
		return errnoFromError(err)
	}
	fillHanwenAttr(&out.Attr, n.path, info)
	return fs.OK
}

func (n *linuxHanwenNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	childPath := childExecPath(n.path, name)
	info, err := n.root.Stat(ctx, childPath)
	if err != nil {
		return nil, errnoFromError(err)
	}
	fillHanwenAttr(&out.Attr, childPath, info)
	child := n.NewInode(ctx, &linuxHanwenNode{root: n.root, path: childPath}, stableAttrForInfo(childPath, info))
	return child, fs.OK
}

func (n *linuxHanwenNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	entries, err := n.root.ListDir(ctx, n.path)
	if err != nil {
		return nil, errnoFromError(err)
	}
	return fs.NewListDirStream(buildDirEntries(n.path, entries)), fs.OK
}

func buildDirEntries(parent string, entries []stdfs.DirEntry) []fuse.DirEntry {
	out := make([]fuse.DirEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, fuse.DirEntry{
			Name: entry.Name(),
			Mode: hanwenDirentMode(entry),
			Ino:  inodeForPath(childExecPath(parent, entry.Name())),
		})
	}
	return out
}

func hanwenDirentMode(entry stdfs.DirEntry) uint32 {
	if entry.IsDir() {
		return fuseDirMode
	}
	return fuseFileMode
}

func (n *linuxHanwenNode) Mkdir(ctx context.Context, name string, _ uint32, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	childPath := childExecPath(n.path, name)
	if err := n.root.MkdirAll(ctx, childPath); err != nil {
		return nil, errnoFromError(err)
	}
	info, err := n.root.Stat(ctx, childPath)
	if err != nil {
		return nil, errnoFromError(err)
	}
	fillHanwenAttr(&out.Attr, childPath, info)
	child := n.NewInode(ctx, &linuxHanwenNode{root: n.root, path: childPath}, stableAttrForInfo(childPath, info))
	return child, fs.OK
}

func (n *linuxHanwenNode) Unlink(ctx context.Context, name string) syscall.Errno {
	if err := n.root.Delete(ctx, childExecPath(n.path, name)); err != nil {
		return errnoFromError(err)
	}
	return fs.OK
}

func (n *linuxHanwenNode) Rmdir(ctx context.Context, name string) syscall.Errno {
	// projectedRoot.Delete handles both files and empty directories;
	// the kernel ensures Rmdir is only called for directories, so the
	// shared delete path is correct.
	if err := n.root.Delete(ctx, childExecPath(n.path, name)); err != nil {
		return errnoFromError(err)
	}
	return fs.OK
}

func (n *linuxHanwenNode) Rename(ctx context.Context, oldName string, newParent fs.InodeEmbedder, newName string, _ uint32) syscall.Errno {
	target, ok := newParent.(*linuxHanwenNode)
	if !ok {
		return syscall.EINVAL
	}
	oldPath := childExecPath(n.path, oldName)
	newPath := childExecPath(target.path, newName)
	if err := n.root.Rename(ctx, oldPath, newPath); err != nil {
		return errnoFromError(err)
	}
	return fs.OK
}

func (n *linuxHanwenNode) Create(ctx context.Context, name string, flags, _ uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	childPath := childExecPath(n.path, name)
	id, err := n.root.CreateHandle(ctx, childPath, true)
	if err != nil {
		return nil, nil, 0, errnoFromError(err)
	}
	info, err := n.root.Stat(ctx, childPath)
	if err != nil {
		_ = n.root.ReleaseHandle(ctx, id)
		return nil, nil, 0, errnoFromError(err)
	}
	fillHanwenAttr(&out.Attr, childPath, info)
	child := n.NewInode(ctx, &linuxHanwenNode{root: n.root, path: childPath}, stableAttrForInfo(childPath, info))
	return child, &linuxHanwenHandle{root: n.root, id: id}, fuse.FOPEN_DIRECT_IO, fs.OK
}

func (n *linuxHanwenNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	id, err := n.root.OpenHandle(ctx, n.path, openFlagsWritable(flags), openFlagsTruncate(flags))
	if err != nil {
		return nil, 0, errnoFromError(err)
	}
	return &linuxHanwenHandle{root: n.root, id: id}, fuse.FOPEN_DIRECT_IO, fs.OK
}

func openFlagsWritable(flags uint32) bool {
	mode := flags & uint32(syscall.O_ACCMODE)
	return mode == syscall.O_WRONLY || mode == syscall.O_RDWR
}

func openFlagsTruncate(flags uint32) bool {
	return flags&syscall.O_TRUNC != 0
}

func (n *linuxHanwenNode) Setattr(ctx context.Context, _ fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	if size, ok := in.GetSize(); ok {
		if err := n.applyTruncate(ctx, size); err != nil {
			return errnoFromError(err)
		}
	}
	info, err := n.root.Stat(ctx, n.path)
	if err != nil {
		return errnoFromError(err)
	}
	fillHanwenAttr(&out.Attr, n.path, info)
	return fs.OK
}

func (n *linuxHanwenNode) applyTruncate(ctx context.Context, size uint64) error {
	id, err := n.root.OpenHandle(ctx, n.path, true, false)
	if err != nil {
		return err
	}
	defer n.root.ReleaseHandle(ctx, id)
	return n.root.TruncateHandle(id, int64(size))
}

// ── Handle operations ──────────────────────────────────────────────

func (h *linuxHanwenHandle) Read(_ context.Context, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	n, err := h.root.ReadHandle(h.id, dest, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, errnoFromError(err)
	}
	return fuse.ReadResultData(dest[:n]), fs.OK
}

func (h *linuxHanwenHandle) Write(_ context.Context, data []byte, off int64) (uint32, syscall.Errno) {
	n, err := h.root.WriteHandle(h.id, data, off)
	if err != nil {
		return 0, errnoFromError(err)
	}
	return uint32(n), fs.OK
}

func (h *linuxHanwenHandle) Flush(ctx context.Context) syscall.Errno {
	if err := h.root.FlushHandle(ctx, h.id); err != nil {
		return errnoFromError(err)
	}
	return fs.OK
}

func (h *linuxHanwenHandle) Release(ctx context.Context) syscall.Errno {
	if err := h.root.ReleaseHandle(ctx, h.id); err != nil {
		return errnoFromError(err)
	}
	return fs.OK
}

// ── Helpers ─────────────────────────────────────────────────────────

func fillHanwenAttr(out *fuse.Attr, name string, info stdfs.FileInfo) {
	out.Ino = inodeForPath(name)
	out.Mode = goModeToFuseMode(info.Mode())
	out.Size = uint64(max(info.Size(), 0))
	stamp := info.ModTime()
	setHanwenTime(&out.Mtime, &out.Mtimensec, stamp)
	setHanwenTime(&out.Ctime, &out.Ctimensec, stamp)
	setHanwenTime(&out.Atime, &out.Atimensec, stamp)
}

// goModeToFuseMode translates Go's os.FileMode into the POSIX wire
// format the FUSE kernel module expects (permissions in the low 12
// bits, type indicator in the upper bits via S_IF* constants).
//
// Go's FileMode encodes type via high-bit flags (ModeDir=1<<31, etc.)
// that are convenient for Go callers but are not what the kernel
// reads. Passing Go's raw uint32(FileMode) to hanwen panics during
// Mkdir because hanwen explicitly checks that the mode's type bits
// match the operation (Mkdir → S_IFDIR).
func goModeToFuseMode(m stdfs.FileMode) uint32 {
	out := uint32(m.Perm())
	switch {
	case m.IsDir():
		return out | syscall.S_IFDIR
	case m&stdfs.ModeSymlink != 0:
		return out | syscall.S_IFLNK
	}
	return out | syscall.S_IFREG
}

func setHanwenTime(secs *uint64, nsecs *uint32, t time.Time) {
	*secs = uint64(t.Unix())
	*nsecs = uint32(t.Nanosecond())
}

func stableAttrForInfo(name string, info stdfs.FileInfo) fs.StableAttr {
	mode := uint32(fuseFileMode)
	if info.IsDir() {
		mode = uint32(fuseDirMode)
	}
	return fs.StableAttr{
		Mode: mode,
		Ino:  inodeForPath(name),
	}
}

// errnoFromError converts a Go error from the projection layer into a
// FUSE syscall.Errno the kernel expects.
//
// The classifier is intentionally narrow: known wrappers (ENOENT for
// "file not found", EINVAL for bad path arguments, EROFS for write
// rejections) get mapped explicitly; everything else degrades to EIO,
// which is the right "something went wrong, ask the syscall layer to
// surface it" answer for a userspace filesystem.
func errnoFromError(err error) syscall.Errno {
	if err == nil {
		return fs.OK
	}
	if errno := unwrapErrno(err); errno != 0 {
		return errno
	}
	if isNotExist(err) {
		return syscall.ENOENT
	}
	if isInvalidArgument(err) {
		return syscall.EINVAL
	}
	if isReadOnly(err) {
		return syscall.EROFS
	}
	return syscall.EIO
}

func unwrapErrno(err error) syscall.Errno {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno
	}
	return 0
}

func isNotExist(err error) bool {
	return errors.Is(err, stdfs.ErrNotExist) || errors.Is(err, os.ErrNotExist)
}

func isInvalidArgument(err error) bool {
	return errors.Is(err, stdfs.ErrInvalid) || strings.Contains(err.Error(), "invalid argument")
}

func isReadOnly(err error) bool {
	return errors.Is(err, stdfs.ErrPermission) || strings.Contains(err.Error(), "read-only")
}

// ensure the path import is referenced when above doesn't use it
var _ = path.Clean
