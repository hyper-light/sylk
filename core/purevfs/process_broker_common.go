package purevfs

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

type executionNamespace interface {
	Stat(ctx context.Context, name string) (fs.FileInfo, error)
	ListDir(ctx context.Context, dir string) ([]fs.DirEntry, error)
	ReadFile(ctx context.Context, name string) ([]byte, error)
	MkdirAll(ctx context.Context, dir string) error
	WriteFile(ctx context.Context, name string, content []byte) error
	DeleteFile(ctx context.Context, name string) error
	Rename(ctx context.Context, oldName, newName string) error
	ReadOnly() bool
}

type mountedNamespace struct {
	root      string
	namespace executionNamespace
}

type projectedRoot struct {
	mounts     []mountedNamespace
	staticDirs map[string]struct{}
	budget     *executionBudget

	mu      sync.Mutex
	handles map[uint64]*projectedHandle
	nextID  atomic.Uint64
}

type projectedHandle struct {
	namespace executionNamespace
	path      string
	content   []byte
	writable  bool
	dirty     bool
	reserved  int64
}

type projectionFileInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
}

type projectionDirEntry struct {
	name string
	info fs.FileInfo
}

type workspaceNamespace struct {
	fs   ExecutionFS
	root string
}

type hostNamespace struct {
	root string
}

type memoryNamespace struct {
	mu     sync.RWMutex
	files  map[string]*memoryFile
	dirs   map[string]*memoryDir
	budget *executionBudget
}

type memoryFile struct {
	// content holds the bytes as stored in memory. When compressed=true,
	// this is the DEFLATE-encoded payload; otherwise it is the raw file
	// bytes. rawSize records the pre-compression length so callers can
	// report the real file size without decompressing.
	content    []byte
	mode       fs.FileMode
	modTime    time.Time
	accessTime time.Time // updated on every read; drives cold-file eviction
	compressed bool
	rawSize    int // only meaningful when compressed=true
}

// size returns the logical file size — the bytes a reader would see —
// regardless of whether the stored payload is compressed. Stat and
// directory listings use this so the sandbox sees consistent sizes.
func (f *memoryFile) size() int64 {
	if f.compressed {
		return int64(f.rawSize)
	}
	return int64(len(f.content))
}

type memoryDir struct {
	mode    fs.FileMode
	modTime time.Time
}

func newProjectedRoot(req BrokerRunRequest, budget *executionBudget) (*projectedRoot, error) {
	root := &projectedRoot{
		staticDirs: map[string]struct{}{"/": {}},
		handles:    make(map[uint64]*projectedHandle),
		budget:     budget,
	}
	for _, mount := range req.Plan.Mounts {
		if !strings.HasPrefix(strings.TrimSpace(mount.VirtualPath), "/") {
			continue
		}
		namespace, ok := namespaceForMount(req, mount, budget)
		if !ok {
			continue
		}
		mountRoot := normalizeExecPath(mount.VirtualPath)
		root.mounts = append(root.mounts, mountedNamespace{
			root:      mountRoot,
			namespace: namespace,
		})
		root.addStaticParents(mountRoot)
	}
	sort.Slice(root.mounts, func(i, j int) bool {
		return len(root.mounts[i].root) > len(root.mounts[j].root)
	})
	return root, nil
}

func namespaceForMount(req BrokerRunRequest, mount MountSpec, budget *executionBudget) (executionNamespace, bool) {
	switch mount.Kind {
	case MountWorkspace:
		if req.Workspace == nil {
			return nil, false
		}
		return workspaceNamespace{
			fs:   req.Workspace,
			root: req.Workspace.WorkingDir(),
		}, true
	case MountToolchain:
		namespace, ok := newHostNamespace(mount.BackingPath)
		return namespace, ok
	case MountTemp, MountCache, MountHome, MountOutput:
		return newMemoryNamespace(budget), true
	default:
		return nil, false
	}
}

func newHostNamespace(root string) (executionNamespace, bool) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, false
	}
	if !filepath.IsAbs(root) {
		resolved, err := exec.LookPath(root)
		if err != nil {
			return nil, false
		}
		root = resolved
	}
	return hostNamespace{root: root}, true
}

