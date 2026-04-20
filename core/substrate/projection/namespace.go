// Package projection serves substrate manifests as a read-only
// filesystem that the FUSE projection layer (and the execution
// backend) can mount alongside the existing pipeline / global / disk
// layers.
//
// A Namespace holds a set of Attachments, each pairing a mount path
// (e.g. "/python/site-packages" or "/bin") with a substrate manifest.
// Reads under a mount path resolve to the manifest's file entries,
// served from the substrate's blob store. Writes are refused — the
// substrate is canonical from the agent's perspective and
// content-addressing means a "write to a substrate file" is meaningless
// (the new bytes would just be a different blob, not an update to the
// existing one).
//
// The Namespace's surface mirrors the unexported executionNamespace
// interface in core/purevfs (Stat, ListDir, ReadFile, MkdirAll,
// WriteFile, DeleteFile, Rename, ReadOnly). Phase 6's execution
// backend wires it into the broker's projection composer.
package projection

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/adalundhe/sylk/core/substrate/blob"
	"github.com/adalundhe/sylk/core/substrate/manifest"
)

// Namespace is the substrate's read-only filesystem projection.
//
// Multiple manifests can be attached at distinct mount paths. The
// Namespace synthesizes the directory tree across all attachments and
// resolves reads through the underlying blob store.
//
// Safe for concurrent use. Attach/Detach take a write lock; reads take
// a read lock.
type Namespace struct {
	blobs *blob.Store

	mu          sync.RWMutex
	attachments []*attachment
	// dirs is the synthesized directory tree across all attachments.
	// It is rebuilt on every Attach/Detach. Keys are normalized paths.
	dirs map[string]struct{}
	// fileIndex maps normalized substrate-relative paths to the
	// (attachment, file_entry) that owns them. Built on Attach so
	// Stat/Read are O(1) lookups.
	fileIndex map[string]fileLocation
}

// attachment pairs a mount path with the manifest hosted there.
type attachment struct {
	mountPath string // normalized, leading slash, no trailing slash unless root
	manifest  *manifest.Manifest
}

// fileLocation records which file entry serves a given path.
type fileLocation struct {
	owner *attachment
	entry manifest.FileEntry
}

// New constructs an empty Namespace backed by blobs.
func New(blobs *blob.Store) (*Namespace, error) {
	if blobs == nil {
		return nil, ErrNilBlobStore
	}
	return &Namespace{
		blobs:     blobs,
		dirs:      map[string]struct{}{"/": {}},
		fileIndex: make(map[string]fileLocation),
	}, nil
}

// Attachment is the public input describing where to mount a manifest.
type Attachment struct {
	// MountPath is the substrate-relative directory the manifest's
	// files live under (e.g. "/python/site-packages",
	// "/bin", "/lib/x86_64-linux-gnu"). Always POSIX-style.
	MountPath string

	// Manifest is the substrate manifest to project at MountPath.
	Manifest *manifest.Manifest
}

// Attach mounts a manifest at the given path.
//
// The mount path is normalized; the manifest's file entries
// (which are already substrate-relative — e.g. "/bin/pytest" inside
// a pytest manifest) are joined with the mount path to produce the
// absolute paths the Namespace serves.
//
// Conflicts: if an Attach would produce a path already claimed by
// another attachment, ErrPathConflict is returned and the Namespace
// state is unchanged.
func (n *Namespace) Attach(att Attachment) error {
	if att.Manifest == nil {
		return ErrNilManifest
	}
	mountPath := normalizeProjectionPath(att.MountPath)
	candidate := &attachment{mountPath: mountPath, manifest: att.Manifest}

	n.mu.Lock()
	defer n.mu.Unlock()
	pending := buildPendingIndex(candidate)
	if err := n.detectConflicts(pending); err != nil {
		return err
	}
	n.commitAttachment(candidate, pending)
	return nil
}

func buildPendingIndex(a *attachment) map[string]fileLocation {
	out := make(map[string]fileLocation, len(a.manifest.Files))
	for i := range a.manifest.Files {
		full := joinProjectionPath(a.mountPath, a.manifest.Files[i].Path)
		out[full] = fileLocation{owner: a, entry: a.manifest.Files[i]}
	}
	return out
}

func (n *Namespace) detectConflicts(pending map[string]fileLocation) error {
	for path := range pending {
		if existing, ok := n.fileIndex[path]; ok {
			return fmt.Errorf("%w: %q claimed by both %s and %s",
				ErrPathConflict, path, existing.owner.manifest.Package, pending[path].owner.manifest.Package)
		}
	}
	return nil
}

func (n *Namespace) commitAttachment(a *attachment, pending map[string]fileLocation) {
	n.attachments = append(n.attachments, a)
	for path, loc := range pending {
		n.fileIndex[path] = loc
		n.recordDirectoryAncestors(path)
	}
	// Mount path itself is always a directory.
	n.dirs[a.mountPath] = struct{}{}
	n.recordDirectoryAncestors(a.mountPath)
}

