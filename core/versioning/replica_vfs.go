package versioning

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// ReplicaVFS is the per-merge, full in-RAM audit VFS with a parent
// pointer up the arrival-ordered chain and disk as the authoritative
// baseline. See docs/PARALLEL_GLOBAL_VFS.md §3.6 — single shared VFS
// per merge (inspector and tester both operate on it), copy-on-write
// reads that materialize on first access, writes never touch disk.
//
// Chain topology:
//
//	Disk (authoritative committed state; updated only by CommitResolver)
//	  ↑ read-through when chain misses
//	ReplicaVFS_N-1 (earlier in-flight merge)
//	  ↑
//	ReplicaVFS_N   (this one — audits merge N)
//
// The ReplicaVFS satisfies vfsBaseFS — so another ReplicaVFS can
// chain through it as its own parent — and implements its own
// Read/Write/Delete API for the audit-scoped FileAccess.
//
// Sealing: when the merge's audit accepts, Seal() freezes the
// overlay and returns the diff vs. the parent chain for MergePipe
// submission. After Seal, writes are rejected. Abandon() marks the
// VFS as chain-bypassable for downstream replicas (rejection).
type ReplicaVFS struct {
	// mu protects writes and deletes. Reads acquire a read lock
	// only during the local-store lookup; chain traversal walks
	// parent pointers without holding this mutex.
	mu sync.RWMutex

	// mergedVersion identifies which merge this ReplicaVFS audits.
	// Stable for the lifetime of the VFS.
	mergedVersion SemanticVersion

	// replicaID is the deterministic identifier of the merge's audit
	// (used as the WAL holder ID for overlay entries). Shared by
	// both inspector and tester replicas of this merge.
	replicaID string

	// parent is either another ReplicaVFS (previous merge in the
	// arrival chain) or nil (the chain bottoms out at disk directly).
	parent *ReplicaVFS

	// diskBase is the read-only fallback when a path isn't owned by
	// this ReplicaVFS or any ancestor. Disk IS the authoritative
	// baseline of committed work; reads through diskBase are a
	// first-class part of the audit experience.
	diskBase vfsBaseFS

	// writes holds the in-RAM content this ReplicaVFS OWNS — writes
	// authored by this layer's replicas (inspector + tester). Exactly
	// the set that becomes the audit-addendum diff on Seal.
	// Distinct from readCache (§3.6: "local content store for writes
	// + read-cache from ancestors"). Never includes cached reads
	// from ancestors; that would corrupt the diff.
	writes map[string][]byte

	// deletes marks paths this ReplicaVFS has explicitly removed.
	// A deleted path shadows the ancestor chain on read (returns
	// ErrFileNotFound without consulting parent or disk).
	deletes map[string]struct{}

	// owned is a cheap bloom-like set of paths present in writes or
	// deletes. Used by child ReplicaVFS to short-circuit chain walks
	// for paths this layer doesn't own. Updated under mu write lock.
	owned map[string]struct{}

	// readCache is the §3.6 JIT materialization cache — resolved
	// content from ancestors / disk memoised for subsequent reads
	// from this same replica. Kept separate from writes so Seal's
	// diff NEVER includes cached reads (that would spuriously mark
	// ancestor content as audit-authored).
	//
	// Invalidated on Abandon of self. Bounded to readCacheMaxPaths
	// by dropping the entire cache on overflow — a simple bound
	// that avoids per-entry LRU bookkeeping. Audits touch a
	// predictable, small set of paths; a 1-shot wipe on overflow is
	// fine in practice.
	readCache map[string][]byte

	// sealed is true after Seal() has been called. Further writes
	// return ErrReplicaVFSSealed.
	sealed atomic.Bool

	// abandoned is true after Abandon() has been called. Chain walks
	// from descendants skip this layer entirely.
	abandoned atomic.Bool

	// wal is the session's control WAL for durability. Writes and
	// deletes append entries before mutating in-memory state; Seal
	// and Abandon each write a terminal marker.
	wal *ControlWAL

	// sealedDiff is the cached Seal() output. Populated once Seal
	// fires; reused if Seal is called again (idempotent).
	sealedDiff *ReplicaVFSDiff

	// createdAt / sealedAt for observability.
	createdAt time.Time
	sealedAt  time.Time
}

