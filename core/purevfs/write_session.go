package purevfs

import (
	"errors"
	"io/fs"
	"sync"
	"sync/atomic"
)

var (
	// ErrStaleGeneration is returned by WriteSession methods when the
	// session's branch generation no longer matches the workspace's
	// current branch generation. This means another writer has Sealed
	// the branch since this session was opened — the writer is fenced
	// and must abandon its outstanding work.
	ErrStaleGeneration = errors.New("purevfs: branch generation has advanced; write session is fenced")

	// ErrSessionSealed is returned when a write is attempted through a
	// WriteSession that has already been Sealed. Once Seal returns,
	// the session is one-shot — the predecessor has handed off
	// authoritatively and cannot resume writing.
	ErrSessionSealed = errors.New("purevfs: write session has been sealed")
)

// branchGenerations tracks per-branch monotonic generation counters.
// Each Seal on a branch bumps the counter; any WriteSession opened
// before the bump becomes stale.
//
// The map is allocated lazily per Workspace and protected by its own
// lock to keep the Workspace's main mu off the generation hot path.
// Per-branch counters are atomic.Uint64 so Generation() is lock-free
// and so concurrent fence checks across branches don't contend.
type branchGenerations struct {
	mu       sync.RWMutex
	counters map[string]*atomic.Uint64
}

func newBranchGenerations() *branchGenerations {
	return &branchGenerations{counters: make(map[string]*atomic.Uint64)}
}

// counterFor returns the atomic counter for branch, creating a fresh
// zero-valued counter on first reference. Callers can then Load to
// read the current generation or Add to bump it.
func (b *branchGenerations) counterFor(branch string) *atomic.Uint64 {
	b.mu.RLock()
	c, ok := b.counters[branch]
	b.mu.RUnlock()
	if ok {
		return c
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if c, ok := b.counters[branch]; ok {
		return c
	}
	c = &atomic.Uint64{}
	b.counters[branch] = c
	return c
}

// SealedHandoff is the receipt returned by WriteSession.Seal. It
// pins the branch snapshot the predecessor finished writing to, plus
// the post-seal generation the successor must match when opening its
// own WriteSession on the same branch. The predecessor hands this
// value to the successor (or the orchestrator) as proof of seal.
type SealedHandoff struct {
	Branch     string
	SnapshotID SnapshotID
	Generation uint64 // first generation that may write next; sessions opened earlier are stale
}

// WriteSession is a fenced view of a Workspace branch. It captures the
// branch's generation at open time and refuses to write once the
// branch has been Sealed by another holder (ErrStaleGeneration) or by
// itself (ErrSessionSealed).
//
// The session does not lock the workspace — concurrent reads and
// other branches proceed normally. The fence check on every write is
// a single atomic.Uint64 load: cheap.
//
// WriteSession is not safe for concurrent use by multiple goroutines.
// Hand it to one writer at a time; if multiple writers need to
// participate they should each open their own session against the
// same generation and the orchestrator must reconcile them.
type WriteSession struct {
	ws     *Workspace
	branch string
	gen    uint64 // generation captured at BeginWriteSession
	sealed atomic.Bool
}

// BeginWriteSession opens a fenced write session against branch. The
// returned session can write while the branch's generation still
// matches what was captured here. Successor sessions opened after a
// Seal observe a higher generation and fence the predecessor.
func (w *Workspace) BeginWriteSession(branch string) *WriteSession {
	if w.branchGens == nil {
		// Lazy init; mu is acquired only on the first session ever
		// opened on a workspace.
		w.mu.Lock()
		if w.branchGens == nil {
			w.branchGens = newBranchGenerations()
		}
		w.mu.Unlock()
	}
	counter := w.branchGens.counterFor(branch)
	return &WriteSession{
		ws:     w,
		branch: branch,
		gen:    counter.Load(),
	}
}

// Generation returns the current generation of branch. Useful for
// telemetry and for opening a session at a known generation.
func (w *Workspace) Generation(branch string) uint64 {
	if w.branchGens == nil {
		return 0
	}
	return w.branchGens.counterFor(branch).Load()
}

// Branch returns the branch this session writes to.
func (s *WriteSession) Branch() string { return s.branch }

// Generation returns the generation captured at BeginWriteSession.
func (s *WriteSession) Generation() uint64 { return s.gen }

// IsFenced reports whether this session has been overtaken by a
// successor (Seal happened on the same branch by another holder).
// Equivalent to a successful write returning ErrStaleGeneration.
func (s *WriteSession) IsFenced() bool {
	if s.sealed.Load() {
		return false // sealed by self, not fenced by other
	}
	return s.ws.branchGens.counterFor(s.branch).Load() > s.gen
}

// checkLive verifies the session can still write: not sealed by self
// and not overtaken by a successor.
func (s *WriteSession) checkLive() error {
	if s.sealed.Load() {
		return ErrSessionSealed
	}
	if s.ws.branchGens.counterFor(s.branch).Load() > s.gen {
		return ErrStaleGeneration
	}
	return nil
}

// WriteFile writes content through the session, returning the new
// snapshot ID on success or ErrStaleGeneration / ErrSessionSealed if
// the session is no longer authoritative.
func (s *WriteSession) WriteFile(filePath string, content []byte, mode fs.FileMode) (SnapshotID, error) {
	if err := s.checkLive(); err != nil {
		return 0, err
	}
	return s.ws.WriteFile(s.branch, filePath, content, mode)
}

// WriteFileBody writes a prepared FileBody through the session.
func (s *WriteSession) WriteFileBody(filePath string, body *FileBody, mode fs.FileMode) (SnapshotID, error) {
	if err := s.checkLive(); err != nil {
		return 0, err
	}
	return s.ws.WriteFileBody(s.branch, filePath, body, mode)
}

// Delete removes a path through the session.
func (s *WriteSession) Delete(filePath string) (SnapshotID, error) {
	if err := s.checkLive(); err != nil {
		return 0, err
	}
	return s.ws.Delete(s.branch, filePath)
}

// Seal hands off the branch atomically: the current branch HEAD is
// pinned and returned in the SealedHandoff, and the branch generation
// is bumped so any other live WriteSession on the same branch becomes
// stale on its next write. After Seal, this session itself rejects
// further writes with ErrSessionSealed.
//
// Seal is idempotent: a second call returns ErrSessionSealed without
// double-bumping the generation.
func (s *WriteSession) Seal() (SealedHandoff, error) {
	if !s.sealed.CompareAndSwap(false, true) {
		return SealedHandoff{}, ErrSessionSealed
	}
	counter := s.ws.branchGens.counterFor(s.branch)
	// Pin the snapshot we're sealing at before advancing the
	// generation. branchSnapshotLocked needs the workspace lock; we
	// drop it before bumping so writers can release naturally.
	s.ws.mu.RLock()
	snap, err := s.ws.branchSnapshotLocked(s.branch)
	s.ws.mu.RUnlock()
	if err != nil {
		// Unwind the seal so the caller can retry; we never bumped
		// the generation, so the workspace state is unchanged.
		s.sealed.Store(false)
		return SealedHandoff{}, err
	}
	nextGen := counter.Add(1)
	return SealedHandoff{
		Branch:     s.branch,
		SnapshotID: snap.ID,
		Generation: nextGen,
	}, nil
}