func newMemoryNamespace(budget *executionBudget) *memoryNamespace {
	now := time.Now().UTC()
	return &memoryNamespace{
		files: make(map[string]*memoryFile),
		dirs: map[string]*memoryDir{
			"/": {mode: fs.ModeDir | 0o755, modTime: now},
		},
		budget: budget,
	}
}

func (p *projectedRoot) addStaticParents(name string) {
	for current := path.Dir(name); current != "."; current = path.Dir(current) {
		p.staticDirs[current] = struct{}{}
		if current == "/" {
			return
		}
	}
}

func (p *projectedRoot) Stat(ctx context.Context, name string) (fs.FileInfo, error) {
	mount, inner, ok := p.findNamespace(name)
	if ok {
		info, err := mount.namespace.Stat(ctx, inner)
		if err != nil {
			return nil, translateExecutionFSError(err)
		}
		return info, nil
	}
	if p.isStaticDir(name) {
		return staticDirInfo(name), nil
	}
	return nil, syscall.ENOENT
}

func (p *projectedRoot) ListDir(ctx context.Context, dir string) ([]fs.DirEntry, error) {
	mount, inner, ok := p.findNamespace(dir)
	if ok {
		entries, err := mount.namespace.ListDir(ctx, inner)
		if err != nil {
			return nil, translateExecutionFSError(err)
		}
		return entries, nil
	}
	if !p.isStaticDir(dir) {
		return nil, syscall.ENOENT
	}
	return p.listStaticDir(dir), nil
}

func (p *projectedRoot) MkdirAll(ctx context.Context, dir string) error {
	mount, inner, ok := p.findNamespace(dir)
	if !ok {
		return syscall.EPERM
	}
	return translateExecutionFSError(mount.namespace.MkdirAll(ctx, inner))
}

func (p *projectedRoot) Delete(ctx context.Context, name string) error {
	mount, inner, ok := p.findNamespace(name)
	if !ok {
		return syscall.EPERM
	}
	return translateExecutionFSError(mount.namespace.DeleteFile(ctx, inner))
}

func (p *projectedRoot) Rename(ctx context.Context, oldName, newName string) error {
	oldMount, oldInner, ok := p.findNamespace(oldName)
	if !ok {
		return syscall.ENOENT
	}
	newMount, newInner, ok := p.findNamespace(newName)
	if !ok || oldMount.root != newMount.root {
		return syscall.EXDEV
	}
	return translateExecutionFSError(oldMount.namespace.Rename(ctx, oldInner, newInner))
}

func (p *projectedRoot) CreateHandle(ctx context.Context, name string, truncate bool) (uint64, error) {
	mount, inner, ok := p.findNamespace(name)
	if !ok {
		return 0, syscall.EPERM
	}
	if err := mount.namespace.WriteFile(ctx, inner, nil); err != nil {
		return 0, translateExecutionFSError(err)
	}
	handle, err := p.newHandle(ctx, mount.namespace, inner, true, truncate)
	if err != nil {
		return 0, err
	}
	return handle, nil
}

func (p *projectedRoot) OpenHandle(ctx context.Context, name string, writable bool, truncate bool) (uint64, error) {
	mount, inner, ok := p.findNamespace(name)
	if !ok {
		return 0, syscall.ENOENT
	}
	return p.newHandle(ctx, mount.namespace, inner, writable, truncate)
}

func (p *projectedRoot) ReadHandle(id uint64, data []byte, offset int64) (int, error) {
	p.mu.Lock()
	handle := p.handles[id]
	p.mu.Unlock()
	if handle == nil {
		return 0, syscall.EBADF
	}
	if offset >= int64(len(handle.content)) {
		return 0, nil
	}
	end := int(offset) + len(data)
	if end > len(handle.content) {
		end = len(handle.content)
	}
	return copy(data, handle.content[offset:end]), nil
}

func (p *projectedRoot) WriteHandle(id uint64, data []byte, offset int64) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	handle := p.handles[id]
	if handle == nil {
		return 0, syscall.EBADF
	}
	if !handle.writable {
		return 0, syscall.EPERM
	}
	start, end, err := checkedWriteBounds(offset, len(data))
	if err != nil {
		return 0, err
	}
	if err := p.resizeHandleBuffer(handle, int64(end)); err != nil {
		return 0, err
	}
	copy(handle.content[start:end], data)
	handle.dirty = true
	return len(data), nil
}