// ReplicaVFSDiff is the Seal() output: the set of writes and deletes
// this ReplicaVFS owns, relative to its parent chain. Submitted to
// MergePipe as an audit-addendum changeset.
type ReplicaVFSDiff struct {
	// MergedVersion is the merge this diff applies to.
	MergedVersion SemanticVersion
	// Writes is the set of path → final content owned by this layer.
	Writes map[string][]byte
	// Deletes is the set of paths this layer removed.
	Deletes []string
}

// ReplicaVFSConfig configures a ReplicaVFS at construction.
type ReplicaVFSConfig struct {
	// MergedVersion identifies the merge this VFS audits.
	MergedVersion SemanticVersion
	// ReplicaID is the WAL holder ID for overlay entries.
	ReplicaID string
	// Parent is the previous ReplicaVFS in the arrival chain, or
	// nil if this is the chain root (reads fall directly to disk).
	Parent *ReplicaVFS
	// DiskBase is the authoritative committed-state fallback. Must
	// be set when Parent is nil; may also be set when Parent is
	// non-nil to short-circuit the chain walk on miss (defensive
	// redundancy — disk is always the bottom).
	DiskBase vfsBaseFS
	// WAL is the session's control WAL. Writes/deletes/seal/abandon
	// all append entries.
	WAL *ControlWAL
}

// Sentinel errors for ReplicaVFS state transitions.
var (
	// ErrReplicaVFSSealed is returned when a write/delete is
	// attempted after Seal() has been called.
	ErrReplicaVFSSealed = errors.New("replica vfs: sealed; no further writes accepted")
	// ErrReplicaVFSAbandoned is returned when a write/delete is
	// attempted after Abandon() has been called.
	ErrReplicaVFSAbandoned = errors.New("replica vfs: abandoned; no further writes accepted")
	// ErrReplicaVFSConfig flags a bad construction config.
	ErrReplicaVFSConfig = errors.New("replica vfs: invalid configuration")
)

// NewReplicaVFS constructs a ReplicaVFS from the given config.
// Validates that either a Parent or a DiskBase is supplied — a
// ReplicaVFS with no parent AND no disk base has nothing to fall
// back to on read miss, which is a configuration error.
func NewReplicaVFS(cfg ReplicaVFSConfig) (*ReplicaVFS, error) {
	if cfg.Parent == nil && cfg.DiskBase == nil {
		return nil, fmt.Errorf("%w: must supply Parent or DiskBase", ErrReplicaVFSConfig)
	}
	if cfg.ReplicaID == "" {
		return nil, fmt.Errorf("%w: ReplicaID required", ErrReplicaVFSConfig)
	}
	return &ReplicaVFS{
		mergedVersion: cfg.MergedVersion,
		replicaID:     cfg.ReplicaID,
		parent:        cfg.Parent,
		diskBase:      cfg.DiskBase,
		writes:        make(map[string][]byte),
		deletes:       make(map[string]struct{}),
		owned:         make(map[string]struct{}),
		wal:           cfg.WAL,
		createdAt:     time.Now().UTC(),
	}, nil
}

// MergedVersion returns the merge this ReplicaVFS audits.
func (r *ReplicaVFS) MergedVersion() SemanticVersion { return r.mergedVersion }

// ReplicaID returns the deterministic identifier of this VFS's merge.
func (r *ReplicaVFS) ReplicaID() string { return r.replicaID }

// Parent returns the parent ReplicaVFS, or nil if this is the root.
func (r *ReplicaVFS) Parent() *ReplicaVFS { return r.parent }

// IsSealed reports whether Seal has been called.
func (r *ReplicaVFS) IsSealed() bool { return r.sealed.Load() }

// IsAbandoned reports whether Abandon has been called.
func (r *ReplicaVFS) IsAbandoned() bool { return r.abandoned.Load() }

