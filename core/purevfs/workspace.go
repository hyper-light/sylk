package purevfs

import (
	"errors"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/benbjohnson/immutable"
)

var (
	ErrSnapshotNotFound  = errors.New("purevfs: snapshot not found")
	ErrBranchNotFound    = errors.New("purevfs: branch not found")
	ErrInvalidPath       = errors.New("purevfs: invalid path")
	ErrPathNotFound      = errors.New("purevfs: path not found")
	ErrAlreadyExists     = errors.New("purevfs: path already exists")
	ErrNotDirectory      = errors.New("purevfs: not a directory")
	ErrNotFile           = errors.New("purevfs: not a regular file")
	ErrDirectoryNotEmpty = errors.New("purevfs: directory not empty")
	ErrHandleNotFound    = errors.New("purevfs: handle not found")
	ErrReadOnlyHandle    = errors.New("purevfs: handle is read-only")
	ErrHandleConflict    = errors.New("purevfs: handle base state diverged")
)

type SnapshotID uint64

type JournalOp string

const (
	JournalCreate   JournalOp = "create"
	JournalModify   JournalOp = "modify"
	JournalDelete   JournalOp = "delete"
	JournalRename   JournalOp = "rename"
	JournalMkdir    JournalOp = "mkdir"
	JournalSymlink  JournalOp = "symlink"
	JournalLink     JournalOp = "link"
	JournalSeed     JournalOp = "seed"
	JournalTruncate JournalOp = "truncate"
)

type JournalEntry struct {
	Seq           uint64
	Branch        string
	Snapshot      SnapshotID
	Op            JournalOp
	Path          string
	SecondaryPath string
	Inode         uint64
	Timestamp     time.Time
}

// Snapshot is one immutable point-in-time view of the workspace.
//
// Inodes is a persistent hash-array-mapped trie (HAMT) keyed by inode
// ID. It is shared structurally across snapshots: a mutation
// produces a new *immutable.Map that reuses every unchanged trie
// node from the parent. With S retained snapshots and N inodes,
// total memory is O(N + S × log N) — versus the O(N × S) cost of
// the previous full-clone-per-mutation design that pinned hundreds
// of MiB of duplicated inode tables in heap.
//
// All mutators MUST treat Inodes as immutable and reassign with
// Set/Delete. Direct map-style writes are impossible (the type is
// not a map) so the old `newInodes[id] = …` shape will not compile.
type Snapshot struct {
	ID        SnapshotID
	Parent    SnapshotID
	Root      *treeNode
	Inodes    *immutable.Map[uint64, *Inode]
	CreatedAt time.Time
	Reason    string
	JournalLo uint64
	JournalHi uint64
}

type SnapshotInfo struct {
	ID        SnapshotID
	Parent    SnapshotID
	CreatedAt time.Time
	Reason    string
	JournalLo uint64
	JournalHi uint64
}

type EntryInfo struct {
	Name       string
	Path       string
	Snapshot   SnapshotID
	Inode      uint64
	Kind       EntryKind
	Mode       fs.FileMode
	ModTime    time.Time
	Size       int64
	Nlink      int
	LinkTarget string
}

type OpenFile struct {
	FH           uint64
	Branch       string
	Path         string
	BaseSnapshot SnapshotID
	BaseInode    uint64
	Writable     bool
	Dirty        bool
	Mode         fs.FileMode
	Body         *FileBody
}

type treeNode struct {
	Inode    uint64
	Children map[string]*treeNode
}

type Workspace struct {
	mu           sync.RWMutex
	store        *ChunkStore
	reader       RealFileReader
	snapshots    map[SnapshotID]*Snapshot
	branches     map[string]SnapshotID
	handles      map[uint64]*OpenFile
	journal      []JournalEntry
	nextSnapshot SnapshotID
	nextInode    uint64
	nextHandle   uint64
	nextJournal  uint64

	// branchGens tracks per-branch monotonic generations used by
	// WriteSession to fence handoffs. Allocated lazily on first
	// BeginWriteSession to keep workspaces that never use the seal
	// API zero-cost.
	branchGens *branchGenerations
}

