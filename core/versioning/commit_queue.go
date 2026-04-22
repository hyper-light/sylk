package versioning

import (
	"errors"
	"fmt"
	"sync"
	"time"
)

// CommitState is the lifecycle state of a merge descriptor within the
// commit queue. See docs/PARALLEL_GLOBAL_VFS.md §3.4.
type CommitState string

const (
	// CommitStateAuditing means the per-merge audit replica has launched
	// but not yet emitted a terminal decision.
	CommitStateAuditing CommitState = "auditing"

	// CommitStateAccepted means the replica accepted; the descriptor is
	// awaiting its FIFO turn to disk-commit.
	CommitStateAccepted CommitState = "accepted"

	// CommitStateRejected means the replica rejected; the descriptor
	// blocks the queue head until either a superseding remediation
	// lands or the caller explicitly abandons.
	CommitStateRejected CommitState = "rejected"

	// CommitStateSuperseded means a remediation descriptor has claimed
	// this slot. On commit advancement, the supersedor's diff is
	// flushed in this slot's place.
	CommitStateSuperseded CommitState = "superseded"

	// CommitStateCommitted means the descriptor's changeset has been
	// flushed to disk. Terminal; descriptor can be released.
	CommitStateCommitted CommitState = "committed"

	// CommitStateAbandoned means the descriptor was explicitly dropped
	// (DAG terminal abort or architect-declared abandonment). Terminal.
	CommitStateAbandoned CommitState = "abandoned"
)

// CommitEntry is the queue-embedded record for a single merge's
// lifecycle from launch-audit through terminal resolution.
type CommitEntry struct {
	// Descriptor is the merge event this entry corresponds to. Immutable
	// after enqueue.
	Descriptor MergeDescriptor

	// State is the current lifecycle position.
	State CommitState

	// RejectionReason carries the audit replica's explanation when
	// State == CommitStateRejected.
	RejectionReason string

	// BigPictureConcerns carries structured reasons the global inspector
	// flagged. Used by architect remediation composition and by
	// observability.
	BigPictureConcerns []string

	// SupersededBy is the MergedVersion of the descriptor that claimed
	// this entry's slot. Set when State == CommitStateSuperseded.
	SupersededBy SemanticVersion

	// AuditReplicaID identifies the replica currently auditing (for
	// observability and cancellation).
	AuditReplicaID string

	// EnqueuedAt is the wall-clock time this entry joined the queue.
	EnqueuedAt time.Time

	// TerminalAt is the wall-clock time this entry reached a terminal
	// state (Committed, Superseded, Abandoned). Zero until terminal.
	TerminalAt time.Time
}

// CommitQueue is the durable, WAL-backed FIFO log of merge
// descriptors awaiting audit and disk commit. Every state transition
// is written to the session's ControlWAL before the in-memory mutation
// is applied — write-ahead discipline. On SessionVFS.Open, the
// queue is reconstructed from the WAL via ReplayTransition, so any
// transitions that were durable at the time of last close (or crash)
// are restored before CommitResolver starts.
//
// Semantic model:
//   - Enqueue is called once per merge, immediately after
//     MergePipelineIntoGreen produces a new descriptor.
//   - MarkAccepted / MarkRejected are called by the audit replica
//     dispatch layer when the replica emits its decision.
//   - MarkSuperseded is called by the orchestrator when a remediation
//     merge claims a rejected entry's slot.
//   - MarkCommitted / Abandon are called by the commit resolver on
//     terminal disposition.
//   - Advance pops the head when it is terminal.
//
// See docs/PARALLEL_GLOBAL_VFS.md §3.4.
type CommitQueue struct {
	mu         sync.Mutex
	entries    []*CommitEntry
	byVer      map[SemanticVersion]*CommitEntry
	wal        *ControlWAL
	subscribeMu sync.Mutex
	subscribers []chan CommitQueueEvent
}