func (p *projectedRoot) TruncateHandle(id uint64, size int64) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	handle := p.handles[id]
	if handle == nil {
		return syscall.EBADF
	}
	if !handle.writable {
		return syscall.EPERM
	}
	nextSize, err := checkedBufferSize(size)
	if err != nil {
		return err
	}
	if err := p.resizeHandleBuffer(handle, int64(nextSize)); err != nil {
		return err
	}
	handle.dirty = true
	return nil
}

func (p *projectedRoot) FlushHandle(ctx context.Context, id uint64) error {
	p.mu.Lock()
	handle := p.handles[id]
	p.mu.Unlock()
	if handle == nil {
		return syscall.EBADF
	}
	return p.flushHandle(ctx, handle)
}

func (p *projectedRoot) ReleaseHandle(ctx context.Context, id uint64) error {
	p.mu.Lock()
	handle := p.handles[id]
	delete(p.handles, id)
	p.mu.Unlock()
	if handle == nil {
		return nil
	}
	defer p.releaseHandleReservation(handle)
	return p.flushHandle(ctx, handle)
}

func (p *projectedRoot) newHandle(ctx context.Context, namespace executionNamespace, name string, writable bool, truncate bool) (uint64, error) {
	if writable && namespace.ReadOnly() {
		return 0, syscall.EPERM
	}
	content, reserved, err := p.loadHandleContent(ctx, namespace, name, truncate)
	if err != nil {
		return 0, translateExecutionFSError(err)
	}
	id := p.nextID.Add(1)
	p.mu.Lock()
	p.handles[id] = &projectedHandle{
		namespace: namespace,
		path:      name,
		content:   content,
		writable:  writable,
		dirty:     truncate,
		reserved:  reserved,
	}
	p.mu.Unlock()
	return id, nil
}

func (p *projectedRoot) flushHandle(ctx context.Context, handle *projectedHandle) error {
	if handle == nil || !handle.dirty {
		return nil
	}
	if err := handle.namespace.WriteFile(ctx, handle.path, handle.content); err != nil {
		return translateExecutionFSError(err)
	}
	handle.dirty = false
	return nil
}

func (p *projectedRoot) loadHandleContent(ctx context.Context, namespace executionNamespace, name string, truncate bool) ([]byte, int64, error) {
	if truncate {
		return nil, 0, nil
	}
	reserved, err := p.reserveHandleSize(ctx, namespace, name)
	if err != nil {
		return nil, 0, err
	}
	content, err := namespace.ReadFile(ctx, name)
	if err != nil {
		p.releaseReservedBytes(reserved)
		return nil, 0, err
	}
	actual := int64(len(content))
	if err := p.adjustReservedBytes(&reserved, actual); err != nil {
		p.releaseReservedBytes(reserved)
		return nil, 0, err
	}
	return content, reserved, nil
}

func (p *projectedRoot) reserveHandleSize(ctx context.Context, namespace executionNamespace, name string) (int64, error) {
	info, err := namespace.Stat(ctx, name)
	if err != nil {
		if errors.Is(err, syscall.ENOENT) {
			return 0, nil
		}
		return 0, err
	}
	size := info.Size()
	return size, p.reserveBytes(size)
}

func (p *projectedRoot) resizeHandleBuffer(handle *projectedHandle, nextSize int64) error {
	if err := p.adjustReservedBytes(&handle.reserved, nextSize); err != nil {
		return err
	}
	handle.content = resizeBytes(handle.content, int(nextSize))
	return nil
}

func (p *projectedRoot) adjustReservedBytes(current *int64, next int64) error {
	if next < 0 {
		return syscall.EINVAL
	}
	if next > *current {
		if err := p.reserveBytes(next - *current); err != nil {
			return err
		}
		*current = next
		return nil
	}
	p.releaseReservedBytes(*current - next)
	*current = next
	return nil
}

func (p *projectedRoot) reserveBytes(bytes int64) error {
	if p == nil || p.budget == nil {
		return nil
	}
	return p.budget.Reserve(bytes)
}