// Read resolves path through the chain. Resolution order per
// docs/PARALLEL_GLOBAL_VFS.md §3.6:
//
//  1. Local writes (owned content at this layer).
//  2. Local deletes (shadow — propagates to all descendants).
//  3. Parent chain (recursive; parent's delete shadow blocks disk).
//  4. Disk (authoritative committed state; only reached if no
//     ancestor shadowed the path).
//
// Delete shadowing is critical: a delete at layer N hides the path
// from N and every descendant below N. Without that, a child layer
// would fall through the parent's "not found" all the way to disk,
// surfacing the pre-delete content and defeating the shadow. The
// chain walk therefore distinguishes "not in this layer" from
// "deleted at this layer" via an internal three-state result.
//
// Locking: acquires mu as a reader per-layer. Chain walks never
// hold mu across layers — each layer acquires its own lock.
func (r *ReplicaVFS) Read(path string) ([]byte, error) {
	if r == nil {
		return nil, ErrFileNotFound
	}
	// Fast path: JIT read-cache hit. Skips chain walk entirely.
	// Cached content is a defensive copy so callers can't mutate the
	// cache through the returned slice.
	if cached, ok := r.lookupReadCache(path); ok {
		return cached, nil
	}
	content, state := r.readThroughChain(path)
	switch state {
	case readResolveHit:
		// Cache the resolved content for subsequent hits. §3.6.
		r.populateReadCache(path, content)
		return content, nil
	case readResolveShadowed:
		// Deleted upstream — don't fall through to disk. Don't cache
		// a "not found" sentinel either; subsequent reads re-walk
		// and re-discover the shadow via the same path. (Writing a
		// nil to readCache would conflate "cached not-found" with
		// "cached empty file.")
		return nil, ErrFileNotFound
	default:
		if r.diskBase != nil {
			raw, err := r.diskBase.ReadFile(path)
			if err == nil {
				r.populateReadCache(path, raw)
			}
			return raw, err
		}
		return nil, ErrFileNotFound
	}
}

// readCacheMaxPaths caps the JIT read cache. When the cache reaches
// this many entries, the next insertion wipes it entirely (simple
// bounded-cache policy). Audits typically touch O(tens) of paths;
// the cap is chosen generous enough that normal workloads never
// overflow while still preventing pathological memory growth if a
// misbehaving audit reads many paths.
const readCacheMaxPaths = 512

func (r *ReplicaVFS) lookupReadCache(path string) ([]byte, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.readCache == nil {
		return nil, false
	}
	content, ok := r.readCache[path]
	if !ok {
		return nil, false
	}
	out := make([]byte, len(content))
	copy(out, content)
	return out, true
}

// populateReadCache stores a defensive copy of content at path.
// Skipped when the layer is sealed or abandoned — sealed readers
// should see stable content that won't be polluted by post-seal
// cache writes, and abandoned layers are chain-bypassed anyway.
func (r *ReplicaVFS) populateReadCache(path string, content []byte) {
	if r.sealed.Load() || r.abandoned.Load() {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.readCache == nil {
		r.readCache = make(map[string][]byte)
	}
	if len(r.readCache) >= readCacheMaxPaths {
		// Simple bounded cache: wipe and restart when the cap hits.
		// More sophisticated policies (LRU, LFU) would add per-entry
		// metadata; for audit workloads with small touched-path sets
		// this is both sufficient and straightforward to reason
		// about.
		r.readCache = make(map[string][]byte)
	}
	cached := make([]byte, len(content))
	copy(cached, content)
	r.readCache[path] = cached
}

// invalidateReadCache clears the JIT cache. Called on Abandon so
// descendant chain walks that bypass this layer don't pick up stale
// cached content.
func (r *ReplicaVFS) invalidateReadCache() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.readCache = nil
}

// readResolveState classifies the outcome of a chain traversal.
// Distinguishes "the path was deleted upstream" (don't fall through
// to disk) from "the path isn't in the chain" (do fall through).
type readResolveState uint8