func NewWorkspace(store *ChunkStore, reader RealFileReader) *Workspace {
	if store == nil {
		store = NewChunkStore(0)
	}
	if reader == nil {
		reader = OSRealFileReader{}
	}

	now := time.Now().UTC()
	rootInode := &Inode{
		ID:      1,
		Kind:    EntryDir,
		Mode:    0o755 | fs.ModeDir,
		ModTime: now,
		Nlink:   1,
	}
	root := &treeNode{
		Inode:    1,
		Children: make(map[string]*treeNode),
	}
	initial := &Snapshot{
		ID:        1,
		Root:      root,
		Inodes:    immutable.NewMap[uint64, *Inode](nil).Set(1, rootInode),
		CreatedAt: now,
		Reason:    "workspace init",
	}

	return &Workspace{
		store:        store,
		reader:       reader,
		snapshots:    map[SnapshotID]*Snapshot{1: initial},
		branches:     map[string]SnapshotID{"main": 1},
		handles:      make(map[uint64]*OpenFile),
		nextSnapshot: 1,
		nextInode:    1,
		nextHandle:   0,
		nextJournal:  0,
	}
}

func (w *Workspace) ChunkStore() *ChunkStore {
	return w.store
}

// Close releases the workspace's underlying chunk arena. Call when the
// workspace (and any FileBody it produced) is being permanently torn
// down — without it, anonymous mmap regions stay mapped until process
// exit. Idempotent and safe to call on a workspace whose store was
// passed in by an external owner; the external owner must take care
// not to call Close twice on the same store.
func (w *Workspace) Close() {
	if w == nil || w.store == nil {
		return
	}
	w.store.Close()
}

func (w *Workspace) Head(branch string) (SnapshotID, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()
	id, ok := w.branches[strings.TrimSpace(branch)]
	if !ok {
		return 0, ErrBranchNotFound
	}
	return id, nil
}

func (w *Workspace) CreateBranch(name string, base SnapshotID) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrBranchNotFound
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if _, ok := w.snapshots[base]; !ok {
		return ErrSnapshotNotFound
	}
	if _, exists := w.branches[name]; exists {
		return ErrAlreadyExists
	}
	w.branches[name] = base
	return nil
}

func (w *Workspace) ForkBranch(src, dst string) error {
	head, err := w.Head(src)
	if err != nil {
		return err
	}
	return w.CreateBranch(dst, head)
}

func (w *Workspace) SnapshotInfo(id SnapshotID) (SnapshotInfo, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	snap, ok := w.snapshots[id]
	if !ok {
		return SnapshotInfo{}, ErrSnapshotNotFound
	}
	return snapshotInfo(snap), nil
}