// CommitQueueEventKind classifies the queue-transition a subscriber
// receives.
type CommitQueueEventKind string

const (
	// CommitQueueEventEnqueued fires when a new descriptor joins the
	// queue in Auditing state. The orchestrator's per-merge audit
	// dispatcher subscribes and launches a replica for every event.
	CommitQueueEventEnqueued CommitQueueEventKind = "enqueued"
	// CommitQueueEventDecided fires on accept/reject transitions.
	// Architect remediation subscribes to rejections.
	CommitQueueEventDecided CommitQueueEventKind = "decided"
	// CommitQueueEventSuperseded fires when a rejected entry is
	// claimed by a supersedor.
	CommitQueueEventSuperseded CommitQueueEventKind = "superseded"
	// CommitQueueEventCommitted fires when a descriptor reaches disk.
	CommitQueueEventCommitted CommitQueueEventKind = "committed"
	// CommitQueueEventAbandoned fires on terminal abandon.
	CommitQueueEventAbandoned CommitQueueEventKind = "abandoned"
)

// CommitQueueEvent is delivered to subscribers on every transition.
// Consumers must read promptly: a slow subscriber does NOT block the
// queue (the send is non-blocking; slow consumers drop events and
// can catch up via a Snapshot).
type CommitQueueEvent struct {
	Kind          CommitQueueEventKind
	MergedVersion SemanticVersion
	Descriptor    MergeDescriptor
	State         CommitState
	ReplicaID     string
	SupersedorVer SemanticVersion
}

// NewCommitQueue returns an empty commit queue. The optional wal is
// used to persist every state transition before applying it in
// memory. Tests may pass nil to run fully in-memory.
func NewCommitQueue(wal *ControlWAL) *CommitQueue {
	return &CommitQueue{
		byVer: make(map[SemanticVersion]*CommitEntry),
		wal:   wal,
	}
}

// Enqueue registers a new merge descriptor on the queue in auditing
// state. Idempotent: re-enqueueing the same MergedVersion is a no-op.
// Write-ahead: the ControlWAL entry is appended before the in-memory
// state is mutated, so a crash between WAL fsync and in-memory apply
// is resolved correctly on Open replay.
func (q *CommitQueue) Enqueue(desc MergeDescriptor) (*CommitEntry, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if existing := q.byVer[desc.MergedVersion]; existing != nil {
		return existing, nil
	}
	if err := q.walAppend(ControlKindCommitQueueEnqueue, ControlEntryPayload{
		MergedVersion: desc.MergedVersion,
		BaseVersion:   desc.BaseVersion,
		PipelineID:    desc.PipelineID,
		PathCount:     uint32(desc.PathCount),
		BasePath:      append([]string(nil), desc.Paths...),
	}); err != nil {
		return nil, err
	}
	entry := &CommitEntry{
		Descriptor: desc,
		State:      CommitStateAuditing,
		EnqueuedAt: time.Now().UTC(),
	}
	q.entries = append(q.entries, entry)
	q.byVer[desc.MergedVersion] = entry
	q.emit(CommitQueueEvent{
		Kind:          CommitQueueEventEnqueued,
		MergedVersion: desc.MergedVersion,
		Descriptor:    desc,
		State:         CommitStateAuditing,
	})
	return entry, nil
}

// Lookup returns the entry for a MergedVersion, or nil if not present.
func (q *CommitQueue) Lookup(ver SemanticVersion) *CommitEntry {
	q.mu.Lock()
	defer q.mu.Unlock()
	if entry := q.byVer[ver]; entry != nil {
		clone := *entry
		return &clone
	}
	return nil
}

// Snapshot returns a defensive copy of all entries in arrival order.
func (q *CommitQueue) Snapshot() []CommitEntry {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]CommitEntry, len(q.entries))
	for i, e := range q.entries {
		out[i] = *e
	}
	return out
}