func (p *projectedRoot) releaseReservedBytes(bytes int64) {
	if p == nil || p.budget == nil {
		return
	}
	p.budget.Release(bytes)
}

func (p *projectedRoot) releaseHandleReservation(handle *projectedHandle) {
	if handle == nil || handle.reserved <= 0 {
		return
	}
	p.releaseReservedBytes(handle.reserved)
	handle.reserved = 0
}

func (p *projectedRoot) findNamespace(name string) (mountedNamespace, string, bool) {
	cleaned := normalizeExecPath(name)
	for _, mount := range p.mounts {
		if cleaned == mount.root {
			return mount, "/", true
		}
		if !hasExecPrefix(cleaned, mount.root) {
			continue
		}
		inner := strings.TrimPrefix(cleaned, mount.root)
		if inner == "" {
			inner = "/"
		}
		return mount, normalizeExecPath(inner), true
	}
	return mountedNamespace{}, "", false
}

func (p *projectedRoot) isStaticDir(name string) bool {
	_, ok := p.staticDirs[normalizeExecPath(name)]
	return ok
}

func (p *projectedRoot) listStaticDir(dir string) []fs.DirEntry {
	parent := normalizeExecPath(dir)
	entries := make(map[string]projectionDirEntry)
	for _, mount := range p.mounts {
		p.addStaticChild(entries, parent, mount.root)
	}
	for staticDir := range p.staticDirs {
		if staticDir == parent {
			continue
		}
		p.addStaticChild(entries, parent, staticDir)
	}
	out := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name() < out[j].Name()
	})
	return out
}

func (p *projectedRoot) addStaticChild(entries map[string]projectionDirEntry, parent, candidate string) {
	if candidate == parent || !hasExecPrefix(candidate, parent) {
		return
	}
	rel := strings.TrimPrefix(candidate, parent)
	rel = strings.TrimPrefix(rel, "/")
	segment := rel
	if idx := strings.IndexRune(rel, '/'); idx >= 0 {
		segment = rel[:idx]
	}
	if segment == "" {
		return
	}
	name := path.Join(parent, segment)
	entries[segment] = projectionDirEntry{
		name: segment,
		info: staticDirInfo(name),
	}
}

func (n workspaceNamespace) Stat(ctx context.Context, name string) (fs.FileInfo, error) {
	return n.fs.Stat(ctx, n.resolve(name))
}

func (n workspaceNamespace) ListDir(ctx context.Context, dir string) ([]fs.DirEntry, error) {
	return n.fs.ListDir(ctx, n.resolve(dir))
}

func (n workspaceNamespace) ReadFile(ctx context.Context, name string) ([]byte, error) {
	return n.fs.ReadFile(ctx, n.resolve(name))
}

func (n workspaceNamespace) MkdirAll(ctx context.Context, dir string) error {
	return n.fs.MkdirAll(ctx, n.resolve(dir))
}

func (n workspaceNamespace) WriteFile(ctx context.Context, name string, content []byte) error {
	return n.fs.WriteFile(ctx, n.resolve(name), content)
}

func (n workspaceNamespace) DeleteFile(ctx context.Context, name string) error {
	info, err := n.Stat(ctx, name)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return syscall.EPERM
	}
	return n.fs.DeleteFile(ctx, n.resolve(name))
}

func (n workspaceNamespace) Rename(ctx context.Context, oldName, newName string) error {
	info, err := n.Stat(ctx, oldName)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return syscall.EPERM
	}
	content, err := n.ReadFile(ctx, oldName)
	if err != nil {
		return err
	}
	if err := n.WriteFile(ctx, newName, content); err != nil {
		return err
	}
	return n.DeleteFile(ctx, oldName)
}

func (n workspaceNamespace) ReadOnly() bool {
	return n.fs.IsReadOnly()
}

func (n workspaceNamespace) resolve(name string) string {
	root := strings.TrimSpace(n.root)
	inner := strings.TrimPrefix(normalizeExecPath(name), "/")
	if root == "" {
		if inner == "" {
			return "."
		}
		return inner
	}
	if inner == "" {
		return root
	}
	return path.Join(root, inner)
}

func (n hostNamespace) Stat(_ context.Context, name string) (fs.FileInfo, error) {
	return os.Stat(n.resolve(name))
}

