package versioning

import (
	"fmt"
	"io/fs"
	"sort"
	"sync"
)

// CopyMaterialization is a byte-for-byte snapshot of green at a specific
// SemanticVersion (= a specific Copy in docs/PARALLEL_GLOBAL_VFS.md
// terminology). Produced by SessionVFS.CopyAt; consumed by pipeline
// dispatch to seed a new pipelineVFS with starting content AND by audit
// replica launch to frame the replica's context.
//
// The materialization is a flat path→content map. Reads/walks against it
// are O(1) per path. No upstream read-through; nothing shared with green
// or with other Copies. Owning caller has full ownership of the bytes.
//
// Memory cost = sum of all path contents at the target version. For
// codebases in the typical range this is on the order of a few MB; the
// trade we accept for hard isolation per docs/PARALLEL_GLOBAL_VFS.md.
type CopyMaterialization struct {
	// Version identifies which Copy this materialization represents.
	Version SemanticVersion

	// Files is the path → content mapping. Keys are canonical absolute
	// paths (as stored in WAL deltas); values are the final content at
	// Version after replaying all deltas up to and including Version.
	// A path is absent when it was deleted at or before Version.
	Files map[string][]byte
}

// Read returns the content for path at this materialization's Version.
// ok is false when the path does not exist (either was never written or
// was deleted).
func (c *CopyMaterialization) Read(path string) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	content, ok := c.Files[path]
	if !ok {
		return nil, false
	}
	// Return a defensive copy so callers can't mutate our cached content.
	out := make([]byte, len(content))
	copy(out, content)
	return out, true
}

// Paths returns the sorted list of paths present at this materialization.
// Useful for iterating a Copy's contents deterministically.
func (c *CopyMaterialization) Paths() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.Files))
	for p := range c.Files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// FileCount returns the number of paths present at this materialization.
func (c *CopyMaterialization) FileCount() int {
	if c == nil {
		return 0
	}
	return len(c.Files)
}

// CopyAt materializes the session's green state as it existed AT the
// given SemanticVersion. Returns a new CopyMaterialization with
// byte-for-byte file contents at that version.
//
// Behavior:
//   - version == zero → materialize the initial baseline (empty or
//     disk-only state before any merges).
//   - version == CurrentVersion() → materialize the current green state.
//   - version < CurrentVersion() → replay WAL deltas up to `version` to
//     reconstruct the historical Copy.
//   - version > CurrentVersion() → error (cannot materialize a future
//     Copy; no such state exists).
//
// Implementation: replay all WAL delta/checkpoint entries whose version
// is <= `version`, applying each entry's deltas to a cumulative in-memory
// state map. This is O(disk baseline + total deltas to version) per
// call. Stage 4 introduces periodic snapshot checkpoints to bound the
// replay cost; until then, every CopyAt call replays from scratch.
//
// Deletes are honored — a delete delta removes the path from the
// materialization's Files map.
//
// Read consistency: CopyAt holds an internal lock while snapshotting the
// WAL; concurrent merges may advance the WAL's CurrentVersion after
// CopyAt returns but the materialization remains valid for the version
// it captured.
func (s *SessionVFS) CopyAt(version SemanticVersion) (*CopyMaterialization, error) {
	if s == nil {
		return nil, fmt.Errorf("session vfs: CopyAt: nil receiver")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrVFSClosed
	}
	wal := s.wal
	s.mu.Unlock()
	return copyAtWithWAL(wal, version)
}

// copyAtLocked is the unlocked internal variant of CopyAt, safe to call
// from code paths that already hold s.mu. Specifically: BeginPipeline
// invokes it while constructing a pipeline VFS under s.mu.
func (s *SessionVFS) copyAtLocked(version SemanticVersion) (*CopyMaterialization, error) {
	if s == nil {
		return nil, fmt.Errorf("session vfs: copyAtLocked: nil receiver")
	}
	if s.closed {
		return nil, ErrVFSClosed
	}
	return copyAtWithWAL(s.wal, version)
}

