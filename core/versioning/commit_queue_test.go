package versioning

import (
	"testing"
)

// enqueue is a test helper that panics on wal failure so test bodies
// stay focused on queue semantics. Production callers of Enqueue
// propagate the error.
func enqueue(t *testing.T, q *CommitQueue, desc MergeDescriptor) *CommitEntry {
	t.Helper()
	entry, err := q.Enqueue(desc)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	return entry
}

// TestCommitQueue_LifecycleAcceptedToCommitted verifies the happy-path
// lifecycle: auditing → accepted → committed → advanced.
func TestCommitQueue_LifecycleAcceptedToCommitted(t *testing.T) {
	q := NewCommitQueue(nil)
	desc := MergeDescriptor{
		PipelineID:    "task_a",
		MergedVersion: SemanticVersion{Major: 0, Minor: 1},
	}
	entry := enqueue(t, q, desc)
	if entry == nil || entry.State != CommitStateAuditing {
		t.Fatalf("enqueue state = %v, want auditing", entry)
	}
	if q.Depth() != 1 {
		t.Fatalf("depth = %d, want 1", q.Depth())
	}

	if err := q.MarkAccepted(desc.MergedVersion, "replica-1"); err != nil {
		t.Fatalf("MarkAccepted: %v", err)
	}
	head := q.Head()
	if head == nil || head.State != CommitStateAccepted {
		t.Fatalf("head state = %v, want accepted", head)
	}
	if head.AuditReplicaID != "replica-1" {
		t.Fatalf("replica_id = %q, want replica-1", head.AuditReplicaID)
	}

	if _, ok := q.Advance(); ok {
		t.Fatal("Advance popped an accepted-not-committed head")
	}

	if err := q.MarkCommitted(desc.MergedVersion); err != nil {
		t.Fatalf("MarkCommitted: %v", err)
	}
	popped, ok := q.Advance()
	if !ok {
		t.Fatal("Advance did not pop committed head")
	}
	if popped.Descriptor.PipelineID != "task_a" {
		t.Fatalf("popped pipeline = %q, want task_a", popped.Descriptor.PipelineID)
	}
	if q.Depth() != 0 {
		t.Fatalf("depth after pop = %d, want 0", q.Depth())
	}
}

func TestCommitQueue_FIFOBlockedByRejection(t *testing.T) {
	q := NewCommitQueue(nil)
	a := MergeDescriptor{PipelineID: "a", MergedVersion: SemanticVersion{Minor: 1}}
	b := MergeDescriptor{PipelineID: "b", MergedVersion: SemanticVersion{Minor: 2}}
	enqueue(t, q, a)
	enqueue(t, q, b)

	if err := q.MarkRejected(a.MergedVersion, "replica-a", "bad architecture", []string{"interface clash"}); err != nil {
		t.Fatalf("MarkRejected a: %v", err)
	}
	if err := q.MarkAccepted(b.MergedVersion, "replica-b"); err != nil {
		t.Fatalf("MarkAccepted b: %v", err)
	}

	if _, ok := q.Advance(); ok {
		t.Fatal("Advance popped while head is rejected-without-supersedor")
	}
	if q.Depth() != 2 {
		t.Fatalf("depth = %d, want 2", q.Depth())
	}
	if q.DepthBlocked() != 1 {
		t.Fatalf("blocked = %d, want 1", q.DepthBlocked())
	}
}

func TestCommitQueue_SupersessionPromotesRejectedSlot(t *testing.T) {
	q := NewCommitQueue(nil)
	a := MergeDescriptor{PipelineID: "a", MergedVersion: SemanticVersion{Minor: 1}}
	b := MergeDescriptor{PipelineID: "b", MergedVersion: SemanticVersion{Minor: 2}}
	remediation := MergeDescriptor{PipelineID: "a_fix_1", MergedVersion: SemanticVersion{Minor: 3}}
	enqueue(t, q, a)
	enqueue(t, q, b)
	enqueue(t, q, remediation)

	if err := q.MarkRejected(a.MergedVersion, "r1", "won't compile", nil); err != nil {
		t.Fatal(err)
	}
	if err := q.MarkAccepted(b.MergedVersion, "r2"); err != nil {
		t.Fatal(err)
	}
	if err := q.MarkAccepted(remediation.MergedVersion, "r3"); err != nil {
		t.Fatal(err)
	}
	if err := q.MarkSuperseded(a.MergedVersion, remediation.MergedVersion); err != nil {
		t.Fatal(err)
	}

	popped, ok := q.Advance()
	if !ok {
		t.Fatal("Advance did not pop superseded head")
	}
	if popped.Descriptor.PipelineID != "a" {
		t.Fatalf("popped pipeline = %q, want a", popped.Descriptor.PipelineID)
	}
	if popped.State != CommitStateSuperseded {
		t.Fatalf("popped state = %v, want superseded", popped.State)
	}

	head := q.Head()
	if head == nil || head.Descriptor.PipelineID != "b" {
		t.Fatalf("head after supersession pop = %v, want b", head)
	}
}