const (
	// readResolveMiss means no layer in the chain has an opinion;
	// caller should fall through to disk.
	readResolveMiss readResolveState = iota
	// readResolveHit means an ancestor (or this layer) owns the
	// path with content.
	readResolveHit
	// readResolveShadowed means an ancestor has the path in its
	// deletes set — the path is logically not-present for any
	// descendant even if disk has a version.
	readResolveShadowed
)

// readThroughChain walks from the current layer up through its
// parents, returning the first hit or shadow it encounters.
// Abandoned layers are skipped entirely.
func (r *ReplicaVFS) readThroughChain(path string) ([]byte, readResolveState) {
	if r == nil {
		return nil, readResolveMiss
	}
	if r.abandoned.Load() {
		if r.parent != nil {
			return r.parent.readThroughChain(path)
		}
		return nil, readResolveMiss
	}
	r.mu.RLock()
	if _, deleted := r.deletes[path]; deleted {
		r.mu.RUnlock()
		return nil, readResolveShadowed
	}
	if content, ok := r.writes[path]; ok {
		out := make([]byte, len(content))
		copy(out, content)
		r.mu.RUnlock()
		return out, readResolveHit
	}
	r.mu.RUnlock()
	if r.parent != nil {
		return r.parent.readThroughChain(path)
	}
	return nil, readResolveMiss
}

// Exists reports whether path is reachable through the chain. Same
// shadow semantics as Read — a delete at any layer hides the path
// from descendants.
func (r *ReplicaVFS) Exists(path string) (bool, error) {
	if r == nil {
		return false, nil
	}
	_, state := r.readThroughChain(path)
	switch state {
	case readResolveHit:
		return true, nil
	case readResolveShadowed:
		return false, nil
	default:
		if r.diskBase != nil {
			return r.diskBase.Exists(path)
		}
		return false, nil
	}
}

// Stat returns file metadata via chain resolution. Falls through to
// disk for committed paths. Returns ErrFileNotFound for paths that
// exist only in overlay (stat against the overlay content would
// require synthesizing fs.FileInfo; out of scope for audit use
// cases today).
func (r *ReplicaVFS) Stat(path string) (fs.FileInfo, error) {
	if r == nil {
		return nil, ErrFileNotFound
	}
	if r.abandoned.Load() {
		if r.parent != nil {
			return r.parent.Stat(path)
		}
		if r.diskBase != nil {
			return r.diskBase.Stat(path)
		}
		return nil, ErrFileNotFound
	}
	r.mu.RLock()
	if _, deleted := r.deletes[path]; deleted {
		r.mu.RUnlock()
		return nil, ErrFileNotFound
	}
	_, hasOverlay := r.writes[path]
	r.mu.RUnlock()
	if r.parent != nil {
		info, err := r.parent.Stat(path)
		if err == nil {
			return info, nil
		}
	}
	if r.diskBase != nil {
		info, err := r.diskBase.Stat(path)
		if err == nil {
			return info, nil
		}
	}
	if hasOverlay {
		// Path is in overlay but neither parent nor disk knows it —
		// synthesize a minimal info with current time and overlay
		// content length.
		return replicaOverlayFileInfo{path: path, size: int64(len(r.writes[path])), modTime: time.Now().UTC()}, nil
	}
	return nil, ErrFileNotFound
}

// ListDir returns the union of paths under dir across the chain plus
// disk, minus those marked deleted at any layer between the path's
// origin and the caller. Directory listings walk the chain top-down
// so newer overlays shadow older ancestors' views.
func (r *ReplicaVFS) ListDir(dir string) ([]string, error) {
	if r == nil {
		return nil, ErrFileNotFound
	}
	visible := make(map[string]struct{})
	if err := r.collectListDir(dir, visible); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(visible))
	for p := range visible {
		out = append(out, p)
	}
	sort.Strings(out)
	return out, nil
}