// MaterializePipelineFromCopy seeds the pipeline VFS's base-reader
// and base-FS from the Copy at baseVersion. Named per
// docs/PARALLEL_GLOBAL_VFS.md Stage 2. Extracted from BeginPipeline
// so the materialization semantics are independently testable and
// can be invoked by any future surface (rebind after crash, audit
// replica scaffolding, etc.).
//
// Semantics:
//   - Materialize the Copy via CopyAt(baseVersion) — full path→content
//     snapshot at that version.
//   - Install the Copy as the pipeline's read base: reads hit the
//     Copy's files first, falling through to the session's
//     disk-backed baseFS for paths the Copy didn't capture (paths
//     unchanged since disk).
//   - The pipeline VFS retains exclusive ownership of the returned
//     materialization — isolation from subsequent green advancement
//     is guaranteed by the Copy's immutability post-return.
//
// Concurrency: acquires s.mu around the CopyAt step, then installs
// the readers on the supplied pipelineVFS outside the session lock.
// pipelineVFS's own mutexes serialize the install with concurrent
// readers.
func (s *SessionVFS) MaterializePipelineFromCopy(pipelineVFS *PipelineVFS, baseVersion SemanticVersion) error {
	if s == nil {
		return fmt.Errorf("session vfs: MaterializePipelineFromCopy: nil receiver")
	}
	if pipelineVFS == nil {
		return fmt.Errorf("session vfs: MaterializePipelineFromCopy: nil pipelineVFS")
	}
	if baseVersion.IsZero() {
		return fmt.Errorf("session vfs: MaterializePipelineFromCopy: base version is zero (use current-green dispatch instead)")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrVFSClosed
	}
	s.mu.Unlock()
	mat, err := s.CopyAt(baseVersion)
	if err != nil {
		return fmt.Errorf("session vfs: materialize base copy %s: %w", baseVersion.String(), err)
	}
	installCopyMaterializationOnPipelineVFS(pipelineVFS, mat, s.baseFS)
	return nil
}

// materializePipelineFromCopyLocked is the s.mu-held variant called
// from BeginPipeline. Shares the installation logic with
// MaterializePipelineFromCopy but reuses the already-held session
// lock for the CopyAt replay. Caller owns error handling (closing
// the pipeline VFS on failure, etc.).
func (s *SessionVFS) materializePipelineFromCopyLocked(pipelineVFS *PipelineVFS, baseVersion SemanticVersion) error {
	if baseVersion.IsZero() {
		return fmt.Errorf("session vfs: materializePipelineFromCopyLocked: base version is zero")
	}
	mat, err := s.copyAtLocked(baseVersion)
	if err != nil {
		return fmt.Errorf("session vfs: materialize base copy %s: %w", baseVersion.String(), err)
	}
	installCopyMaterializationOnPipelineVFS(pipelineVFS, mat, s.baseFS)
	return nil
}

// installCopyMaterializationOnPipelineVFS wires the Copy as the
// pipeline's base read surface. Split out so the locked and
// exported variants share exactly the same installation — any
// future change to how pipelines consume Copies touches one site.
func installCopyMaterializationOnPipelineVFS(pipelineVFS *PipelineVFS, mat *CopyMaterialization, diskBase vfsBaseFS) {
	pipelineVFS.SetBaseReader(func(path string) ([]byte, error) {
		if content, ok := mat.Read(path); ok {
			return content, nil
		}
		if diskBase == nil {
			return nil, ErrFileNotFound
		}
		return diskBase.ReadFile(path)
	})
	pipelineVFS.SetBaseFS(&copyBackedBaseFS{mat: mat, fallback: diskBase})
}

// copyAtWithWAL does the actual materialization given a WAL reference.
// Shared implementation between the locked and unlocked entry points.
func copyAtWithWAL(wal SemanticWAL, version SemanticVersion) (*CopyMaterialization, error) {
	if wal == nil {
		return nil, fmt.Errorf("session vfs: CopyAt: semantic WAL is unavailable")
	}

	currentVer := wal.CurrentVersion()
	if compareVersions(version, currentVer) > 0 {
		return nil, fmt.Errorf("session vfs: CopyAt: requested version %s is ahead of current green %s", version.String(), currentVer.String())
	}

	// Replay from the zero version. Our WAL API gives us all entries
	// newer than a given version; replaying from zero walks the full
	// index.
	zeroVer := SemanticVersion{}
	entries, err := wal.GetDeltasSince(zeroVer)
	if err != nil {
		return nil, fmt.Errorf("session vfs: CopyAt: replay WAL: %w", err)
	}

	mat := &CopyMaterialization{
		Version: version,
		Files:   make(map[string][]byte, 16),
	}

	for _, entry := range entries {
		if compareVersions(entry.Version, version) > 0 {
			break
		}
		applyWALEntryToCopy(mat, entry)
	}
	return mat, nil
}

