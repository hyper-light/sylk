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

// CommitQueue is the durable (in the future; in-memory for stage 3)
// FIFO log of merge descriptors awaiting audit and disk commit.
//
// Semantic model:
//   - Enqueue is called once per merge, immediately after
//     MergePipelineIntoGreen produces a new descriptor.
//   - MarkAccepted / MarkRejected are called by the audit replica
//     dispatch layer when the replica emits its decision.
//   - Advance is called by the commit resolver to pop the head when it
//     is terminal (Committed or Superseded/dropped).
//
// See docs/PARALLEL_GLOBAL_VFS.md §3.4 for the design.
type CommitQueue struct {
	mu      sync.Mutex
	entries []*CommitEntry
	byVer   map[SemanticVersion]*CommitEntry
}

// NewCommitQueue returns an empty commit queue.
func NewCommitQueue() *CommitQueue {
	return &CommitQueue{
		byVer: make(map[SemanticVersion]*CommitEntry),
	}
}

// Enqueue registers a new merge descriptor on the queue in auditing
// state. Idempotent: re-enqueueing the same MergedVersion is a no-op.
func (q *CommitQueue) Enqueue(desc MergeDescriptor) *CommitEntry {
	q.mu.Lock()
	defer q.mu.Unlock()
	if existing := q.byVer[desc.MergedVersion]; existing != nil {
		return existing
	}
	entry := &CommitEntry{
		Descriptor: desc,
		State:      CommitStateAuditing,
		EnqueuedAt: time.Now().UTC(),
	}
	q.entries = append(q.entries, entry)
	q.byVer[desc.MergedVersion] = entry
	return entry
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
// auditing state or does not exist.
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
	entry.State = CommitStateAccepted
	entry.AuditReplicaID = replicaID
	return nil
}

// MarkRejected transitions the entry to rejected, capturing the
// replica's reason and any big-picture concerns.
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
	entry.State = CommitStateRejected
	entry.AuditReplicaID = replicaID
	entry.RejectionReason = reason
	if len(concerns) > 0 {
		entry.BigPictureConcerns = append([]string(nil), concerns...)
	}
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
	entry.State = CommitStateSuperseded
	entry.SupersededBy = supersedorVer
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
	entry.State = CommitStateCommitted
	entry.TerminalAt = time.Now().UTC()
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
	entry.State = CommitStateAbandoned
	if entry.RejectionReason == "" {
		entry.RejectionReason = reason
	}
	entry.TerminalAt = time.Now().UTC()
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

// ErrQueueEmpty is returned by operations on an empty queue.
var ErrQueueEmpty = errors.New("commit queue: empty")