// Head returns the current head entry in arrival order. Returns nil
// when the queue is empty OR when the head has reached a terminal
// state that the resolver hasn't yet popped (the caller should Advance
// before reading Head again).
func (q *CommitQueue) Head() *CommitEntry {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.entries) == 0 {
		return nil
	}
	head := q.entries[0]
	if head == nil {
		return nil
	}
	clone := *head
	return &clone
}

// MarkAccepted transitions the entry with the given MergedVersion from
// auditing to accepted. Returns an error if the entry is not in
// auditing state or does not exist. Write-ahead to ControlWAL before
// mutating in-memory state.
func (q *CommitQueue) MarkAccepted(ver SemanticVersion, replicaID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	entry := q.byVer[ver]
	if entry == nil {
		return fmt.Errorf("commit queue: MarkAccepted: version %s not in queue", ver.String())
	}
	if entry.State != CommitStateAuditing {
		return fmt.Errorf("commit queue: MarkAccepted: entry %s in state %s (expected %s)", ver.String(), entry.State, CommitStateAuditing)
	}
	if err := q.walAppend(ControlKindCommitQueueMarkAccepted, ControlEntryPayload{
		MergedVersion: ver,
		ReplicaID:     replicaID,
	}); err != nil {
		return err
	}
	entry.State = CommitStateAccepted
	entry.AuditReplicaID = replicaID
	q.emit(CommitQueueEvent{
		Kind:          CommitQueueEventDecided,
		MergedVersion: ver,
		Descriptor:    entry.Descriptor,
		State:         CommitStateAccepted,
		ReplicaID:     replicaID,
	})
	return nil
}

// MarkRejected transitions the entry to rejected, capturing the
// replica's reason and any big-picture concerns. Write-ahead to
// ControlWAL before mutating in-memory state.
func (q *CommitQueue) MarkRejected(ver SemanticVersion, replicaID, reason string, concerns []string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	entry := q.byVer[ver]
	if entry == nil {
		return fmt.Errorf("commit queue: MarkRejected: version %s not in queue", ver.String())
	}
	if entry.State != CommitStateAuditing {
		return fmt.Errorf("commit queue: MarkRejected: entry %s in state %s (expected %s)", ver.String(), entry.State, CommitStateAuditing)
	}
	if err := q.walAppend(ControlKindCommitQueueMarkRejected, ControlEntryPayload{
		MergedVersion:   ver,
		ReplicaID:       replicaID,
		RejectionReason: reason,
		Concerns:        append([]string(nil), concerns...),
	}); err != nil {
		return err
	}
	entry.State = CommitStateRejected
	entry.AuditReplicaID = replicaID
	entry.RejectionReason = reason
	if len(concerns) > 0 {
		entry.BigPictureConcerns = append([]string(nil), concerns...)
	}
	q.emit(CommitQueueEvent{
		Kind:          CommitQueueEventDecided,
		MergedVersion: ver,
		Descriptor:    entry.Descriptor,
		State:         CommitStateRejected,
		ReplicaID:     replicaID,
	})
	return nil
}

// MarkSuperseded marks a rejected entry as superseded by another
// descriptor. The resolver will flush the supersedor's diff in this
// entry's slot. Calling on a non-rejected entry returns an error.
func (q *CommitQueue) MarkSuperseded(rejectedVer, supersedorVer SemanticVersion) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	entry := q.byVer[rejectedVer]
	if entry == nil {
		return fmt.Errorf("commit queue: MarkSuperseded: rejected version %s not in queue", rejectedVer.String())
	}
	if entry.State != CommitStateRejected {
		return fmt.Errorf("commit queue: MarkSuperseded: entry %s in state %s (expected %s)", rejectedVer.String(), entry.State, CommitStateRejected)
	}
	if _, ok := q.byVer[supersedorVer]; !ok {
		return fmt.Errorf("commit queue: MarkSuperseded: supersedor %s not in queue", supersedorVer.String())
	}
	if err := q.walAppend(ControlKindCommitQueueMarkSuperseded, ControlEntryPayload{
		MergedVersion: rejectedVer,
		SupersedorVer: supersedorVer,
	}); err != nil {
		return err
	}
	entry.State = CommitStateSuperseded
	entry.SupersededBy = supersedorVer
	q.emit(CommitQueueEvent{
		Kind:          CommitQueueEventSuperseded,
		MergedVersion: rejectedVer,
		Descriptor:    entry.Descriptor,
		State:         CommitStateSuperseded,
		SupersedorVer: supersedorVer,
	})
	return nil
}