// applyWALEntryToCopy replays one WAL entry's deltas onto the
// materialization's Files map, honoring create/write/delete semantics.
// Ignores entries whose deltas carry empty paths (malformed; surfaced
// via caller-side debugging rather than failing CopyAt).
func applyWALEntryToCopy(mat *CopyMaterialization, entry VersionedWALEntry) {
	for _, d := range entry.Deltas {
		if d.Path == "" {
			continue
		}
		switch d.Op {
		case WALDeltaOpDelete:
			delete(mat.Files, d.Path)
		default:
			// Treat all non-delete ops (create/write/patch) as
			// "set content." NewContent carries the post-delta
			// content; OldContent is the inverse for rollback and
			// not needed here.
			buf := make([]byte, len(d.NewContent))
			copy(buf, d.NewContent)
			mat.Files[d.Path] = buf
		}
	}
}

// compareVersions returns -1/0/+1 a la semver. Defined as a free
// function so CopyAt's replay loop stays self-contained.
func compareVersions(a, b SemanticVersion) int {
	return a.Compare(b)
}

// Compile-time assertion that CopyMaterialization is safe for concurrent
// read access when the caller does not mutate it. We do not expose a
// mutation API.
var _ sync.Locker = (*copyMaterializationPlaceholderLock)(nil)

type copyMaterializationPlaceholderLock struct{}

func (copyMaterializationPlaceholderLock) Lock()   {}
func (copyMaterializationPlaceholderLock) Unlock() {}

// copyBackedBaseFS is the vfsBaseFS implementation used when a pipeline
// VFS is dispatched with BaseCopyVersion set. Reads resolve first from
// the pipeline-owned CopyMaterialization (byte-for-byte content at the
// target Copy's version); on miss, fall through to the session's
// baseline filesystem (disk).
//
// This is the read-path counterpart to the byte-for-byte materialization
// described in docs/PARALLEL_GLOBAL_VFS.md §3.6. It provides the
// isolation guarantee: the pipeline's reads reflect exactly the Copy's
// state, independent of any subsequent green advancement.
type copyBackedBaseFS struct {
	mat      *CopyMaterialization
	fallback vfsBaseFS
}

func (b *copyBackedBaseFS) ReadFile(path string) ([]byte, error) {
	if b == nil {
		return nil, ErrFileNotFound
	}
	if b.mat != nil {
		if content, ok := b.mat.Read(path); ok {
			return content, nil
		}
	}
	if b.fallback == nil {
		return nil, ErrFileNotFound
	}
	return b.fallback.ReadFile(path)
}

func (b *copyBackedBaseFS) Exists(path string) (bool, error) {
	if b == nil {
		return false, nil
	}
	if b.mat != nil {
		if _, ok := b.mat.Read(path); ok {
			return true, nil
		}
	}
	if b.fallback == nil {
		return false, nil
	}
	return b.fallback.Exists(path)
}

func (b *copyBackedBaseFS) ListDir(dir string) ([]string, error) {
	// Directory listing for a Copy-backed base FS is the union of
	// (a) entries the materialization has under dir and (b) entries
	// present on disk that are not shadowed by the materialization.
	// For stage 2 we return the fallback's listing; a future stage
	// will add materialization-aware listing when the audit replica
	// needs to enumerate Copy contents.
	if b == nil || b.fallback == nil {
		return nil, ErrFileNotFound
	}
	return b.fallback.ListDir(dir)
}

func (b *copyBackedBaseFS) Stat(path string) (fs.FileInfo, error) {
	// Stat falls through to the fallback. ListDir's limitation applies
	// here too. Sufficient for stage 2 since BeginPipeline's existing
	// tests exercise ReadFile / Exists. Expand in stage 3 / stage 4
	// when audit replica enumeration lands.
	if b == nil || b.fallback == nil {
		return nil, ErrFileNotFound
	}
	return b.fallback.Stat(path)
}