func TestCommitQueue_Abandon(t *testing.T) {
	q := NewCommitQueue(nil)
	d := MergeDescriptor{PipelineID: "d", MergedVersion: SemanticVersion{Minor: 1}}
	enqueue(t, q, d)

	if err := q.Abandon(d.MergedVersion, "DAG cancelled"); err != nil {
		t.Fatal(err)
	}
	head := q.Head()
	if head == nil || head.State != CommitStateAbandoned {
		t.Fatalf("head state = %v, want abandoned", head)
	}
	popped, ok := q.Advance()
	if !ok {
		t.Fatal("Advance did not pop abandoned head")
	}
	if popped.State != CommitStateAbandoned {
		t.Fatalf("popped state = %v, want abandoned", popped.State)
	}
}

func TestCommitQueue_LookupFindsEnqueued(t *testing.T) {
	q := NewCommitQueue(nil)
	d := MergeDescriptor{PipelineID: "x", MergedVersion: SemanticVersion{Minor: 7}}
	enqueue(t, q, d)
	got := q.Lookup(d.MergedVersion)
	if got == nil {
		t.Fatal("Lookup returned nil for enqueued version")
	}
	if got.Descriptor.PipelineID != "x" {
		t.Fatalf("looked up pipeline = %q, want x", got.Descriptor.PipelineID)
	}
	if q.Lookup(SemanticVersion{Minor: 99}) != nil {
		t.Fatal("Lookup returned non-nil for unknown version")
	}
}

func TestCommitQueue_DoubleEnqueueIsIdempotent(t *testing.T) {
	q := NewCommitQueue(nil)
	d := MergeDescriptor{PipelineID: "x", MergedVersion: SemanticVersion{Minor: 1}}
	a := enqueue(t, q, d)
	b := enqueue(t, q, d)
	if a != b {
		t.Fatal("Enqueue returned different entries for same MergedVersion")
	}
	if q.Depth() != 1 {
		t.Fatalf("depth = %d, want 1", q.Depth())
	}
}

func TestCommitQueue_InvalidTransitions(t *testing.T) {
	q := NewCommitQueue(nil)
	d := MergeDescriptor{PipelineID: "x", MergedVersion: SemanticVersion{Minor: 1}}
	enqueue(t, q, d)

	if err := q.MarkCommitted(d.MergedVersion); err == nil {
		t.Fatal("MarkCommitted on auditing: expected error")
	}
	other := SemanticVersion{Minor: 2}
	if err := q.MarkSuperseded(d.MergedVersion, other); err == nil {
		t.Fatal("MarkSuperseded on auditing: expected error")
	}
	if err := q.MarkAccepted(d.MergedVersion, "r"); err != nil {
		t.Fatal(err)
	}
	if err := q.MarkRejected(d.MergedVersion, "r", "", nil); err == nil {
		t.Fatal("MarkRejected on accepted: expected error")
	}
	if err := q.MarkCommitted(d.MergedVersion); err != nil {
		t.Fatal(err)
	}
	if err := q.Abandon(d.MergedVersion, ""); err == nil {
		t.Fatal("Abandon on committed: expected error")
	}
}

// TestCommitQueue_SubscribeReceivesEveryTransition pins the
// observer-pattern contract: Subscribe returns a channel that
// receives one event per state transition, in the same order the
// transitions were applied. The orchestrator's per-merge audit
// dispatcher subscribes on session Open; missing events would leave
// merges unaudited forever.
func TestCommitQueue_SubscribeReceivesEveryTransition(t *testing.T) {
	q := NewCommitQueue(nil)
	events, unsub := q.Subscribe()
	defer unsub()

	desc := MergeDescriptor{PipelineID: "p", MergedVersion: SemanticVersion{Minor: 1}}
	enqueue(t, q, desc)
	if err := q.MarkAccepted(desc.MergedVersion, "r1"); err != nil {
		t.Fatal(err)
	}
	if err := q.MarkCommitted(desc.MergedVersion); err != nil {
		t.Fatal(err)
	}

	want := []CommitQueueEventKind{
		CommitQueueEventEnqueued,
		CommitQueueEventDecided,
		CommitQueueEventCommitted,
	}
	for _, w := range want {
		select {
		case ev := <-events:
			if ev.Kind != w {
				t.Fatalf("event kind = %q, want %q", ev.Kind, w)
			}
			if ev.MergedVersion != desc.MergedVersion {
				t.Fatalf("event version = %v, want %v", ev.MergedVersion, desc.MergedVersion)
			}
		default:
			t.Fatalf("expected %q event, queue empty", w)
		}
	}
}