func (w *Workspace) Snapshots() []SnapshotInfo {
	w.mu.RLock()
	defer w.mu.RUnlock()

	out := make([]SnapshotInfo, 0, len(w.snapshots))
	for _, snap := range w.snapshots {
		out = append(out, snapshotInfo(snap))
	}
	slices.SortFunc(out, func(a, b SnapshotInfo) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	return out
}

func snapshotInfo(snap *Snapshot) SnapshotInfo {
	return SnapshotInfo{
		ID:        snap.ID,
		Parent:    snap.Parent,
		CreatedAt: snap.CreatedAt,
		Reason:    snap.Reason,
		JournalLo: snap.JournalLo,
		JournalHi: snap.JournalHi,
	}
}

func (w *Workspace) JournalSince(seq uint64) []JournalEntry {
	w.mu.RLock()
	defer w.mu.RUnlock()

	idx := slices.IndexFunc(w.journal, func(entry JournalEntry) bool {
		return entry.Seq > seq
	})
	if idx < 0 {
		return nil
	}
	out := make([]JournalEntry, len(w.journal[idx:]))
	copy(out, w.journal[idx:])
	return out
}

func (w *Workspace) ReadFile(snapshot SnapshotID, filePath string) ([]byte, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	snap, err := w.snapshotLocked(snapshot)
	if err != nil {
		return nil, err
	}
	_, inode, err := w.resolveLocked(snap, filePath)
	if err != nil {
		return nil, err
	}
	if inode.Kind != EntryFile {
		return nil, ErrNotFile
	}
	return w.readInodeLocked(inode)
}

func (w *Workspace) ReadFileFromBranch(branch, filePath string) ([]byte, error) {
	head, err := w.Head(branch)
	if err != nil {
		return nil, err
	}
	return w.ReadFile(head, filePath)
}

func (w *Workspace) Stat(snapshot SnapshotID, filePath string) (EntryInfo, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	snap, err := w.snapshotLocked(snapshot)
	if err != nil {
		return EntryInfo{}, err
	}
	node, inode, err := w.resolveLocked(snap, filePath)
	if err != nil {
		return EntryInfo{}, err
	}
	return buildEntryInfo(snapshot, cleanVFSPath(filePath), path.Base(cleanVFSPath(filePath)), node, inode), nil
}

func (w *Workspace) ListDir(snapshot SnapshotID, dirPath string) ([]EntryInfo, error) {
	w.mu.RLock()
	defer w.mu.RUnlock()

	snap, err := w.snapshotLocked(snapshot)
	if err != nil {
		return nil, err
	}
	node, inode, err := w.resolveLocked(snap, dirPath)
	if err != nil {
		return nil, err
	}
	if inode.Kind != EntryDir {
		return nil, ErrNotDirectory
	}
	names := make([]string, 0, len(node.Children))
	for name := range node.Children {
		names = append(names, name)
	}
	slices.Sort(names)
	out := make([]EntryInfo, 0, len(names))
	root := cleanVFSPath(dirPath)
	for _, name := range names {
		child := node.Children[name]
		childInode, _ := snap.Inodes.Get(child.Inode)
		out = append(out, buildEntryInfo(snapshot, path.Join(root, name), name, child, childInode))
	}
	return out, nil
}

func (w *Workspace) Exists(snapshot SnapshotID, filePath string) bool {
	_, err := w.Stat(snapshot, filePath)
	return err == nil
}

func (w *Workspace) Mkdir(branch, dirPath string, mode fs.FileMode) (SnapshotID, error) {
	return w.applyMutation(branch, cleanVFSPath(dirPath), func(snap *Snapshot, target string) (*Snapshot, uint64, error) {
		if target == "/" {
			return nil, 0, ErrAlreadyExists
		}
		parentPath, name := path.Split(target)
		parentPath = strings.TrimSuffix(parentPath, "/")
		if parentPath == "" {
			parentPath = "/"
		}
		parentNode, parentInode, err := w.resolveLocked(snap, parentPath)
		if err != nil {
			return nil, 0, err
		}
		if parentInode.Kind != EntryDir {
			return nil, 0, ErrNotDirectory
		}
		if _, exists := parentNode.Children[name]; exists {
			return nil, 0, ErrAlreadyExists
		}

		newInodeID := w.nextInodeLocked()
		now := time.Now().UTC()
		mutParent := parentInode.Clone()
		mutParent.ModTime = now
		newInodes := snap.Inodes.
			Set(newInodeID, &Inode{
				ID:      newInodeID,
				Kind:    EntryDir,
				Mode:    (mode & fs.ModePerm) | fs.ModeDir,
				ModTime: now,
				Nlink:   1,
			}).
			Set(parentNode.Inode, mutParent)

		newRoot, clonedParent, err := clonePathTo(snap.Root, splitVFSPath(parentPath))
		if err != nil {
			return nil, 0, err
		}
		clonedParent.Children[name] = &treeNode{
			Inode:    newInodeID,
			Children: make(map[string]*treeNode),
		}
		return w.finalizeSnapshotLocked(snap, newRoot, newInodes, "mkdir "+target), newInodeID, nil
	}, JournalMkdir, "")
}

func (w *Workspace) WriteFile(branch, filePath string, content []byte, mode fs.FileMode) (SnapshotID, error) {
	body := NewEmptyFileBody()
	if err := body.WriteAt(0, content, w.store); err != nil {
		return 0, err
	}
	return w.writeBody(branch, filePath, body, mode, JournalCreate, JournalModify)
}

func (w *Workspace) WriteFileBody(branch, filePath string, body *FileBody, mode fs.FileMode) (SnapshotID, error) {
	return w.writeBody(branch, filePath, body, mode, JournalCreate, JournalModify)
}

func (w *Workspace) SeedRealFile(branch, filePath, realPath string, mode fs.FileMode, size int64) (SnapshotID, error) {
	body := NewRealFileBody(realPath, size)
	return w.writeBody(branch, filePath, body, mode, JournalSeed, JournalSeed)
}

func (w *Workspace) writeBody(
	branch, filePath string,
	body *FileBody,
	mode fs.FileMode,
	createOp, modifyOp JournalOp,
) (SnapshotID, error) {
	filePath = cleanVFSPath(filePath)
	w.mu.Lock()
	defer w.mu.Unlock()

	snap, err := w.branchSnapshotLocked(branch)
	if err != nil {
		return 0, err
	}
	_, _, lookupErr := w.resolveLocked(snap, filePath)
	creating := errors.Is(lookupErr, ErrPathNotFound)

	next, inodeID, err := w.writeBodyOnSnapshotLocked(snap, filePath, body, mode)
	if err != nil {
		return 0, err
	}
	w.branches[branch] = next.ID
	if creating {
		w.appendJournalLocked(branch, next.ID, createOp, filePath, "", inodeID)
	} else {
		w.appendJournalLocked(branch, next.ID, modifyOp, filePath, "", inodeID)
	}
	return next.ID, nil
}

func (w *Workspace) Delete(branch, filePath string) (SnapshotID, error) {
	filePath = cleanVFSPath(filePath)
	return w.applyMutation(branch, filePath, func(snap *Snapshot, target string) (*Snapshot, uint64, error) {
		if target == "/" {
			return nil, 0, ErrInvalidPath
		}
		parentPath, name := path.Split(target)
		parentPath = strings.TrimSuffix(parentPath, "/")
		if parentPath == "" {
			parentPath = "/"
		}
		parentNode, parentInode, err := w.resolveLocked(snap, parentPath)
		if err != nil {
			return nil, 0, err
		}
		child, ok := parentNode.Children[name]
		if !ok {
			return nil, 0, ErrPathNotFound
		}
		childInode, _ := snap.Inodes.Get(child.Inode)
		if childInode == nil {
			return nil, 0, ErrPathNotFound
		}
		if childInode.Kind == EntryDir && len(child.Children) > 0 {
			return nil, 0, ErrDirectoryNotEmpty
		}

		newRoot, clonedParent, err := clonePathTo(snap.Root, splitVFSPath(parentPath))
		if err != nil {
			return nil, 0, err
		}
		delete(clonedParent.Children, name)

		now := time.Now().UTC()
		parentUpdated := parentInode.Clone()
		parentUpdated.ModTime = now
		newInodes := snap.Inodes.Set(parentNode.Inode, parentUpdated)

		if childInode.Nlink <= 1 {
			newInodes = newInodes.Delete(child.Inode)
		} else {
			updated := childInode.Clone()
			updated.Nlink--
			updated.ModTime = now
			newInodes = newInodes.Set(child.Inode, updated)
		}

		return w.finalizeSnapshotLocked(snap, newRoot, newInodes, "delete "+target), child.Inode, nil
	}, JournalDelete, "")
}

func (w *Workspace) Rename(branch, oldPath, newPath string) (SnapshotID, error) {
	oldPath = cleanVFSPath(oldPath)
	newPath = cleanVFSPath(newPath)
	if oldPath == newPath {
		head, err := w.Head(branch)
		if err != nil {
			return 0, err
		}
		return head, nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	snap, err := w.branchSnapshotLocked(branch)
	if err != nil {
		return 0, err
	}

	oldParentPath, oldName := path.Split(oldPath)
	oldParentPath = normalizedParent(oldParentPath)
	newParentPath, newName := path.Split(newPath)
	newParentPath = normalizedParent(newParentPath)

	oldParentNode, oldParentInode, err := w.resolveLocked(snap, oldParentPath)
	if err != nil {
		return 0, err
	}
	srcNode, ok := oldParentNode.Children[oldName]
	if !ok {
		return 0, ErrPathNotFound
	}
	srcInode, _ := snap.Inodes.Get(srcNode.Inode)
	if srcInode == nil {
		return 0, ErrPathNotFound
	}

	newParentNode, newParentInode, err := w.resolveLocked(snap, newParentPath)
	if err != nil {
		return 0, err
	}
	if newParentInode.Kind != EntryDir {
		return 0, ErrNotDirectory
	}
	if srcInode.Kind == EntryDir && strings.HasPrefix(newPath+"/", oldPath+"/") {
		return 0, ErrInvalidPath
	}

	now := time.Now().UTC()
	parentUpdated := oldParentInode.Clone()
	parentUpdated.ModTime = now
	newInodes := snap.Inodes.Set(oldParentNode.Inode, parentUpdated)
	if oldParentNode.Inode != newParentNode.Inode {
		dstParent := newParentInode.Clone()
		dstParent.ModTime = now
		newInodes = newInodes.Set(newParentNode.Inode, dstParent)
	} else {
		newInodes = newInodes.Set(newParentNode.Inode, parentUpdated)
	}

	newRoot, clonedOldParent, err := clonePathTo(snap.Root, splitVFSPath(oldParentPath))
	if err != nil {
		return 0, err
	}
	delete(clonedOldParent.Children, oldName)

	newRoot, clonedNewParent, err := clonePathTo(newRoot, splitVFSPath(newParentPath))
	if err != nil {
		return 0, err
	}

	if existing, exists := newParentNode.Children[newName]; exists {
		existingInode, _ := snap.Inodes.Get(existing.Inode)
		if existingInode == nil {
			return 0, ErrPathNotFound
		}
		if existingInode.Kind == EntryDir && len(existing.Children) > 0 {
			return 0, ErrDirectoryNotEmpty
		}
		if existingInode.Nlink <= 1 {
			newInodes = newInodes.Delete(existing.Inode)
		} else {
			updated := existingInode.Clone()
			updated.Nlink--
			updated.ModTime = now
			newInodes = newInodes.Set(existing.Inode, updated)
		}
	}

	clonedNewParent.Children[newName] = &treeNode{
		Inode:    srcNode.Inode,
		Children: srcNode.Children,
	}

	next := w.finalizeSnapshotLocked(snap, newRoot, newInodes, "rename "+oldPath+" -> "+newPath)
	w.branches[branch] = next.ID
	w.appendJournalLocked(branch, next.ID, JournalRename, oldPath, newPath, srcNode.Inode)
	return next.ID, nil
}

func (w *Workspace) Link(branch, existingPath, newPath string) (SnapshotID, error) {
	existingPath = cleanVFSPath(existingPath)
	newPath = cleanVFSPath(newPath)
	return w.applyMutation(branch, newPath, func(snap *Snapshot, target string) (*Snapshot, uint64, error) {
		srcNode, srcInode, err := w.resolveLocked(snap, existingPath)
		if err != nil {
			return nil, 0, err
		}
		if srcInode.Kind == EntryDir {
			return nil, 0, ErrNotFile
		}
		parentPath, name := path.Split(target)
		parentPath = normalizedParent(parentPath)
		parentNode, parentInode, err := w.resolveLocked(snap, parentPath)
		if err != nil {
			return nil, 0, err
		}
		if _, exists := parentNode.Children[name]; exists {
			return nil, 0, ErrAlreadyExists
		}

		now := time.Now().UTC()
		updated := srcInode.Clone()
		updated.Nlink++
		updated.ModTime = now
		parentUpdated := parentInode.Clone()
		parentUpdated.ModTime = now
		newInodes := snap.Inodes.
			Set(srcNode.Inode, updated).
			Set(parentNode.Inode, parentUpdated)

		newRoot, clonedParent, err := clonePathTo(snap.Root, splitVFSPath(parentPath))
		if err != nil {
			return nil, 0, err
		}
		clonedParent.Children[name] = &treeNode{Inode: srcNode.Inode}
		return w.finalizeSnapshotLocked(snap, newRoot, newInodes, "link "+existingPath+" -> "+target), srcNode.Inode, nil
	}, JournalLink, existingPath)
}

func (w *Workspace) Symlink(branch, linkPath, target string, mode fs.FileMode) (SnapshotID, error) {
	linkPath = cleanVFSPath(linkPath)
	return w.applyMutation(branch, linkPath, func(snap *Snapshot, vfsPath string) (*Snapshot, uint64, error) {
		parentPath, name := path.Split(vfsPath)
		parentPath = normalizedParent(parentPath)
		parentNode, parentInode, err := w.resolveLocked(snap, parentPath)
		if err != nil {
			return nil, 0, err
		}
		if _, exists := parentNode.Children[name]; exists {
			return nil, 0, ErrAlreadyExists
		}
		now := time.Now().UTC()
		newInodeID := w.nextInodeLocked()
		parentUpdated := parentInode.Clone()
		parentUpdated.ModTime = now
		newInodes := snap.Inodes.
			Set(newInodeID, &Inode{
				ID:         newInodeID,
				Kind:       EntrySymlink,
				Mode:       (mode & fs.ModePerm) | fs.ModeSymlink,
				ModTime:    now,
				Nlink:      1,
				LinkTarget: target,
			}).
			Set(parentNode.Inode, parentUpdated)

		newRoot, clonedParent, err := clonePathTo(snap.Root, splitVFSPath(parentPath))
		if err != nil {
			return nil, 0, err
		}
		clonedParent.Children[name] = &treeNode{Inode: newInodeID}
		return w.finalizeSnapshotLocked(snap, newRoot, newInodes, "symlink "+vfsPath), newInodeID, nil
	}, JournalSymlink, target)
}

func (w *Workspace) OpenRead(snapshot SnapshotID, filePath string) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	snap, err := w.snapshotLocked(snapshot)
	if err != nil {
		return 0, err
	}
	_, inode, err := w.resolveLocked(snap, filePath)
	if err != nil {
		return 0, err
	}
	if inode.Kind != EntryFile {
		return 0, ErrNotFile
	}

	fh := w.nextHandleLocked()
	w.handles[fh] = &OpenFile{
		FH:           fh,
		BaseSnapshot: snapshot,
		BaseInode:    inode.ID,
		Body:         inode.Body.Clone(),
		Mode:         inode.Mode,
	}
	return fh, nil
}

func (w *Workspace) OpenWrite(branch, filePath string, create bool, mode fs.FileMode) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	snap, err := w.branchSnapshotLocked(branch)
	if err != nil {
		return 0, err
	}
	node, inode, err := w.resolveLocked(snap, filePath)
	if err != nil {
		if !create || !errors.Is(err, ErrPathNotFound) {
			return 0, err
		}
		fh := w.nextHandleLocked()
		w.handles[fh] = &OpenFile{
			FH:           fh,
			Branch:       branch,
			Path:         cleanVFSPath(filePath),
			BaseSnapshot: snap.ID,
			BaseInode:    0,
			Writable:     true,
			Mode:         mode & fs.ModePerm,
			Body:         NewEmptyFileBody(),
		}
		return fh, nil
	}
	if inode.Kind != EntryFile {
		return 0, ErrNotFile
	}
	fh := w.nextHandleLocked()
	w.handles[fh] = &OpenFile{
		FH:           fh,
		Branch:       branch,
		Path:         cleanVFSPath(filePath),
		BaseSnapshot: snap.ID,
		BaseInode:    node.Inode,
		Writable:     true,
		Mode:         inode.Mode,
		Body:         inode.Body.Clone(),
	}
	return fh, nil
}

