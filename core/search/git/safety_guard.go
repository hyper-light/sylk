package git

import (
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-git/go-git/v5/plumbing"
)

// workChCapacity is the buffer size for the async work channel.
// Derived from: bus dispatch is sequential (at most 1 operation active),
// each operation generates ≤3 async work items (Complete/Abort + backup),
// ×4 burst tolerance = 16.
const workChCapacity = 16

// safetyWorkKind distinguishes async work items processed by the worker.
type safetyWorkKind int

const (
	workLogComplete safetyWorkKind = iota
	workLogAbort
	workBackupBranch
	workBackupDetached
)

// safetyWork is a unit of async work for the worker goroutine.
type safetyWork struct {
	kind  safetyWorkKind
	entry *JournalEntry
	name  string        // branch name (workBackupBranch)
	hash  plumbing.Hash // commit hash (workBackupBranch/Detached)
}

// SafetyGuard coordinates pre/post-operation snapshots and journal logging.
// It subscribes to the GitBus and automatically creates snapshots + journal
// entries for all mutating operations.
//
// Optimizations over naive implementation:
//   - Async worker: PhasePost journal writes and backup operations run in a
//     dedicated goroutine, keeping the bus dispatch path fast.
//   - Sequencer coalescing: multi-step operations (rebase, cherry-pick sequence)
//     share a single snapshot, avoiding per-step overhead.
//   - In-memory ref index: sylk refs are tracked in memory, avoiding full
//     IterReferences scans on repos with thousands of refs.
//   - Cached dirty detection: uses StatusWatcher's cached status for O(1)
//     worktree dirty checks instead of full wt.Status() scans.
type SafetyGuard struct {
	client  *GitClient
	bus     *GitBus
	journal *Journal
	config  SafetyConfig
	watcher *StatusWatcher // optional, for cached dirty detection
	unsub   func()
	refIdx  *refIndex

	// Async worker for non-critical journal writes and backup operations.
	workCh     chan safetyWork
	workerDone chan struct{}

	// Inflight tracking for Begin/Complete correlation.
	mu       sync.Mutex
	inflight map[uint64]*JournalEntry

	// Sequencer coalescing: reuse the initial snapshot for all steps.
	seqActive  bool
	seqSnapRef string // snapshot ref name from the sequence start
}

// NewSafetyGuard creates a SafetyGuard, opens the journal, starts the async
// worker, and subscribes to the bus for automatic snapshot + journal logging.
// The watcher parameter is optional (nil disables cached dirty detection).
func NewSafetyGuard(client *GitClient, bus *GitBus, cfg SafetyConfig, watcher *StatusWatcher) (*SafetyGuard, error) {
	gitDir, err := resolveGitDir(client)
	if err != nil {
		return nil, fmt.Errorf("safety guard: resolve git dir: %w", err)
	}

	journalDir := filepath.Join(gitDir, journalDirName)
	journal, err := OpenJournal(journalDir)
	if err != nil {
		return nil, fmt.Errorf("safety guard: open journal: %w", err)
	}

	sg := &SafetyGuard{
		client:     client,
		bus:        bus,
		journal:    journal,
		config:     cfg,
		watcher:    watcher,
		refIdx:     newRefIndex(client),
		workCh:     make(chan safetyWork, workChCapacity),
		workerDone: make(chan struct{}),
		inflight:   make(map[uint64]*JournalEntry),
	}

	go sg.worker()

	// Subscribe to all mutating operations (nil ops = wildcard).
	sg.unsub = bus.Subscribe(nil, sg.onEvent)

	return sg, nil
}

// Close unsubscribes from the bus, drains the async worker, and closes the journal.
func (sg *SafetyGuard) Close() error {
	if sg.unsub != nil {
		sg.unsub()
	}
	close(sg.workCh)
	<-sg.workerDone // Wait for worker to drain queued work.
	return sg.journal.Close()
}