// collectListDir walks the chain top-down, accumulating visible
// paths. A path is visible at the topmost layer that owns it
// (overlay) unless that layer has it in deletes.
func (r *ReplicaVFS) collectListDir(dir string, visible map[string]struct{}) error {
	if r == nil {
		return nil
	}
	if r.abandoned.Load() {
		if r.parent != nil {
			return r.parent.collectListDir(dir, visible)
		}
		if r.diskBase != nil {
			entries, err := r.diskBase.ListDir(dir)
			if err == nil {
				for _, e := range entries {
					visible[e] = struct{}{}
				}
			}
			return nil
		}
		return nil
	}
	r.mu.RLock()
	for path := range r.writes {
		if pathInDir(path, dir) {
			if _, del := r.deletes[path]; !del {
				visible[path] = struct{}{}
			}
		}
	}
	localDeletes := make(map[string]struct{}, len(r.deletes))
	for p := range r.deletes {
		localDeletes[p] = struct{}{}
	}
	r.mu.RUnlock()
	// Walk ancestors + disk; drop paths this layer has explicitly
	// deleted from whatever we've collected.
	var inherited map[string]struct{}
	if r.parent != nil {
		inherited = make(map[string]struct{})
		_ = r.parent.collectListDir(dir, inherited)
	}
	if r.diskBase != nil {
		if inherited == nil {
			inherited = make(map[string]struct{})
		}
		if entries, err := r.diskBase.ListDir(dir); err == nil {
			for _, e := range entries {
				inherited[e] = struct{}{}
			}
		}
	}
	for p := range inherited {
		if _, del := localDeletes[p]; del {
			continue
		}
		if _, already := visible[p]; already {
			continue
		}
		visible[p] = struct{}{}
	}
	return nil
}

func pathInDir(path, dir string) bool {
	if dir == "" || dir == "." {
		return true
	}
	if len(path) <= len(dir) {
		return false
	}
	if path[:len(dir)] != dir {
		return false
	}
	// path starts with dir; ensure it's either dir/ or dir itself
	return path[len(dir)] == '/'
}