func (n *Namespace) recordDirectoryAncestors(p string) {
	for current := path.Dir(p); current != "."; current = path.Dir(current) {
		n.dirs[current] = struct{}{}
		if current == "/" {
			return
		}
	}
}

// Detach removes the attachment whose manifest matches manifestID.
// Returns ErrNotAttached if no attachment with that ID is currently
// mounted.
func (n *Namespace) Detach(manifestID manifest.ID) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	idx, found := n.findAttachment(manifestID)
	if !found {
		return fmt.Errorf("%w: %s", ErrNotAttached, manifestID)
	}
	n.removeAttachmentAt(idx)
	n.rebuildIndices()
	return nil
}

func (n *Namespace) findAttachment(manifestID manifest.ID) (int, bool) {
	for i, a := range n.attachments {
		if a.manifest.ID == manifestID {
			return i, true
		}
	}
	return 0, false
}

func (n *Namespace) removeAttachmentAt(idx int) {
	n.attachments = append(n.attachments[:idx], n.attachments[idx+1:]...)
}

// rebuildIndices recomputes fileIndex and dirs from the current
// attachment set. Called after Detach so removed paths cleanly
// disappear from the synthesized tree.
func (n *Namespace) rebuildIndices() {
	n.dirs = map[string]struct{}{"/": {}}
	n.fileIndex = make(map[string]fileLocation)
	for _, a := range n.attachments {
		n.dirs[a.mountPath] = struct{}{}
		n.recordDirectoryAncestors(a.mountPath)
		for i := range a.manifest.Files {
			full := joinProjectionPath(a.mountPath, a.manifest.Files[i].Path)
			n.fileIndex[full] = fileLocation{owner: a, entry: a.manifest.Files[i]}
			n.recordDirectoryAncestors(full)
		}
	}
}

// Attachments returns a snapshot of currently-attached manifests'
// PackageIDs. Useful for Phase 5.5 view discovery and diagnostics.
func (n *Namespace) Attachments() []manifest.PackageID {
	n.mu.RLock()
	defer n.mu.RUnlock()
	out := make([]manifest.PackageID, 0, len(n.attachments))
	for _, a := range n.attachments {
		out = append(out, a.manifest.Package)
	}
	return out
}

// ── executionNamespace surface ──────────────────────────────────────

// Stat returns the FileInfo for the named path, or syscall.ENOENT.
func (n *Namespace) Stat(_ context.Context, name string) (fs.FileInfo, error) {
	cleaned := normalizeProjectionPath(name)
	n.mu.RLock()
	defer n.mu.RUnlock()
	if loc, ok := n.fileIndex[cleaned]; ok {
		return projectionFileInfo{
			name:    path.Base(cleaned),
			size:    n.sizeForLocation(loc),
			mode:    fs.FileMode(loc.entry.Mode),
			modTime: loc.owner.manifest.CreatedAt,
			symlink: loc.entry.Symlink,
		}, nil
	}
	if _, ok := n.dirs[cleaned]; ok {
		return projectionFileInfo{
			name:    dirBaseName(cleaned),
			mode:    fs.ModeDir | defaultDirMode,
			modTime: now(),
		}, nil
	}
	return nil, syscall.ENOENT
}

func (n *Namespace) sizeForLocation(loc fileLocation) int64 {
	if loc.entry.Symlink {
		return int64(len(loc.entry.Path))
	}
	bytes, err := n.blobs.Bytes(loc.entry.BlobHash)
	if err != nil {
		return 0
	}
	return int64(len(bytes))
}

// ListDir enumerates direct children of dir.
func (n *Namespace) ListDir(_ context.Context, dir string) ([]fs.DirEntry, error) {
	cleaned := normalizeProjectionPath(dir)
	n.mu.RLock()
	defer n.mu.RUnlock()
	if _, ok := n.dirs[cleaned]; !ok {
		return nil, syscall.ENOENT
	}
	return n.collectDirEntries(cleaned), nil
}

func (n *Namespace) collectDirEntries(parent string) []fs.DirEntry {
	seen := make(map[string]projectionDirEntry)
	n.collectFileChildren(parent, seen)
	n.collectSubdirChildren(parent, seen)
	out := make([]fs.DirEntry, 0, len(seen))
	for _, e := range seen {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Name() < out[j].Name()
	})
	return out
}

func (n *Namespace) collectFileChildren(parent string, into map[string]projectionDirEntry) {
	for filePath, loc := range n.fileIndex {
		base, ok := directChildOf(filePath, parent)
		if !ok {
			continue
		}
		into[base] = projectionDirEntry{
			name: base,
			info: projectionFileInfo{
				name:    base,
				size:    n.sizeForLocation(loc),
				mode:    fs.FileMode(loc.entry.Mode),
				modTime: loc.owner.manifest.CreatedAt,
				symlink: loc.entry.Symlink,
			},
		}
	}
}