// TestCommitQueue_SubscribeSlowConsumerDoesNotBlockQueue pins the
// non-blocking-send contract. A subscriber that never drains its
// channel must not stall queue operations; sustained slowness causes
// dropped events (observable via Snapshot).
func TestCommitQueue_SubscribeSlowConsumerDoesNotBlockQueue(t *testing.T) {
	q := NewCommitQueue(nil)
	_, unsub := q.Subscribe()
	defer unsub()

	// Send more events than the 64-slot channel buffer. The queue
	// must still accept every Enqueue without blocking.
	for i := 0; i < 100; i++ {
		desc := MergeDescriptor{
			PipelineID:    "p",
			MergedVersion: SemanticVersion{Minor: uint32(i + 1)},
		}
		enqueue(t, q, desc)
	}
	if got := q.Depth(); got != 100 {
		t.Fatalf("depth = %d, want 100 (queue must not block on slow subscribers)", got)
	}
}

// TestCommitQueue_DurableAcrossReopen pins the core durability
// invariant: transitions persisted to ControlWAL are restored on
// restart, even if in-memory state was lost. After a crash, the
// queue rebuilt via ApplyControlEntry is bit-identical to the
// pre-crash state.
func TestCommitQueue_DurableAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	wal, err := OpenControlWAL(ControlWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("open wal: %v", err)
	}
	q1 := NewCommitQueue(wal)

	a := MergeDescriptor{PipelineID: "a", MergedVersion: SemanticVersion{Minor: 1}, BaseVersion: SemanticVersion{Minor: 0}}
	b := MergeDescriptor{PipelineID: "b", MergedVersion: SemanticVersion{Minor: 2}, BaseVersion: SemanticVersion{Minor: 1}}
	c := MergeDescriptor{PipelineID: "c_fix", MergedVersion: SemanticVersion{Minor: 3}, BaseVersion: SemanticVersion{Minor: 1}}
	enqueue(t, q1, a)
	enqueue(t, q1, b)
	enqueue(t, q1, c)
	if err := q1.MarkRejected(a.MergedVersion, "r-a", "needs fix", []string{"lint error"}); err != nil {
		t.Fatal(err)
	}
	if err := q1.MarkAccepted(b.MergedVersion, "r-b"); err != nil {
		t.Fatal(err)
	}
	if err := q1.MarkAccepted(c.MergedVersion, "r-c"); err != nil {
		t.Fatal(err)
	}
	if err := q1.MarkSuperseded(a.MergedVersion, c.MergedVersion); err != nil {
		t.Fatal(err)
	}
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}

	// Simulate reopen: fresh in-memory queue, replay WAL.
	wal2, err := OpenControlWAL(ControlWALConfig{Dir: dir})
	if err != nil {
		t.Fatalf("reopen wal: %v", err)
	}
	defer wal2.Close()

	descs := map[SemanticVersion]MergeDescriptor{
		a.MergedVersion: a,
		b.MergedVersion: b,
		c.MergedVersion: c,
	}
	q2 := NewCommitQueue(wal2)
	if err := wal2.Replay(func(e *ControlEntry) error {
		return q2.ApplyControlEntry(e, func(v SemanticVersion) (MergeDescriptor, bool) {
			d, ok := descs[v]
			return d, ok
		})
	}); err != nil {
		t.Fatalf("replay: %v", err)
	}

	// Post-replay state must match pre-crash state exactly.
	if q2.Depth() != 3 {
		t.Fatalf("depth after replay = %d, want 3", q2.Depth())
	}
	aEntry := q2.Lookup(a.MergedVersion)
	if aEntry == nil || aEntry.State != CommitStateSuperseded || aEntry.SupersededBy != c.MergedVersion {
		t.Fatalf("a after replay: %+v, want superseded by %v", aEntry, c.MergedVersion)
	}
	bEntry := q2.Lookup(b.MergedVersion)
	if bEntry == nil || bEntry.State != CommitStateAccepted || bEntry.AuditReplicaID != "r-b" {
		t.Fatalf("b after replay: %+v, want accepted by r-b", bEntry)
	}
	cEntry := q2.Lookup(c.MergedVersion)
	if cEntry == nil || cEntry.State != CommitStateAccepted || cEntry.AuditReplicaID != "r-c" {
		t.Fatalf("c after replay: %+v, want accepted by r-c", cEntry)
	}
}