// RecoverOnStartup scans the journal for incomplete operations and
// resolves their snapshot refs for the caller to present recovery options.
// Uses the in-memory ref index for O(1) snapshot lookup.
func (sg *SafetyGuard) RecoverOnStartup() ([]IncompleteOp, error) {
	entries, err := sg.journal.FindIncompleteOps()
	if err != nil {
		return nil, err
	}

	if len(entries) == 0 {
		return nil, nil
	}

	// Use the ref index for fast snapshot lookup.
	snapRefs := sg.refIdx.listSnaps()
	snapByName := make(map[string]*SnapshotRef, len(snapRefs))
	for i := range snapRefs {
		snapByName[snapRefs[i].Name] = &snapRefs[i]
	}

	ops := make([]IncompleteOp, len(entries))
	for i, e := range entries {
		ops[i] = IncompleteOp{
			Entry:    e,
			Snapshot: snapByName[e.SnapRef],
		}
	}

	return ops, nil
}

// ResolveIncomplete handles an incomplete operation based on the chosen action.
func (sg *SafetyGuard) ResolveIncomplete(op IncompleteOp, action RecoveryAction) error {
	switch action {
	case RecoveryRollback:
		return sg.resolveRollback(op)
	case RecoveryResume:
		return sg.resolveResume(op)
	case RecoveryIgnore:
		return sg.resolveIgnore(op)
	default:
		return fmt.Errorf("safety guard: unknown recovery action %d", action)
	}
}

// resolveRollback restores the snapshot and marks the journal entry complete.
func (sg *SafetyGuard) resolveRollback(op IncompleteOp) error {
	if op.Snapshot == nil {
		return fmt.Errorf("safety guard: no snapshot available for rollback")
	}

	sg.client.mu.Lock()
	err := sg.client.restoreSnapshot(op.Snapshot)
	sg.client.mu.Unlock()
	if err != nil {
		return fmt.Errorf("safety guard: rollback: %w", err)
	}

	return sg.journal.LogComplete(&op.Entry)
}

// resolveResume accepts the current state and marks the entry complete.
func (sg *SafetyGuard) resolveResume(op IncompleteOp) error {
	return sg.journal.LogComplete(&op.Entry)
}

// resolveIgnore marks the entry as aborted without any state changes.
func (sg *SafetyGuard) resolveIgnore(op IncompleteOp) error {
	return sg.journal.LogAbort(&op.Entry)
}

// GCSnapshots removes old snapshot, deleted, and detached backup refs.
// Uses the ref index for enumeration and updates it on removal.
func (sg *SafetyGuard) GCSnapshots() (int, error) {
	sg.client.mu.Lock()
	defer sg.client.mu.Unlock()

	cutoff := time.Now().Add(-sg.config.SnapshotRetention)
	removed := 0

	// GC snapshot refs (count + age).
	snaps := sg.refIdx.listSnaps()
	for i, ref := range snaps {
		tooMany := i >= sg.config.MaxSnapshots
		tooOld := ref.Timestamp.Before(cutoff)
		if !tooMany && !tooOld {
			continue
		}
		refName := plumbing.ReferenceName(ref.Name)
		if err := sg.client.repo.Storer.RemoveReference(refName); err == nil {
			sg.refIdx.removeRef(ref.Name)
			removed++
		}
	}

	// GC deleted and detached backup refs (age only).
	removed += sg.gcBackupRefsIndexed(sg.refIdx.listDeleted(), cutoff)
	removed += sg.gcBackupRefsIndexed(sg.refIdx.listDetached(), cutoff)

	// GC abort and stash refs (age only).
	removed += sg.gcBackupRefsIndexed(sg.refIdx.listAborts(), cutoff)
	removed += sg.gcBackupRefsIndexed(sg.refIdx.listStashes(), cutoff)

	return removed, nil
}