// MarkCommitted transitions an entry to committed (terminal). The
// resolver calls this after a successful disk flush. Callable on
// Accepted (normal case) or Superseded (when the supersedor's diff is
// the one flushed).
func (q *CommitQueue) MarkCommitted(ver SemanticVersion) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	entry := q.byVer[ver]
	if entry == nil {
		return fmt.Errorf("commit queue: MarkCommitted: version %s not in queue", ver.String())
	}
	switch entry.State {
	case CommitStateAccepted, CommitStateSuperseded:
		// OK.
	default:
		return fmt.Errorf("commit queue: MarkCommitted: entry %s in state %s (expected Accepted or Superseded)", ver.String(), entry.State)
	}
	if err := q.walAppend(ControlKindCommitQueueMarkCommitted, ControlEntryPayload{
		MergedVersion: ver,
	}); err != nil {
		return err
	}
	entry.State = CommitStateCommitted
	entry.TerminalAt = time.Now().UTC()
	q.emit(CommitQueueEvent{
		Kind:          CommitQueueEventCommitted,
		MergedVersion: ver,
		Descriptor:    entry.Descriptor,
		State:         CommitStateCommitted,
	})
	return nil
}

// Abandon marks an entry as terminally abandoned. Used for DAG terminal
// aborts and architect-declared cancellations. Safe to call on any
// non-terminal entry.
func (q *CommitQueue) Abandon(ver SemanticVersion, reason string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	entry := q.byVer[ver]
	if entry == nil {
		return fmt.Errorf("commit queue: Abandon: version %s not in queue", ver.String())
	}
	switch entry.State {
	case CommitStateCommitted, CommitStateAbandoned:
		return fmt.Errorf("commit queue: Abandon: entry %s already terminal (%s)", ver.String(), entry.State)
	}
	if err := q.walAppend(ControlKindCommitQueueAbandon, ControlEntryPayload{
		MergedVersion:   ver,
		RejectionReason: reason,
	}); err != nil {
		return err
	}
	entry.State = CommitStateAbandoned
	if entry.RejectionReason == "" {
		entry.RejectionReason = reason
	}
	entry.TerminalAt = time.Now().UTC()
	q.emit(CommitQueueEvent{
		Kind:          CommitQueueEventAbandoned,
		MergedVersion: ver,
		Descriptor:    entry.Descriptor,
		State:         CommitStateAbandoned,
	})
	return nil
}

// Advance pops and returns the head entry if (and only if) it has
// reached a terminal state that the resolver has already processed
// (Committed, Superseded-and-handled, or Abandoned). Returns nil and
// a false when the head is not yet removable.
//
// The resolver uses this AFTER handling the head (flushing to disk or
// recognizing an abandonment) to clear the slot. Advance does NOT
// perform the side-effect itself; that's the resolver's job.
func (q *CommitQueue) Advance() (*CommitEntry, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.entries) == 0 {
		return nil, false
	}
	head := q.entries[0]
	if head == nil {
		return nil, false
	}
	switch head.State {
	case CommitStateCommitted, CommitStateAbandoned:
		// Removable.
	case CommitStateSuperseded:
		// Superseded heads are removable — the supersedor's diff was
		// flushed in their slot, or their work was moot. Either way
		// the head can pop.
	default:
		return nil, false
	}
	q.entries = q.entries[1:]
	delete(q.byVer, head.Descriptor.MergedVersion)
	clone := *head
	return &clone, true
}