func (w *Workspace) ReadHandle(fh uint64, p []byte, off int64) (int, error) {
	w.mu.RLock()
	handle, ok := w.handles[fh]
	w.mu.RUnlock()
	if !ok {
		return 0, ErrHandleNotFound
	}
	return handle.Body.ReadAt(p, off, w.reader, w.store)
}

func (w *Workspace) WriteHandle(fh uint64, content []byte, off int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	handle, ok := w.handles[fh]
	if !ok {
		return ErrHandleNotFound
	}
	if !handle.Writable {
		return ErrReadOnlyHandle
	}
	if err := handle.Body.WriteAt(off, content, w.store); err != nil {
		return err
	}
	handle.Dirty = true
	return nil
}

func (w *Workspace) TruncateHandle(fh uint64, size int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	handle, ok := w.handles[fh]
	if !ok {
		return ErrHandleNotFound
	}
	if !handle.Writable {
		return ErrReadOnlyHandle
	}
	if err := handle.Body.Truncate(size); err != nil {
		return err
	}
	handle.Dirty = true
	return nil
}

func (w *Workspace) CloseHandle(fh uint64) error {
	w.mu.Lock()
	handle, ok := w.handles[fh]
	if !ok {
		w.mu.Unlock()
		return ErrHandleNotFound
	}
	delete(w.handles, fh)
	w.mu.Unlock()

	if !handle.Writable || !handle.Dirty {
		return nil
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	current, err := w.branchSnapshotLocked(handle.Branch)
	if err != nil {
		return err
	}
	base, err := w.snapshotLocked(handle.BaseSnapshot)
	if err != nil {
		return err
	}
	if !entriesEquivalent(base, current, cleanVFSPath(handle.Path)) {
		return ErrHandleConflict
	}

	_, _, err = w.writeBodyLocked(handle.Branch, cleanVFSPath(handle.Path), handle.Body, handle.Mode)
	return err
}

func (w *Workspace) applyMutation(
	branch string,
	target string,
	mutator func(snap *Snapshot, target string) (*Snapshot, uint64, error),
	op JournalOp,
	secondary string,
) (SnapshotID, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	snap, err := w.branchSnapshotLocked(branch)
	if err != nil {
		return 0, err
	}
	next, inodeID, err := mutator(snap, target)
	if err != nil {
		return 0, err
	}
	w.branches[branch] = next.ID
	w.appendJournalLocked(branch, next.ID, op, target, secondary, inodeID)
	return next.ID, nil
}

func (w *Workspace) writeBodyLocked(branch, filePath string, body *FileBody, mode fs.FileMode) (SnapshotID, uint64, error) {
	snap, err := w.branchSnapshotLocked(branch)
	if err != nil {
		return 0, 0, err
	}
	_, _, lookupErr := w.resolveLocked(snap, filePath)
	creating := errors.Is(lookupErr, ErrPathNotFound)
	next, inodeID, err := w.writeBodyOnSnapshotLocked(snap, filePath, body, mode)
	if err != nil {
		return 0, 0, err
	}
	w.branches[branch] = next.ID
	if inodeID == 0 {
		return next.ID, inodeID, nil
	}
	op := JournalModify
	if creating {
		op = JournalCreate
	}
	w.appendJournalLocked(branch, next.ID, op, filePath, "", inodeID)
	return next.ID, inodeID, nil
}

func (w *Workspace) writeBodyOnSnapshotLocked(snap *Snapshot, filePath string, body *FileBody, mode fs.FileMode) (*Snapshot, uint64, error) {
	parentPath, name := path.Split(filePath)
	parentPath = normalizedParent(parentPath)
	parentNode, parentInode, err := w.resolveLocked(snap, parentPath)
	if err != nil {
		return nil, 0, err
	}
	if parentInode.Kind != EntryDir {
		return nil, 0, ErrNotDirectory
	}
	now := time.Now().UTC()
	newRoot, clonedParent, err := clonePathTo(snap.Root, splitVFSPath(parentPath))
	if err != nil {
		return nil, 0, err
	}
	if existing, exists := parentNode.Children[name]; exists {
		inode, _ := snap.Inodes.Get(existing.Inode)
		if inode == nil {
			return nil, 0, ErrPathNotFound
		}
		if inode.Kind != EntryFile {
			return nil, 0, ErrNotFile
		}
		updated := inode.Clone()
		updated.Mode = mode & fs.ModePerm
		updated.ModTime = now
		updated.Body = body.Clone()
		parentUpdated := parentInode.Clone()
		parentUpdated.ModTime = now
		newInodes := snap.Inodes.
			Set(existing.Inode, updated).
			Set(parentNode.Inode, parentUpdated)
		return w.finalizeSnapshotLocked(snap, newRoot, newInodes, "write "+filePath), existing.Inode, nil
	}
	newInodeID := w.nextInodeLocked()
	parentUpdated := parentInode.Clone()
	parentUpdated.ModTime = now
	newInodes := snap.Inodes.
		Set(newInodeID, &Inode{
			ID:      newInodeID,
			Kind:    EntryFile,
			Mode:    mode & fs.ModePerm,
			ModTime: now,
			Nlink:   1,
			Body:    body.Clone(),
		}).
		Set(parentNode.Inode, parentUpdated)
	clonedParent.Children[name] = &treeNode{Inode: newInodeID}
	return w.finalizeSnapshotLocked(snap, newRoot, newInodes, "create "+filePath), newInodeID, nil
}

func (w *Workspace) finalizeSnapshotLocked(parent *Snapshot, root *treeNode, inodes *immutable.Map[uint64, *Inode], reason string) *Snapshot {
	w.nextSnapshot++
	id := w.nextSnapshot
	snap := &Snapshot{
		ID:        id,
		Parent:    parent.ID,
		Root:      root,
		Inodes:    inodes,
		CreatedAt: time.Now().UTC(),
		Reason:    reason,
	}
	w.snapshots[id] = snap
	return snap
}

func (w *Workspace) appendJournalLocked(branch string, snapshot SnapshotID, op JournalOp, filePath, secondary string, inodeID uint64) {
	w.nextJournal++
	entry := JournalEntry{
		Seq:           w.nextJournal,
		Branch:        branch,
		Snapshot:      snapshot,
		Op:            op,
		Path:          cleanVFSPath(filePath),
		SecondaryPath: secondary,
		Inode:         inodeID,
		Timestamp:     time.Now().UTC(),
	}
	w.journal = append(w.journal, entry)
	if snap := w.snapshots[snapshot]; snap != nil {
		if snap.JournalLo == 0 || entry.Seq < snap.JournalLo {
			snap.JournalLo = entry.Seq
		}
		if entry.Seq > snap.JournalHi {
			snap.JournalHi = entry.Seq
		}
	}
}

func (w *Workspace) snapshotLocked(id SnapshotID) (*Snapshot, error) {
	snap, ok := w.snapshots[id]
	if !ok {
		return nil, ErrSnapshotNotFound
	}
	return snap, nil
}

func (w *Workspace) branchSnapshotLocked(branch string) (*Snapshot, error) {
	id, ok := w.branches[strings.TrimSpace(branch)]
	if !ok {
		return nil, ErrBranchNotFound
	}
	return w.snapshotLocked(id)
}

func (w *Workspace) nextInodeLocked() uint64 {
	w.nextInode++
	return w.nextInode
}

func (w *Workspace) nextHandleLocked() uint64 {
	w.nextHandle++
	return w.nextHandle
}

func (w *Workspace) resolveLocked(snap *Snapshot, filePath string) (*treeNode, *Inode, error) {
	node, err := lookupNode(snap.Root, splitVFSPath(cleanVFSPath(filePath)))
	if err != nil {
		return nil, nil, err
	}
	inode, _ := snap.Inodes.Get(node.Inode)
	if inode == nil {
		return nil, nil, ErrPathNotFound
	}
	return node, inode, nil
}

func (w *Workspace) readInodeLocked(inode *Inode) ([]byte, error) {
	if inode == nil || inode.Kind != EntryFile {
		return nil, ErrNotFile
	}
	out := make([]byte, inode.Size())
	n, err := inode.Body.ReadAt(out, 0, w.reader, w.store)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	return out[:n], nil
}

func buildEntryInfo(snapshot SnapshotID, filePath, name string, _ *treeNode, inode *Inode) EntryInfo {
	return EntryInfo{
		Name:       name,
		Path:       filePath,
		Snapshot:   snapshot,
		Inode:      inode.ID,
		Kind:       inode.Kind,
		Mode:       inode.Mode,
		ModTime:    inode.ModTime,
		Size:       inode.Size(),
		Nlink:      inode.Nlink,
		LinkTarget: inode.LinkTarget,
	}
}

func cloneTreeNode(node *treeNode) *treeNode {
	if node == nil {
		return nil
	}
	out := &treeNode{Inode: node.Inode}
	if node.Children != nil {
		out.Children = make(map[string]*treeNode, len(node.Children))
		for name, child := range node.Children {
			out.Children[name] = child
		}
	}
	return out
}

func clonePathTo(root *treeNode, parts []string) (*treeNode, *treeNode, error) {
	newRoot := cloneTreeNode(root)
	currentNew := newRoot
	currentOld := root
	for _, part := range parts {
		childOld, ok := currentOld.Children[part]
		if !ok {
			return nil, nil, ErrPathNotFound
		}
		childNew := cloneTreeNode(childOld)
		currentNew.Children[part] = childNew
		currentOld = childOld
		currentNew = childNew
	}
	return newRoot, currentNew, nil
}

func lookupNode(root *treeNode, parts []string) (*treeNode, error) {
	current := root
	for _, part := range parts {
		next, ok := current.Children[part]
		if !ok {
			return nil, ErrPathNotFound
		}
		current = next
	}
	return current, nil
}

func splitVFSPath(filePath string) []string {
	cleaned := cleanVFSPath(filePath)
	if cleaned == "/" {
		return nil
	}
	parts := strings.Split(strings.TrimPrefix(cleaned, "/"), "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func cleanVFSPath(filePath string) string {
	if strings.TrimSpace(filePath) == "" {
		return "/"
	}
	cleaned := path.Clean("/" + strings.TrimSpace(filePath))
	if cleaned == "." {
		return "/"
	}
	return cleaned
}

func normalizedParent(parent string) string {
	parent = strings.TrimSuffix(parent, "/")
	if parent == "" {
		return "/"
	}
	return cleanVFSPath(parent)
}

func entriesEquivalent(a, b *Snapshot, filePath string) bool {
	aNode, aInode, aErr := resolveForComparison(a, filePath)
	bNode, bInode, bErr := resolveForComparison(b, filePath)
	if aErr != nil || bErr != nil {
		return errors.Is(aErr, ErrPathNotFound) && errors.Is(bErr, ErrPathNotFound)
	}
	if aNode.Inode != bNode.Inode {
		return false
	}
	return inodesEqual(aInode, bInode)
}

func resolveForComparison(snap *Snapshot, filePath string) (*treeNode, *Inode, error) {
	node, err := lookupNode(snap.Root, splitVFSPath(filePath))
	if err != nil {
		return nil, nil, err
	}
	inode, _ := snap.Inodes.Get(node.Inode)
	if inode == nil {
		return nil, nil, ErrPathNotFound
	}
	return node, inode, nil
}

func inodesEqual(a, b *Inode) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.ID != b.ID || a.Kind != b.Kind || a.Mode != b.Mode || a.Nlink != b.Nlink || a.LinkTarget != b.LinkTarget {
		return false
	}
	return bodiesEqual(a.Body, b.Body)
}

func bodiesEqual(a, b *FileBody) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.size != b.size || len(a.extents) != len(b.extents) {
		return false
	}
	for i := range a.extents {
		if a.extents[i] != b.extents[i] {
			return false
		}
	}
	return true
}
