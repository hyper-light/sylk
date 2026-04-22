package versioning

import (
	"testing"
)

// TestCommitQueue_LifecycleAcceptedToCommitted verifies the happy-path
// lifecycle: auditing → accepted → committed → advanced.
func TestCommitQueue_LifecycleAcceptedToCommitted(t *testing.T) {
	q := NewCommitQueue()
	desc := MergeDescriptor{
		PipelineID:    "task_a",
		MergedVersion: SemanticVersion{Major: 0, Minor: 1},
	}
	entry := q.Enqueue(desc)
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

	// Advance shouldn't pop an accepted-but-not-committed entry.
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

// TestCommitQueue_FIFOBlockedByRejection verifies that a rejected head
// blocks subsequent accepted entries from advancing.
func TestCommitQueue_FIFOBlockedByRejection(t *testing.T) {
	q := NewCommitQueue()
	a := MergeDescriptor{PipelineID: "a", MergedVersion: SemanticVersion{Minor: 1}}
	b := MergeDescriptor{PipelineID: "b", MergedVersion: SemanticVersion{Minor: 2}}
	q.Enqueue(a)
	q.Enqueue(b)

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

// TestCommitQueue_SupersessionPromotesRejectedSlot verifies the
// supersession flow: a rejected entry is claimed by a later descriptor;
// Advance pops the rejected head when marked Superseded.
func TestCommitQueue_SupersessionPromotesRejectedSlot(t *testing.T) {
	q := NewCommitQueue()
	a := MergeDescriptor{PipelineID: "a", MergedVersion: SemanticVersion{Minor: 1}}
	b := MergeDescriptor{PipelineID: "b", MergedVersion: SemanticVersion{Minor: 2}}
	remediation := MergeDescriptor{PipelineID: "a_fix_1", MergedVersion: SemanticVersion{Minor: 3}}
	q.Enqueue(a)
	q.Enqueue(b)
	q.Enqueue(remediation)

	// a rejected; b accepted but blocked behind a; remediation merged
	// and accepted.
	if err := q.MarkRejected(a.MergedVersion, "r1", "won't compile", nil); err != nil {
		t.Fatal(err)
	}
	if err := q.MarkAccepted(b.MergedVersion, "r2"); err != nil {
		t.Fatal(err)
	}
	if err := q.MarkAccepted(remediation.MergedVersion, "r3"); err != nil {
		t.Fatal(err)
	}

	// Architect declares remediation supersedes a.
	if err := q.MarkSuperseded(a.MergedVersion, remediation.MergedVersion); err != nil {
		t.Fatal(err)
	}

	// Advance pops the superseded head.
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

	// Now head should be b (accepted). Mark it committed.
	head := q.Head()
	if head == nil || head.Descriptor.PipelineID != "b" {
		t.Fatalf("head after supersession pop = %v, want b", head)
	}
}

// TestCommitQueue_Abandon verifies abandon-path terminal semantics.
func TestCommitQueue_Abandon(t *testing.T) {
	q := NewCommitQueue()
	d := MergeDescriptor{PipelineID: "d", MergedVersion: SemanticVersion{Minor: 1}}
	q.Enqueue(d)

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

// TestCommitQueue_LookupFindsEnqueued verifies that Lookup returns the
// enqueued entry by its MergedVersion, and nil for unknown versions.
func TestCommitQueue_LookupFindsEnqueued(t *testing.T) {
	q := NewCommitQueue()
	d := MergeDescriptor{PipelineID: "x", MergedVersion: SemanticVersion{Minor: 7}}
	q.Enqueue(d)
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

// TestCommitQueue_DoubleEnqueueIsIdempotent verifies enqueue is
// idempotent on the same MergedVersion — important for retry semantics.
func TestCommitQueue_DoubleEnqueueIsIdempotent(t *testing.T) {
	q := NewCommitQueue()
	d := MergeDescriptor{PipelineID: "x", MergedVersion: SemanticVersion{Minor: 1}}
	a := q.Enqueue(d)
	b := q.Enqueue(d)
	if a != b {
		t.Fatal("Enqueue returned different entries for same MergedVersion")
	}
	if q.Depth() != 1 {
		t.Fatalf("depth = %d, want 1", q.Depth())
	}
}

// TestCommitQueue_InvalidTransitions verifies guard errors on illegal
// state transitions.
func TestCommitQueue_InvalidTransitions(t *testing.T) {
	q := NewCommitQueue()
	d := MergeDescriptor{PipelineID: "x", MergedVersion: SemanticVersion{Minor: 1}}
	q.Enqueue(d)

	// Can't commit an auditing entry.
	if err := q.MarkCommitted(d.MergedVersion); err == nil {
		t.Fatal("MarkCommitted on auditing: expected error")
	}
	// Can't supersede an auditing entry.
	other := SemanticVersion{Minor: 2}
	if err := q.MarkSuperseded(d.MergedVersion, other); err == nil {
		t.Fatal("MarkSuperseded on auditing: expected error")
	}

	// Mark as accepted, then try to reject (illegal).
	if err := q.MarkAccepted(d.MergedVersion, "r"); err != nil {
		t.Fatal(err)
	}
	if err := q.MarkRejected(d.MergedVersion, "r", "", nil); err == nil {
		t.Fatal("MarkRejected on accepted: expected error")
	}
	// Commit it.
	if err := q.MarkCommitted(d.MergedVersion); err != nil {
		t.Fatal(err)
	}
	// Can't abandon a committed entry.
	if err := q.Abandon(d.MergedVersion, ""); err == nil {
		t.Fatal("Abandon on committed: expected error")
	}
}