// gcBackupRefsIndexed removes backup refs older than cutoff, updating the index.
func (sg *SafetyGuard) gcBackupRefsIndexed(refs []SnapshotRef, cutoff time.Time) int {
	removed := 0
	for _, ref := range refs {
		if ref.Timestamp.Before(cutoff) {
			refName := plumbing.ReferenceName(ref.Name)
			if err := sg.client.repo.Storer.RemoveReference(refName); err == nil {
				sg.refIdx.removeRef(ref.Name)
				removed++
			}
		}
	}
	return removed
}

// GCJournal removes old journal segments.
func (sg *SafetyGuard) GCJournal() error {
	cutoff := time.Now().Add(-sg.config.SnapshotRetention)
	return sg.journal.GC(cutoff)
}

// IsDirtyWorktree checks whether the worktree has uncommitted changes.
// Uses the StatusWatcher cache when available for O(1) detection,
// falling back to a full worktree scan otherwise.
func (sg *SafetyGuard) IsDirtyWorktree() bool {
	if sg.watcher != nil {
		if dirty, ok := isDirtyFromCachedStatus(sg.watcher.LastUpdate()); ok {
			return dirty
		}
	}
	return sg.client.isDirtyWorktree()
}

// =============================================================================
// Async worker
// =============================================================================

// worker drains the work channel and processes items sequentially.
// Journal writes are serialized through this goroutine, keeping the
// bus dispatch path non-blocking for PhasePost events and backups.
func (sg *SafetyGuard) worker() {
	defer close(sg.workerDone)
	for w := range sg.workCh {
		sg.processWork(w)
	}
}

// processWork handles a single async work item.
func (sg *SafetyGuard) processWork(w safetyWork) {
	switch w.kind {
	case workLogComplete:
		_ = sg.journal.LogComplete(w.entry)
	case workLogAbort:
		_ = sg.journal.LogAbort(w.entry)
	case workBackupBranch:
		if snap, err := sg.client.createBranchBackup(w.name, w.hash); err == nil {
			sg.refIdx.addDeleted(*snap)
		}
	case workBackupDetached:
		if snap, err := sg.client.createDetachedBackup(w.hash); err == nil {
			sg.refIdx.addDetached(*snap)
		}
	}
}

// queueWork sends a work item to the async worker. Non-blocking: drops
// the item if the channel is full (best-effort).
func (sg *SafetyGuard) queueWork(w safetyWork) {
	select {
	case sg.workCh <- w:
	default:
	}
}

// =============================================================================
// Bus event handler
// =============================================================================

// onEvent is the bus event handler. PhasePre creates snapshots and logs
// Begin entries synchronously (required for crash recovery correctness).
// PhasePost and backup operations are queued to the async worker.
func (sg *SafetyGuard) onEvent(event *GitEvent) {
	if !event.IsMutation() {
		return
	}

	switch event.Phase {
	case PhasePre:
		sg.handlePre(event)
	case PhasePost:
		sg.handlePost(event)
	}
}

