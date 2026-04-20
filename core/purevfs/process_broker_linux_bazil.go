//go:build linux && !substrate_fuse_v2

// bazil/fuse Linux backend for the substrate's process broker
// projection.
//
// Compiled by default on Linux. Selectable off via the
// `substrate_fuse_v2` build tag, which switches to the hanwen/go-fuse/v2
// backend in process_broker_linux_hanwen.go.
//
// All FUSE-library-agnostic Linux broker code lives in
// process_broker_linux_common.go.

package purevfs

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"syscall"
	"time"

	bazilfuse "bazil.org/fuse"
	bazilfs "bazil.org/fuse/fs"
)

type linuxExecutionMount struct {
	root     string
	conn     *bazilfuse.Conn
	serveErr chan error
}

type linuxProjectionFS struct {
	root *projectedRoot
}

type linuxProjectionDir struct {
	fs   *linuxProjectionFS
	path string
}

type linuxProjectionFile struct {
	fs   *linuxProjectionFS
	path string
}

type linuxProjectionHandle struct {
	root *projectedRoot
	id   uint64
}

func mountExecutionRoot(_ context.Context, _ BrokerRunRequest, root *projectedRoot) (mountedExecutionRoot, error) {
	mountpoint, err := newLinuxMountpoint()
	if err != nil {
		return nil, err
	}
	conn, err := bazilfuse.Mount(
		mountpoint,
		bazilfuse.FSName("sylk-purevfs"),
		bazilfuse.Subtype("sylk"),
	)
	if err != nil {
		_ = os.Remove(mountpoint)
		return nil, err
	}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- bazilfs.Serve(conn, &linuxProjectionFS{root: root})
	}()
	return &linuxExecutionMount{
		root:     mountpoint,
		conn:     conn,
		serveErr: serveErr,
	}, nil
}

func (m *linuxExecutionMount) Root() string {
	return m.root
}

