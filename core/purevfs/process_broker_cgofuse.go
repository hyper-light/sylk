//go:build windows || (darwin && cgo)

package purevfs

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"syscall"
	"time"

	cgofuse "github.com/winfsp/cgofuse/fuse"
)

type cgofuseExecutionMount struct {
	root string
	host *cgofuse.FileSystemHost
	done chan bool
}

type cgofuseProjectionFS struct {
	cgofuse.FileSystemBase
	root  *projectedRoot
	ready chan struct{}
}

func newCGOFuseProjectionFS(root *projectedRoot) *cgofuseProjectionFS {
	return &cgofuseProjectionFS{
		root:  root,
		ready: make(chan struct{}),
	}
}

func mountCGOFuseExecutionRoot(mountpoint string, opts []string, root *projectedRoot) (mountedExecutionRoot, error) {
	projection := newCGOFuseProjectionFS(root)
	host := cgofuse.NewFileSystemHost(projection)
	done := make(chan bool, 1)
	go func() {
		done <- host.Mount(mountpoint, opts)
	}()
	select {
	case <-projection.ready:
		return &cgofuseExecutionMount{
			root: mountpoint,
			host: host,
			done: done,
		}, nil
	case ok := <-done:
		if !ok {
			return nil, ErrStrictExecutionUnavailable
		}
		return nil, ErrStrictExecutionUnavailable
	case <-time.After(2 * time.Second):
		host.Unmount()
		<-done
		return nil, ErrStrictExecutionUnavailable
	}
}

func (m *cgofuseExecutionMount) Root() string {
	return m.root
}

func (m *cgofuseExecutionMount) Close() error {
	if m.host != nil {
		m.host.Unmount()
	}
	if m.done != nil {
		<-m.done
	}
	if strings.HasSuffix(m.root, ":") {
		return nil
	}
	return os.Remove(m.root)
}

func (f *cgofuseProjectionFS) Init() {
	select {
	case <-f.ready:
	default:
		close(f.ready)
	}
}

func (f *cgofuseProjectionFS) Destroy() {}

func (f *cgofuseProjectionFS) Statfs(string, *cgofuse.Statfs_t) int {
	return 0
}

func (f *cgofuseProjectionFS) Access(path string, _ uint32) int {
	_, err := f.root.Stat(context.Background(), path)
	return cgofuseErr(err)
}

func (f *cgofuseProjectionFS) Mkdir(path string, _ uint32) int {
	return cgofuseErr(f.root.MkdirAll(context.Background(), path))
}

func (f *cgofuseProjectionFS) Unlink(path string) int {
	return cgofuseErr(f.root.Delete(context.Background(), path))
}

func (f *cgofuseProjectionFS) Rmdir(path string) int {
	return cgofuseErr(f.root.Delete(context.Background(), path))
}

func (f *cgofuseProjectionFS) Rename(oldpath, newpath string) int {
	return cgofuseErr(f.root.Rename(context.Background(), oldpath, newpath))
}

func (f *cgofuseProjectionFS) Create(path string, _ int, _ uint32) (int, uint64) {
	id, err := f.root.CreateHandle(context.Background(), path, true)
	return cgofuseErr(err), id
}

func (f *cgofuseProjectionFS) Open(path string, flags int) (int, uint64) {
	id, err := f.root.OpenHandle(context.Background(), path, cgofuseOpenWritable(flags), flags&cgofuse.O_TRUNC != 0)
	return cgofuseErr(err), id
}

func (f *cgofuseProjectionFS) Getattr(path string, stat *cgofuse.Stat_t, _ uint64) int {
	info, err := f.root.Stat(context.Background(), path)
	if err != nil {
		return cgofuseErr(err)
	}
	stat.Mode = cgofuseMode(info.Mode())
	stat.Size = info.Size()
	stat.Atim = cgofuse.NewTimespec(info.ModTime())
	stat.Mtim = cgofuse.NewTimespec(info.ModTime())
	stat.Ctim = cgofuse.NewTimespec(info.ModTime())
	stat.Birthtim = cgofuse.NewTimespec(info.ModTime())
	return 0
}

func (f *cgofuseProjectionFS) Truncate(path string, size int64, fh uint64) int {
	handle := fh
	if handle == 0 {
		id, err := f.root.OpenHandle(context.Background(), path, true, false)
		if err != nil {
			return cgofuseErr(err)
		}
		defer f.root.ReleaseHandle(context.Background(), id)
		handle = id
	}
	return cgofuseErr(f.root.TruncateHandle(handle, size))
}

func (f *cgofuseProjectionFS) Read(_ string, buff []byte, ofst int64, fh uint64) int {
	n, err := f.root.ReadHandle(fh, buff, ofst)
	if err != nil {
		return cgofuseErr(err)
	}
	return n
}

func (f *cgofuseProjectionFS) Write(_ string, buff []byte, ofst int64, fh uint64) int {
	n, err := f.root.WriteHandle(fh, buff, ofst)
	if err != nil {
		return cgofuseErr(err)
	}
	return n
}

func (f *cgofuseProjectionFS) Flush(_ string, fh uint64) int {
	return cgofuseErr(f.root.FlushHandle(context.Background(), fh))
}

func (f *cgofuseProjectionFS) Release(_ string, fh uint64) int {
	return cgofuseErr(f.root.ReleaseHandle(context.Background(), fh))
}

func (f *cgofuseProjectionFS) Opendir(string) (int, uint64) {
	return 0, 0
}

func (f *cgofuseProjectionFS) Readdir(path string, fill func(name string, stat *cgofuse.Stat_t, ofst int64) bool, _ int64, _ uint64) int {
	entries, err := f.root.ListDir(context.Background(), path)
	if err != nil {
		return cgofuseErr(err)
	}
	fill(".", nil, 0)
	fill("..", nil, 0)
	for _, entry := range entries {
		if !fill(entry.Name(), nil, 0) {
			break
		}
	}
	return 0
}

func (f *cgofuseProjectionFS) Releasedir(string, uint64) int {
	return 0
}

func cgofuseMode(mode fs.FileMode) uint32 {
	if mode.IsDir() {
		return cgofuse.S_IFDIR | uint32(mode.Perm())
	}
	return cgofuse.S_IFREG | uint32(mode.Perm())
}

func cgofuseOpenWritable(flags int) bool {
	return flags&cgofuse.O_WRONLY != 0 || flags&cgofuse.O_RDWR != 0
}

func cgofuseErr(err error) int {
	if err == nil {
		return 0
	}
	err = translateExecutionFSError(err)
	switch {
	case errors.Is(err, syscall.ENOENT):
		return -cgofuse.ENOENT
	case errors.Is(err, syscall.EEXIST):
		return -cgofuse.EEXIST
	case errors.Is(err, syscall.ENOTEMPTY):
		return -cgofuse.ENOTEMPTY
	case errors.Is(err, syscall.EXDEV):
		return -cgofuse.EXDEV
	case errors.Is(err, syscall.EPERM):
		return -cgofuse.EPERM
	default:
		return -cgofuse.EIO
	}
}