func (n hostNamespace) ListDir(_ context.Context, dir string) ([]fs.DirEntry, error) {
	return os.ReadDir(n.resolve(dir))
}

func (n hostNamespace) ReadFile(_ context.Context, name string) ([]byte, error) {
	// Route through the process-wide toolchain read cache. hostNamespace is
	// read-only by contract, and the cache keys by (path, size, mtime) so a
	// toolchain file that actually changed on disk (rare — usually only if
	// the user reinstalls a package) is invalidated automatically.
	return cachedReadFile(n.resolve(name))
}

func (n hostNamespace) MkdirAll(context.Context, string) error {
	return fs.ErrPermission
}

func (n hostNamespace) WriteFile(context.Context, string, []byte) error {
	return fs.ErrPermission
}

func (n hostNamespace) DeleteFile(context.Context, string) error {
	return fs.ErrPermission
}

func (n hostNamespace) Rename(context.Context, string, string) error {
	return fs.ErrPermission
}

func (n hostNamespace) ReadOnly() bool {
	return true
}

func (n hostNamespace) resolve(name string) string {
	inner := strings.TrimPrefix(normalizeExecPath(name), "/")
	if inner == "" {
		return n.root
	}
	return filepath.Join(n.root, filepath.FromSlash(inner))
}

func (n *memoryNamespace) Stat(_ context.Context, name string) (fs.FileInfo, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	cleaned := normalizeExecPath(name)
	if file := n.files[cleaned]; file != nil {
		return projectionFileInfo{
			name:    path.Base(cleaned),
			size:    file.size(), // logical (uncompressed) size
			mode:    file.mode,
			modTime: file.modTime,
		}, nil
	}
	if dir := n.dirs[cleaned]; dir != nil {
		return projectionFileInfo{
			name:    dirName(cleaned),
			mode:    dir.mode,
			modTime: dir.modTime,
		}, nil
	}
	return nil, syscall.ENOENT
}

func (n *memoryNamespace) ListDir(_ context.Context, dir string) ([]fs.DirEntry, error) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	cleaned := normalizeExecPath(dir)
	if n.dirs[cleaned] == nil {
		return nil, syscall.ENOENT
	}
	entries := make(map[string]projectionDirEntry)
	for name, file := range n.files {
		n.addChild(entries, cleaned, name, file.mode, file.size(), file.modTime)
	}
	for name, child := range n.dirs {
		n.addChild(entries, cleaned, name, child.mode, 0, child.modTime)
	}
	out := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name() < out[j].Name()
	})
	return out, nil
}

func (n *memoryNamespace) addChild(entries map[string]projectionDirEntry, parent, candidate string, mode fs.FileMode, size int64, modTime time.Time) {
	if candidate == parent || !hasExecPrefix(candidate, parent) {
		return
	}
	rel := strings.TrimPrefix(candidate, parent)
	rel = strings.TrimPrefix(rel, "/")
	name := rel
	if idx := strings.IndexRune(rel, '/'); idx >= 0 {
		name = rel[:idx]
		mode = fs.ModeDir | 0o755
		size = 0
	}
	if name == "" {
		return
	}
	entries[name] = projectionDirEntry{
		name: name,
		info: projectionFileInfo{
			name:    name,
			size:    size,
			mode:    mode,
			modTime: modTime,
		},
	}
}

func (n *memoryNamespace) ReadFile(_ context.Context, name string) ([]byte, error) {
	// Write lock: reads of compressed files mutate access time and may
	// inflate-in-place (which also updates the budget). Upgrading from
	// RLock at read time is not safe, so we hold the write lock for the
	// full read path.
	n.mu.Lock()
	defer n.mu.Unlock()
	cleaned := normalizeExecPath(name)
	file := n.files[cleaned]
	if file == nil {
		return nil, syscall.ENOENT
	}
	n.touchAccessLocked(file)
	return n.maybeInflateInPlaceLocked(cleaned)
}

func (n *memoryNamespace) MkdirAll(_ context.Context, dir string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.ensureDirs(normalizeExecPath(dir))
	return nil
}