// handlePre creates a snapshot and logs a Begin entry before the operation.
// Snapshot creation and LogBegin are synchronous (must complete before the
// operation starts for crash recovery). Backup operations are async.
func (sg *SafetyGuard) handlePre(event *GitEvent) {
	sg.mu.Lock()
	_, isSeqStart := sequencerStartOps[event.Op]
	_, isSeqStep := sequencerStepOps[event.Op]
	skipSnapshot := isSeqStep && sg.seqActive
	if isSeqStart {
		sg.seqActive = true
	}
	sg.mu.Unlock()

	var snap *SnapshotRef
	var snapRef string

	if !skipSnapshot {
		var err error
		snap, err = sg.client.createSnapshot(event.Op)
		if err != nil {
			return // Best-effort: snapshot failure shouldn't block the operation.
		}
		if snap != nil {
			sg.refIdx.addSnap(*snap)
			snapRef = snap.Name
		}
		if isSeqStart {
			sg.mu.Lock()
			sg.seqSnapRef = snapRef
			sg.mu.Unlock()
		}
	} else {
		// Reuse the sequence-start snapshot.
		sg.mu.Lock()
		snapRef = sg.seqSnapRef
		sg.mu.Unlock()
	}

	headHash := plumbing.ZeroHash
	branchRef := ""
	if snap != nil {
		headHash = snap.Hash
		branchRef = snap.BranchRef
	}

	entry := &JournalEntry{
		Op:        event.Op,
		Timestamp: event.Timestamp,
		HeadHash:  headHash,
		BranchRef: branchRef,
		Params:    formatEventParams(event),
		SnapRef:   snapRef,
	}

	// LogBegin is synchronous — must be written before the operation starts
	// so that crash recovery can detect incomplete operations.
	if err := sg.journal.LogBegin(entry); err != nil {
		return // Best-effort.
	}

	sg.mu.Lock()
	sg.inflight[entry.Seq] = entry
	sg.mu.Unlock()

	// Queue backup operations to the async worker.
	sg.queueBackups(event)
}

// handlePost logs a Complete or Abort entry after the operation.
// Journal writes are queued to the async worker to keep the dispatch
// path non-blocking.
func (sg *SafetyGuard) handlePost(event *GitEvent) {
	sg.mu.Lock()
	// Find the inflight entry by scanning for the matching op.
	var entry *JournalEntry
	var seq uint64
	for s, e := range sg.inflight {
		if e.Op == event.Op {
			entry = e
			seq = s
			break
		}
	}
	if entry != nil {
		delete(sg.inflight, seq)
	}

	// Update sequencer coalescing state.
	if sg.seqActive {
		if event.Op == OpSequencerAbort || isSequencerDone(event) {
			sg.seqActive = false
			sg.seqSnapRef = ""
		}
	}
	sg.mu.Unlock()

	if entry == nil {
		return // No matching begin — begin may have failed.
	}

	if event.Err != nil {
		entry.Err = event.Err.Error()
		sg.queueWork(safetyWork{kind: workLogAbort, entry: entry})
	} else {
		sg.queueWork(safetyWork{kind: workLogComplete, entry: entry})
	}
}

// queueBackups queues branch/detached backup operations to the async worker.
// The ref hash is read synchronously (before the operation modifies state),
// but the backup ref creation is async.
func (sg *SafetyGuard) queueBackups(event *GitEvent) {
	if event.Op == OpDeleteBranch {
		if name, ok := event.Params.(string); ok {
			refName := plumbing.NewBranchReferenceName(name)
			if ref, err := sg.client.repo.Reference(refName, true); err == nil {
				sg.queueWork(safetyWork{
					kind: workBackupBranch,
					name: name,
					hash: ref.Hash(),
				})
			}
		}
	}

	if event.Op == OpCheckoutBranch || event.Op == OpCheckoutCommit {
		head, err := sg.client.repo.Head()
		if err == nil && !head.Name().IsBranch() {
			sg.queueWork(safetyWork{
				kind: workBackupDetached,
				hash: head.Hash(),
			})
		}
	}
}

// isSequencerDone returns true when a PhasePost event indicates the
// multi-step sequencer has completed (all steps applied or error).
func isSequencerDone(event *GitEvent) bool {
	_, isStart := sequencerStartOps[event.Op]
	_, isStep := sequencerStepOps[event.Op]
	if !isStart && !isStep {
		return false
	}

	// On error, the sequence is terminated.
	if event.Err != nil {
		return true
	}

	// A nil *SequencerStatus result means state=SeqIdle (sequence complete).
	status, ok := event.Result.(*SequencerStatus)
	return ok && status == nil
}

// formatEventParams converts event params to a string for journal storage.
func formatEventParams(event *GitEvent) string {
	if event.Params == nil {
		return ""
	}
	return fmt.Sprintf("%v", event.Params)
}