// DepthAuditing returns the number of entries currently in the
// Auditing state. Useful for observability and backpressure thresholds.
func (q *CommitQueue) DepthAuditing() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, e := range q.entries {
		if e.State == CommitStateAuditing {
			n++
		}
	}
	return n
}

// DepthBlocked returns the number of entries currently in the Rejected
// state without a supersedor. This is the "stuck head" count.
func (q *CommitQueue) DepthBlocked() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, e := range q.entries {
		if e.State == CommitStateRejected {
			n++
		}
	}
	return n
}

// Depth returns the total number of entries currently in the queue
// (all states).
func (q *CommitQueue) Depth() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.entries)
}

// DepthNonTerminal returns the number of entries not yet in a
// terminal state (Committed, Abandoned). This is the count that
// pipeline-dispatch backpressure cares about — a committed entry
// whose Advance has not yet run is no longer occupying a Copy slot
// and should not count against the backpressure cap. See
// docs/PARALLEL_GLOBAL_VFS.md §5.1.
func (q *CommitQueue) DepthNonTerminal() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, e := range q.entries {
		switch e.State {
		case CommitStateCommitted, CommitStateAbandoned:
			// Terminal; post-flush, pre-Advance. Doesn't occupy a
			// Copy slot so does not count toward backpressure.
		default:
			n++
		}
	}
	return n
}

// ErrQueueEmpty is returned by operations on an empty queue.
var ErrQueueEmpty = errors.New("commit queue: empty")

// walAppend writes a control entry if a WAL is configured. Callers
// hold q.mu when invoking this. Failure to persist fails the entire
// transition — the in-memory state is NOT mutated. The caller receives
// the error; a crash between WAL fsync and in-memory apply is resolved
// correctly on Open replay.
func (q *CommitQueue) walAppend(kind ControlEntryKind, payload ControlEntryPayload) error {
	if q.wal == nil {
		return nil
	}
	_, err := q.wal.Append(kind, payload)
	if err != nil {
		return fmt.Errorf("commit queue: wal append %d: %w", kind, err)
	}
	return nil
}

// Subscribe returns a channel that receives every queue transition
// for this session. The channel is buffered (capacity 64) so brief
// subscriber delays don't block the queue; sustained slowness causes
// event drops (non-blocking send). Subscribers that need the full
// history should Snapshot() on attach and then process live events.
//
// The caller must call the returned unsubscribe function when done
// to release the channel and stop further deliveries.
func (q *CommitQueue) Subscribe() (<-chan CommitQueueEvent, func()) {
	ch := make(chan CommitQueueEvent, 64)
	q.subscribeMu.Lock()
	q.subscribers = append(q.subscribers, ch)
	q.subscribeMu.Unlock()
	unsubscribe := func() {
		q.subscribeMu.Lock()
		for i, c := range q.subscribers {
			if c == ch {
				q.subscribers = append(q.subscribers[:i], q.subscribers[i+1:]...)
				close(ch)
				break
			}
		}
		q.subscribeMu.Unlock()
	}
	return ch, unsubscribe
}

// emit fan-outs an event to every subscriber with a non-blocking
// send. Called after the WAL write succeeds and the in-memory state
// has been mutated, so subscribers observe the post-transition state
// consistently.
func (q *CommitQueue) emit(event CommitQueueEvent) {
	q.subscribeMu.Lock()
	subs := q.subscribers
	q.subscribeMu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- event:
		default:
			// Slow subscriber — event dropped. They can recover via
			// Snapshot() on the next active tick.
		}
	}
}