// Write applies a new content write to this ReplicaVFS. Serialized
// on the VFS mutex (both inspector and tester share this lock).
// Write-ahead to the ControlWAL before in-memory state mutates.
//
// Rejects writes on sealed or abandoned replicas — the audit is
// terminal; no further content mutation is meaningful.
func (r *ReplicaVFS) Write(writerID, path string, content []byte) error {
	if r == nil {
		return ErrReplicaVFSConfig
	}
	if r.sealed.Load() {
		return ErrReplicaVFSSealed
	}
	if r.abandoned.Load() {
		return ErrReplicaVFSAbandoned
	}
	if path == "" {
		return fmt.Errorf("replica vfs: Write: empty path")
	}
	// Write-ahead persistence. A persistence failure aborts the
	// write; callers (tool handlers) propagate the error to the
	// replica's LLM loop.
	if r.wal != nil {
		if _, err := r.wal.Append(ControlKindReplicaOverlayWrite, ControlEntryPayload{
			ReplicaMergeVersion: r.mergedVersion,
			ReplicaID:           r.replicaID,
			OverlayPath:         path,
			OverlayContent:      append([]byte(nil), content...),
			OverlayWriter:       writerID,
		}); err != nil {
			return fmt.Errorf("replica vfs: write wal: %w", err)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	buf := make([]byte, len(content))
	copy(buf, content)
	r.writes[path] = buf
	delete(r.deletes, path) // write overrides a prior delete
	r.owned[path] = struct{}{}
	// Invalidate any stale cached read for this path — the cache
	// holds ancestor/disk content, but this layer now owns the
	// path, so subsequent Reads must resolve through writes.
	delete(r.readCache, path)
	return nil
}

// Delete marks a path removed at this ReplicaVFS layer. Shadow over
// ancestor + disk. Serialized on the mutex; write-ahead to WAL.
func (r *ReplicaVFS) Delete(writerID, path string) error {
	if r == nil {
		return ErrReplicaVFSConfig
	}
	if r.sealed.Load() {
		return ErrReplicaVFSSealed
	}
	if r.abandoned.Load() {
		return ErrReplicaVFSAbandoned
	}
	if path == "" {
		return fmt.Errorf("replica vfs: Delete: empty path")
	}
	if r.wal != nil {
		if _, err := r.wal.Append(ControlKindReplicaOverlayDelete, ControlEntryPayload{
			ReplicaMergeVersion: r.mergedVersion,
			ReplicaID:           r.replicaID,
			OverlayPath:         path,
			OverlayWriter:       writerID,
		}); err != nil {
			return fmt.Errorf("replica vfs: delete wal: %w", err)
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.deletes[path] = struct{}{}
	delete(r.writes, path) // delete overrides a prior write
	r.owned[path] = struct{}{}
	// Invalidate any stale cached read — the path is now shadowed
	// at this layer, so subsequent Reads return ErrFileNotFound.
	delete(r.readCache, path)
	return nil
}

// Seal freezes this ReplicaVFS and returns the diff vs. the parent
// chain. Called by the audit coordinator on accept — the diff is
// submitted to MergePipe as an audit-addendum pipeline-equivalent
// changeset.
//
// After Seal, writes/deletes return ErrReplicaVFSSealed. Reads
// continue to work (downstream replicas may still chain through
// this one for progressive context). Idempotent: a second Seal
// returns the cached diff without re-WAL-writing.
func (r *ReplicaVFS) Seal() (*ReplicaVFSDiff, error) {
	if r == nil {
		return nil, ErrReplicaVFSConfig
	}
	if r.abandoned.Load() {
		return nil, ErrReplicaVFSAbandoned
	}
	if r.sealed.Load() {
		r.mu.RLock()
		diff := r.sealedDiff
		r.mu.RUnlock()
		if diff != nil {
			return diff, nil
		}
		// Racing seal; compute below.
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	diff := &ReplicaVFSDiff{
		MergedVersion: r.mergedVersion,
		Writes:        make(map[string][]byte, len(r.writes)),
		Deletes:       make([]string, 0, len(r.deletes)),
	}
	for path, content := range r.writes {
		buf := make([]byte, len(content))
		copy(buf, content)
		diff.Writes[path] = buf
	}
	for path := range r.deletes {
		diff.Deletes = append(diff.Deletes, path)
	}
	sort.Strings(diff.Deletes)
	if r.wal != nil {
		if _, err := r.wal.Append(ControlKindReplicaOverlaySealed, ControlEntryPayload{
			ReplicaMergeVersion: r.mergedVersion,
			ReplicaID:           r.replicaID,
		}); err != nil {
			return nil, fmt.Errorf("replica vfs: seal wal: %w", err)
		}
	}
	r.sealed.Store(true)
	r.sealedAt = time.Now().UTC()
	r.sealedDiff = diff
	return diff, nil
}

// Abandon marks this ReplicaVFS as chain-bypassable. Downstream
// reads skip over this layer. Writes/deletes on this VFS return
// ErrReplicaVFSAbandoned. Idempotent.
func (r *ReplicaVFS) Abandon() error {
	if r == nil {
		return ErrReplicaVFSConfig
	}
	if r.abandoned.Load() {
		return nil
	}
	if r.wal != nil {
		if _, err := r.wal.Append(ControlKindReplicaOverlayAbandoned, ControlEntryPayload{
			ReplicaMergeVersion: r.mergedVersion,
			ReplicaID:           r.replicaID,
		}); err != nil {
			return fmt.Errorf("replica vfs: abandon wal: %w", err)
		}
	}
	r.abandoned.Store(true)
	// Drop the JIT read cache — downstream chain walks bypass this
	// layer now, so cached ancestor reads held here could mislead
	// future direct callers.
	r.invalidateReadCache()
	return nil
}

// ApplyControlEntry rehydrates a single durable transition on replay.
// Used by SessionVFS.Open to reconstruct the in-memory overlay from
// the ControlWAL. Ignores entries not addressing this replica.
func (r *ReplicaVFS) ApplyControlEntry(entry *ControlEntry) {
	if r == nil || entry == nil {
		return
	}
	if entry.Payload.ReplicaID != r.replicaID {
		return
	}
	switch entry.Kind {
	case ControlKindReplicaOverlayWrite:
		r.mu.Lock()
		buf := make([]byte, len(entry.Payload.OverlayContent))
		copy(buf, entry.Payload.OverlayContent)
		r.writes[entry.Payload.OverlayPath] = buf
		delete(r.deletes, entry.Payload.OverlayPath)
		r.owned[entry.Payload.OverlayPath] = struct{}{}
		r.mu.Unlock()
	case ControlKindReplicaOverlayDelete:
		r.mu.Lock()
		r.deletes[entry.Payload.OverlayPath] = struct{}{}
		delete(r.writes, entry.Payload.OverlayPath)
		r.owned[entry.Payload.OverlayPath] = struct{}{}
		r.mu.Unlock()
	case ControlKindReplicaOverlaySealed:
		r.sealed.Store(true)
		r.sealedAt = entry.Timestamp
	case ControlKindReplicaOverlayAbandoned:
		r.abandoned.Store(true)
	}
}

// replicaOverlayFileInfo is the fs.FileInfo variant returned for
// overlay-only paths (paths that exist in the ReplicaVFS but have
// no ancestor/disk counterpart).
type replicaOverlayFileInfo struct {
	path    string
	size    int64
	modTime time.Time
}

func (f replicaOverlayFileInfo) Name() string       { return f.path }
func (f replicaOverlayFileInfo) Size() int64        { return f.size }
func (f replicaOverlayFileInfo) Mode() fs.FileMode  { return 0o644 }
func (f replicaOverlayFileInfo) ModTime() time.Time { return f.modTime }
func (f replicaOverlayFileInfo) IsDir() bool        { return false }
func (f replicaOverlayFileInfo) Sys() any           { return nil }

// Compile-time assertion that ReplicaVFS satisfies vfsBaseFS — so
// another ReplicaVFS can chain through it as its parent without any
// adapter layer.
var _ vfsBaseFS = (*replicaVFSAsBaseFS)(nil)

// replicaVFSAsBaseFS adapts *ReplicaVFS to the vfsBaseFS interface.
// Used when wiring a ReplicaVFS as the base for a PipelineVFS (audit
// tool loops, or remediation pipelines dispatched against a specific
// ReplicaVFS).
type replicaVFSAsBaseFS struct {
	vfs *ReplicaVFS
}

// AsBaseFS returns a vfsBaseFS view of this ReplicaVFS, usable as
// the base for a downstream PipelineVFS or for wiring into a
// FileAccess scope.
func (r *ReplicaVFS) AsBaseFS() vfsBaseFS {
	if r == nil {
		return nil
	}
	return &replicaVFSAsBaseFS{vfs: r}
}

func (b *replicaVFSAsBaseFS) ReadFile(path string) ([]byte, error) {
	return b.vfs.Read(path)
}
func (b *replicaVFSAsBaseFS) Exists(path string) (bool, error) {
	return b.vfs.Exists(path)
}
func (b *replicaVFSAsBaseFS) ListDir(dir string) ([]string, error) {
	return b.vfs.ListDir(dir)
}
func (b *replicaVFSAsBaseFS) Stat(path string) (fs.FileInfo, error) {
	return b.vfs.Stat(path)
}

// OwnedPaths returns the paths this ReplicaVFS directly writes or
// deletes (not including inherited content). Used by observability
// and by MergePipe submission to construct the audit-addendum
// change list.
func (r *ReplicaVFS) OwnedPaths() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.owned))
	for p := range r.owned {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// ReadContext is the context-aware read entry point used by skills
// that respect cancellation. Identical semantics to Read; wraps for
// future cancellation support when large-file reads need to abort.
func (r *ReplicaVFS) ReadContext(ctx context.Context, path string) ([]byte, error) {
	if ctx != nil {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
	}
	return r.Read(path)
}