func (m *linuxExecutionMount) Close() error {
	var firstErr error
	if err := bazilfuse.Unmount(m.root); err != nil && !errors.Is(err, syscall.EINVAL) {
		firstErr = err
	}
	if m.conn != nil {
		if err := m.conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	select {
	case err := <-m.serveErr:
		if err != nil && !errors.Is(err, bazilfuse.ErrClosedWithoutInit) && firstErr == nil {
			firstErr = err
		}
	case <-time.After(250 * time.Millisecond):
	}
	if err := os.Remove(m.root); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}

func (f *linuxProjectionFS) Root() (bazilfs.Node, error) {
	return &linuxProjectionDir{fs: f, path: "/"}, nil
}

func (d *linuxProjectionDir) Attr(ctx context.Context, attr *bazilfuse.Attr) error {
	return fillLinuxAttr(ctx, d.fs.root, d.path, attr)
}

func (d *linuxProjectionDir) Lookup(ctx context.Context, name string) (bazilfs.Node, error) {
	child := childExecPath(d.path, name)
	info, err := d.fs.root.Stat(ctx, child)
	if err != nil {
		return nil, err
	}
	return linuxNodeForInfo(d.fs, child, info), nil
}

func (d *linuxProjectionDir) ReadDirAll(ctx context.Context) ([]bazilfuse.Dirent, error) {
	entries, err := d.fs.root.ListDir(ctx, d.path)
	if err != nil {
		return nil, err
	}
	out := make([]bazilfuse.Dirent, 0, len(entries))
	for _, entry := range entries {
		out = append(out, bazilfuse.Dirent{
			Inode: inodeForPath(childExecPath(d.path, entry.Name())),
			Name:  entry.Name(),
			Type:  linuxDirentType(entry),
		})
	}
	return out, nil
}

func (d *linuxProjectionDir) Mkdir(ctx context.Context, req *bazilfuse.MkdirRequest) (bazilfs.Node, error) {
	child := childExecPath(d.path, req.Name)
	if err := d.fs.root.MkdirAll(ctx, child); err != nil {
		return nil, err
	}
	return &linuxProjectionDir{fs: d.fs, path: child}, nil
}

func (d *linuxProjectionDir) Remove(ctx context.Context, req *bazilfuse.RemoveRequest) error {
	return d.fs.root.Delete(ctx, childExecPath(d.path, req.Name))
}

func (d *linuxProjectionDir) Rename(ctx context.Context, req *bazilfuse.RenameRequest, newDir bazilfs.Node) error {
	targetDir, ok := newDir.(*linuxProjectionDir)
	if !ok {
		return syscall.EINVAL
	}
	oldPath := childExecPath(d.path, req.OldName)
	newPath := childExecPath(targetDir.path, req.NewName)
	return d.fs.root.Rename(ctx, oldPath, newPath)
}

func (d *linuxProjectionDir) Create(ctx context.Context, req *bazilfuse.CreateRequest, resp *bazilfuse.CreateResponse) (bazilfs.Node, bazilfs.Handle, error) {
	child := childExecPath(d.path, req.Name)
	id, err := d.fs.root.CreateHandle(ctx, child, true)
	if err != nil {
		return nil, nil, err
	}
	resp.Flags |= bazilfuse.OpenDirectIO
	node := &linuxProjectionFile{fs: d.fs, path: child}
	return node, &linuxProjectionHandle{root: d.fs.root, id: id}, nil
}

func (f *linuxProjectionFile) Attr(ctx context.Context, attr *bazilfuse.Attr) error {
	return fillLinuxAttr(ctx, f.fs.root, f.path, attr)
}

func (f *linuxProjectionFile) Open(ctx context.Context, req *bazilfuse.OpenRequest, resp *bazilfuse.OpenResponse) (bazilfs.Handle, error) {
	id, err := f.fs.root.OpenHandle(ctx, f.path, linuxOpenWritable(req.Flags), req.Flags&bazilfuse.OpenTruncate != 0)
	if err != nil {
		return nil, err
	}
	resp.Flags |= bazilfuse.OpenDirectIO
	return &linuxProjectionHandle{root: f.fs.root, id: id}, nil
}

func (f *linuxProjectionFile) Setattr(ctx context.Context, req *bazilfuse.SetattrRequest, resp *bazilfuse.SetattrResponse) error {
	if !req.Valid.Size() {
		return fillLinuxAttr(ctx, f.fs.root, f.path, &resp.Attr)
	}
	id, err := f.fs.root.OpenHandle(ctx, f.path, true, false)
	if err != nil {
		return err
	}
	defer f.fs.root.ReleaseHandle(ctx, id)
	if err := f.fs.root.TruncateHandle(id, int64(req.Size)); err != nil {
		return err
	}
	return fillLinuxAttr(ctx, f.fs.root, f.path, &resp.Attr)
}

func (h *linuxProjectionHandle) Read(_ context.Context, req *bazilfuse.ReadRequest, resp *bazilfuse.ReadResponse) error {
	data := make([]byte, req.Size)
	n, err := h.root.ReadHandle(h.id, data, req.Offset)
	if err != nil {
		return err
	}
	resp.Data = data[:n]
	return nil
}

func (h *linuxProjectionHandle) Write(_ context.Context, req *bazilfuse.WriteRequest, resp *bazilfuse.WriteResponse) error {
	n, err := h.root.WriteHandle(h.id, req.Data, req.Offset)
	if err != nil {
		return err
	}
	resp.Size = n
	return nil
}

func (h *linuxProjectionHandle) Flush(ctx context.Context, _ *bazilfuse.FlushRequest) error {
	return h.root.FlushHandle(ctx, h.id)
}

func (h *linuxProjectionHandle) Release(ctx context.Context, _ *bazilfuse.ReleaseRequest) error {
	return h.root.ReleaseHandle(ctx, h.id)
}

func fillLinuxAttr(ctx context.Context, root *projectedRoot, name string, attr *bazilfuse.Attr) error {
	info, err := root.Stat(ctx, name)
	if err != nil {
		return err
	}
	attr.Inode = inodeForPath(name)
	attr.Mode = info.Mode()
	attr.Size = uint64(max(info.Size(), 0))
	attr.Mtime = info.ModTime()
	attr.Ctime = info.ModTime()
	return nil
}

func linuxNodeForInfo(fsRoot *linuxProjectionFS, name string, info fs.FileInfo) bazilfs.Node {
	if info.IsDir() {
		return &linuxProjectionDir{fs: fsRoot, path: name}
	}
	return &linuxProjectionFile{fs: fsRoot, path: name}
}

func linuxDirentType(entry fs.DirEntry) bazilfuse.DirentType {
	if entry.IsDir() {
		return bazilfuse.DT_Dir
	}
	return bazilfuse.DT_File
}

func linuxOpenWritable(flags bazilfuse.OpenFlags) bool {
	return flags.IsWriteOnly() || flags.IsReadWrite()
}