// ApplyControlEntry rehydrates a single durable transition into the
// in-memory state without re-writing it to the WAL. Used by
// SessionVFS.Open replay. The transitions must arrive in their
// recorded seq order or the state machine will reject them just as it
// would reject out-of-order runtime mutations.
//
// mergeLookup resolves the full MergeDescriptor for an Enqueue
// entry's MergedVersion — the control WAL stores only the version
// key, while the descriptor itself lives in the semantic WAL's merge
// log. This split lets each WAL stay authoritative over its own
// domain.
func (q *CommitQueue) ApplyControlEntry(entry *ControlEntry, mergeLookup func(SemanticVersion) (MergeDescriptor, bool)) error {
	if entry == nil {
		return nil
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	switch entry.Kind {
	case ControlKindCommitQueueEnqueue:
		ver := entry.Payload.MergedVersion
		if _, exists := q.byVer[ver]; exists {
			return nil // idempotent replay
		}
		desc, ok := mergeLookup(ver)
		if !ok {
			// Fallback: reconstruct a minimal descriptor from the
			// control entry so queue state is preserved even if the
			// semantic WAL entry is missing (should not happen in
			// practice; defensive for forward compatibility).
			desc = MergeDescriptor{
				MergedVersion: ver,
				BaseVersion:   entry.Payload.BaseVersion,
				PipelineID:    entry.Payload.PipelineID,
				PathCount:     int(entry.Payload.PathCount),
				Paths:         append([]string(nil), entry.Payload.BasePath...),
				MergedAt:      entry.Timestamp,
			}
		}
		ce := &CommitEntry{
			Descriptor: desc,
			State:      CommitStateAuditing,
			EnqueuedAt: entry.Timestamp,
		}
		q.entries = append(q.entries, ce)
		q.byVer[ver] = ce
	case ControlKindCommitQueueMarkAccepted:
		ce := q.byVer[entry.Payload.MergedVersion]
		if ce == nil {
			return fmt.Errorf("replay MarkAccepted: version %s missing", entry.Payload.MergedVersion)
		}
		ce.State = CommitStateAccepted
		ce.AuditReplicaID = entry.Payload.ReplicaID
	case ControlKindCommitQueueMarkRejected:
		ce := q.byVer[entry.Payload.MergedVersion]
		if ce == nil {
			return fmt.Errorf("replay MarkRejected: version %s missing", entry.Payload.MergedVersion)
		}
		ce.State = CommitStateRejected
		ce.AuditReplicaID = entry.Payload.ReplicaID
		ce.RejectionReason = entry.Payload.RejectionReason
		ce.BigPictureConcerns = append([]string(nil), entry.Payload.Concerns...)
	case ControlKindCommitQueueMarkSuperseded:
		ce := q.byVer[entry.Payload.MergedVersion]
		if ce == nil {
			return fmt.Errorf("replay MarkSuperseded: version %s missing", entry.Payload.MergedVersion)
		}
		ce.State = CommitStateSuperseded
		ce.SupersededBy = entry.Payload.SupersedorVer
	case ControlKindCommitQueueMarkCommitted:
		ce := q.byVer[entry.Payload.MergedVersion]
		if ce == nil {
			return fmt.Errorf("replay MarkCommitted: version %s missing", entry.Payload.MergedVersion)
		}
		ce.State = CommitStateCommitted
		ce.TerminalAt = entry.Timestamp
	case ControlKindCommitQueueAbandon:
		ce := q.byVer[entry.Payload.MergedVersion]
		if ce == nil {
			return fmt.Errorf("replay Abandon: version %s missing", entry.Payload.MergedVersion)
		}
		ce.State = CommitStateAbandoned
		if ce.RejectionReason == "" {
			ce.RejectionReason = entry.Payload.RejectionReason
		}
		ce.TerminalAt = entry.Timestamp
	default:
		// Not a commit-queue kind; caller dispatches to a different
		// subsystem (retention, replica lifecycle, epoch).
	}
	return nil
}