func (n *memoryNamespace) WriteFile(_ context.Context, name string, content []byte) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	cleaned := normalizeExecPath(name)
	n.ensureDirs(path.Dir(cleaned))
	if err := n.adjustFileBytesLocked(cleaned, len(content)); err != nil {
		return err
	}
	now := time.Now().UTC()
	n.files[cleaned] = &memoryFile{
		content:    cloneBytes(content),
		mode:       0o644,
		modTime:    now,
		accessTime: now,
	}
	return nil
}

func (n *memoryNamespace) DeleteFile(_ context.Context, name string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	cleaned := normalizeExecPath(name)
	if file := n.files[cleaned]; file != nil {
		n.releaseBytesLocked(int64(len(file.content)))
		delete(n.files, cleaned)
		return nil
	}
	if _, ok := n.dirs[cleaned]; ok {
		if n.hasChildren(cleaned) {
			return syscall.ENOTEMPTY
		}
		if cleaned != "/" {
			delete(n.dirs, cleaned)
		}
		return nil
	}
	return syscall.ENOENT
}

func (n *memoryNamespace) Rename(_ context.Context, oldName, newName string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	oldPath := normalizeExecPath(oldName)
	newPath := normalizeExecPath(newName)
	if file := n.files[oldPath]; file != nil {
		return n.renameFileLocked(oldPath, newPath, file)
	}
	if dir := n.dirs[oldPath]; dir != nil {
		return n.renameDirLocked(oldPath, newPath, dir)
	}
	return syscall.ENOENT
}

func (n *memoryNamespace) ReadOnly() bool {
	return false
}

func (n *memoryNamespace) ensureDirs(dir string) {
	for current := normalizeExecPath(dir); current != "."; current = path.Dir(current) {
		if _, ok := n.dirs[current]; ok {
			return
		}
		n.dirs[current] = &memoryDir{
			mode:    fs.ModeDir | 0o755,
			modTime: time.Now().UTC(),
		}
		if current == "/" {
			return
		}
	}
}

func (n *memoryNamespace) hasChildren(dir string) bool {
	for name := range n.files {
		if hasExecPrefix(name, dir) {
			return true
		}
	}
	for name := range n.dirs {
		if name != dir && hasExecPrefix(name, dir) {
			return true
		}
	}
	return false
}

func (n *memoryNamespace) adjustFileBytesLocked(name string, next int) error {
	current := 0
	if file := n.files[name]; file != nil {
		// len(file.content) — the bytes actually held in memory, which
		// is what the budget tracks. A compressed file's current usage is
		// its compressed length, not its logical size.
		current = len(file.content)
	}
	delta := int64(next - current)
	if delta > 0 {
		if err := n.reserveBytesLocked(delta); err != nil {
			// Pressure: compress cold files to make room, then retry. If
			// the compression pass still can't free enough, propagate the
			// original error — the user's budget is genuinely too small.
			if freed := n.compressColdFilesLocked(delta); freed >= delta {
				if retryErr := n.reserveBytesLocked(delta); retryErr == nil {
					return nil
				}
			}
			return err
		}
		return nil
	}
	n.releaseBytesLocked(-delta)
	return nil
}

func (n *memoryNamespace) reserveBytesLocked(bytes int64) error {
	if n.budget == nil {
		return nil
	}
	return n.budget.Reserve(bytes)
}

func (n *memoryNamespace) releaseBytesLocked(bytes int64) {
	if n.budget == nil {
		return
	}
	n.budget.Release(bytes)
}

func (n *memoryNamespace) renameFileLocked(oldPath, newPath string, file *memoryFile) error {
	if oldPath == newPath {
		return nil
	}
	n.ensureDirs(path.Dir(newPath))
	n.releaseExistingPathLocked(newPath)
	n.files[newPath] = file
	delete(n.files, oldPath)
	return nil
}

func (n *memoryNamespace) renameDirLocked(oldPath, newPath string, dir *memoryDir) error {
	if oldPath == newPath {
		return nil
	}
	if hasExecPrefix(newPath, oldPath) {
		return syscall.EINVAL
	}
	n.ensureDirs(path.Dir(newPath))
	n.releaseExistingPathLocked(newPath)
	n.dirs[newPath] = dir
	delete(n.dirs, oldPath)
	n.moveFilesLocked(oldPath, newPath)
	n.moveDirsLocked(oldPath, newPath)
	return nil
}