func (n *Namespace) collectSubdirChildren(parent string, into map[string]projectionDirEntry) {
	for dirPath := range n.dirs {
		if dirPath == parent {
			continue
		}
		base, ok := directChildOf(dirPath, parent)
		if !ok {
			continue
		}
		if _, alreadyFile := into[base]; alreadyFile {
			continue
		}
		into[base] = projectionDirEntry{
			name: base,
			info: projectionFileInfo{
				name:    base,
				mode:    fs.ModeDir | defaultDirMode,
				modTime: now(),
			},
		}
	}
}

// directChildOf reports whether p is a direct child of parent, and if
// so returns the basename of that child.
func directChildOf(p, parent string) (string, bool) {
	if !hasParentPrefix(p, parent) {
		return "", false
	}
	rel := strings.TrimPrefix(p, parent)
	rel = strings.TrimPrefix(rel, "/")
	if rel == "" {
		return "", false
	}
	if idx := strings.IndexByte(rel, '/'); idx >= 0 {
		return rel[:idx], true
	}
	return rel, true
}

func hasParentPrefix(p, parent string) bool {
	if parent == "/" {
		return strings.HasPrefix(p, "/") && p != "/"
	}
	return strings.HasPrefix(p, parent+"/")
}

// ReadFile returns the bytes for the named file, or syscall.ENOENT if
// the path does not name a file in the namespace.
func (n *Namespace) ReadFile(_ context.Context, name string) ([]byte, error) {
	cleaned := normalizeProjectionPath(name)
	n.mu.RLock()
	loc, ok := n.fileIndex[cleaned]
	n.mu.RUnlock()
	if !ok {
		return nil, syscall.ENOENT
	}
	if loc.entry.Symlink {
		return []byte(loc.entry.Path), nil
	}
	bytes, err := n.blobs.Bytes(loc.entry.BlobHash)
	if err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrBlobReadFailed, cleaned, err)
	}
	return bytes, nil
}

// MkdirAll, WriteFile, DeleteFile, Rename: all return syscall.EROFS
// because the substrate is read-only by construction. The execution
// broker's layered projection prevents these calls from reaching here
// in normal operation (writes hit the upper writable layers first),
// but the namespace honors its read-only contract regardless.

func (n *Namespace) MkdirAll(context.Context, string) error {
	return syscall.EROFS
}

func (n *Namespace) WriteFile(context.Context, string, []byte) error {
	return syscall.EROFS
}

func (n *Namespace) DeleteFile(context.Context, string) error {
	return syscall.EROFS
}

func (n *Namespace) Rename(context.Context, string, string) error {
	return syscall.EROFS
}

// ReadOnly always returns true.
func (n *Namespace) ReadOnly() bool { return true }

// ── path helpers ────────────────────────────────────────────────────

func normalizeProjectionPath(name string) string {
	cleaned := path.Clean("/" + strings.TrimSpace(name))
	if cleaned == "." {
		return "/"
	}
	return cleaned
}

func joinProjectionPath(mountPath, manifestPath string) string {
	mountClean := normalizeProjectionPath(mountPath)
	innerClean := normalizeProjectionPath(manifestPath)
	if mountClean == "/" {
		return innerClean
	}
	if innerClean == "/" {
		return mountClean
	}
	return normalizeProjectionPath(mountClean + innerClean)
}

func dirBaseName(p string) string {
	if p == "/" {
		return "/"
	}
	return path.Base(p)
}

// ── FileInfo / DirEntry ─────────────────────────────────────────────

type projectionFileInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
	symlink bool
}

func (i projectionFileInfo) Name() string { return i.name }
func (i projectionFileInfo) Size() int64  { return i.size }
func (i projectionFileInfo) Mode() fs.FileMode {
	if i.symlink {
		return i.mode | fs.ModeSymlink
	}
	return i.mode
}
func (i projectionFileInfo) ModTime() time.Time { return i.modTime }
func (i projectionFileInfo) IsDir() bool        { return i.mode.IsDir() }
func (i projectionFileInfo) Sys() any           { return nil }

type projectionDirEntry struct {
	name string
	info projectionFileInfo
}

func (e projectionDirEntry) Name() string               { return e.name }
func (e projectionDirEntry) IsDir() bool                { return e.info.IsDir() }
func (e projectionDirEntry) Type() fs.FileMode          { return e.info.Mode().Type() }
func (e projectionDirEntry) Info() (fs.FileInfo, error) { return e.info, nil }

// defaultDirMode is the unix permission bits stamped on synthesized
// directory entries (the substrate's mount-point and ancestor
// directories that don't come from a manifest's explicit file list).
// 0o755 matches the standard directory permission convention.
const defaultDirMode fs.FileMode = 0o755

// now is overridable for tests that need deterministic timestamps;
// production code uses time.Now via the package-level binding.
var now = func() time.Time { return time.Now().UTC() }

// Errors.
var (
	ErrNilBlobStore   = errors.New("projection: blob store is nil")
	ErrNilManifest    = errors.New("projection: manifest is nil")
	ErrPathConflict   = errors.New("projection: path conflict between attachments")
	ErrNotAttached    = errors.New("projection: manifest not attached")
	ErrBlobReadFailed = errors.New("projection: blob read failed")
)