func (n *memoryNamespace) releaseExistingPathLocked(name string) {
	if file := n.files[name]; file != nil {
		n.releaseBytesLocked(int64(len(file.content)))
		delete(n.files, name)
	}
	if dir := n.dirs[name]; dir != nil && !n.hasChildren(name) {
		delete(n.dirs, name)
	}
}

func (n *memoryNamespace) moveFilesLocked(oldPath, newPath string) {
	for filePath, file := range n.files {
		if !hasExecPrefix(filePath, oldPath) {
			continue
		}
		rel := strings.TrimPrefix(filePath, oldPath)
		n.files[newPath+rel] = file
		delete(n.files, filePath)
	}
}

func (n *memoryNamespace) moveDirsLocked(oldPath, newPath string) {
	for dirPath, dir := range n.dirs {
		if dirPath == oldPath || !hasExecPrefix(dirPath, oldPath) {
			continue
		}
		rel := strings.TrimPrefix(dirPath, oldPath)
		n.dirs[newPath+rel] = dir
		delete(n.dirs, dirPath)
	}
}

func (f projectionFileInfo) Name() string       { return f.name }
func (f projectionFileInfo) Size() int64        { return f.size }
func (f projectionFileInfo) Mode() fs.FileMode  { return f.mode }
func (f projectionFileInfo) ModTime() time.Time { return f.modTime }
func (f projectionFileInfo) IsDir() bool        { return f.mode.IsDir() }
func (f projectionFileInfo) Sys() any           { return nil }

func (e projectionDirEntry) Name() string               { return e.name }
func (e projectionDirEntry) IsDir() bool                { return e.info.IsDir() }
func (e projectionDirEntry) Type() fs.FileMode          { return e.info.Mode().Type() }
func (e projectionDirEntry) Info() (fs.FileInfo, error) { return e.info, nil }

func normalizeExecPath(name string) string {
	cleaned := path.Clean("/" + strings.TrimSpace(strings.ReplaceAll(name, "\\", "/")))
	if cleaned == "." {
		return "/"
	}
	return cleaned
}

func checkedWriteBounds(offset int64, length int) (int, int, error) {
	if offset < 0 {
		return 0, 0, syscall.EINVAL
	}
	end := offset + int64(length)
	if end < offset || end > int64(^uint(0)>>1) {
		return 0, 0, syscall.EFBIG
	}
	return int(offset), int(end), nil
}

func checkedBufferSize(size int64) (int, error) {
	if size < 0 {
		return 0, syscall.EINVAL
	}
	if size > int64(^uint(0)>>1) {
		return 0, syscall.EFBIG
	}
	return int(size), nil
}

func hasExecPrefix(name, prefix string) bool {
	if prefix == "/" {
		return strings.HasPrefix(name, "/") && name != "/"
	}
	return strings.HasPrefix(name, prefix+"/")
}

func staticDirInfo(name string) fs.FileInfo {
	return projectionFileInfo{
		name:    dirName(name),
		mode:    fs.ModeDir | 0o755,
		modTime: time.Now().UTC(),
	}
}

func dirName(name string) string {
	if normalizeExecPath(name) == "/" {
		return "/"
	}
	return path.Base(name)
}

func translateExecutionFSError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, ErrMemoryPressure):
		return syscall.ENOMEM
	case errors.Is(err, fs.ErrNotExist), errors.Is(err, os.ErrNotExist):
		return syscall.ENOENT
	case errors.Is(err, fs.ErrExist):
		return syscall.EEXIST
	case errors.Is(err, fs.ErrPermission):
		return syscall.EPERM
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno
	}
	return err
}

func growBytes(content []byte, size int) []byte {
	if size <= len(content) {
		return content
	}
	next := make([]byte, size)
	copy(next, content)
	return next
}

func resizeBytes(content []byte, size int) []byte {
	if size <= len(content) {
		return cloneBytes(content[:size])
	}
	return growBytes(content, size)
}

func cloneBytes(content []byte) []byte {
	if len(content) == 0 {
		return nil
	}
	out := make([]byte, len(content))
	copy(out, content)
	return out
}
