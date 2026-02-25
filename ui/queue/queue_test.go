package queue

import (
	"fmt"
	"testing"
)

func TestEnqueueAndPeek(t *testing.T) {
	q := New(MaxCapacity)

	e, ok := q.Enqueue("1", "hello", "", "s1")
	if !ok {
		t.Fatal("enqueue should succeed")
	}
	if e.ID != "1" || e.Text != "hello" || e.State != StatePending {
		t.Fatalf("unexpected entry: %+v", e)
	}
	if q.Len() != 1 || q.PendingCount() != 1 {
		t.Fatal("length mismatch")
	}

	peeked, ok := q.Peek()
	if !ok || peeked.ID != "1" {
		t.Fatal("peek should return head entry")
	}
	if q.Len() != 1 {
		t.Fatal("peek should not remove entry")
	}
}

func TestEnqueueAtCapacity(t *testing.T) {
	q := New(2)
	q.Enqueue("1", "a", "", "s1")
	q.Enqueue("2", "b", "", "s1")

	_, ok := q.Enqueue("3", "c", "", "s1")
	if ok {
		t.Fatal("enqueue should fail at capacity")
	}
	if q.Len() != 2 {
		t.Fatalf("expected 2 entries, got %d", q.Len())
	}
}

func TestAdvanceDrainsTerminal(t *testing.T) {
	q := New(MaxCapacity)
	q.Enqueue("1", "first", "", "s1")
	q.Enqueue("2", "second", "", "s1")

	// Mark first as completed.
	q.MarkDispatching("1")
	q.MarkActive("1", "corr-1")
	q.MarkCompleted("1")

	next, ok := q.Advance()
	if !ok || next.ID != "2" {
		t.Fatalf("advance should return second entry, got %+v, ok=%v", next, ok)
	}
}

func TestAdvanceEmptyQueue(t *testing.T) {
	q := New(MaxCapacity)
	_, ok := q.Advance()
	if ok {
		t.Fatal("advance on empty should return false")
	}
}

func TestAdvanceNonTerminalHead(t *testing.T) {
	q := New(MaxCapacity)
	q.Enqueue("1", "first", "", "s1")
	q.MarkDispatching("1")

	_, ok := q.Advance()
	if ok {
		t.Fatal("advance should fail when head is non-terminal and non-pending")
	}
}

func TestCancelPending(t *testing.T) {
	q := New(MaxCapacity)
	q.Enqueue("1", "a", "", "s1")
	q.Enqueue("2", "b", "", "s1")

	if !q.Cancel("1") {
		t.Fatal("cancel should succeed for pending entry")
	}
	if q.Len() != 1 {
		t.Fatalf("expected 1 entry after cancel, got %d", q.Len())
	}
	peeked, _ := q.Peek()
	if peeked.ID != "2" {
		t.Fatal("remaining entry should be '2'")
	}
}

func TestCancelNonPending(t *testing.T) {
	q := New(MaxCapacity)
	q.Enqueue("1", "a", "", "s1")
	q.MarkDispatching("1")

	if q.Cancel("1") {
		t.Fatal("cancel should fail for non-pending entry")
	}
}

func TestCancelAll(t *testing.T) {
	q := New(MaxCapacity)
	q.Enqueue("1", "a", "", "s1")
	q.Enqueue("2", "b", "", "s1")
	q.Enqueue("3", "c", "", "s1")

	// Mark one as active so it's not pending.
	q.MarkDispatching("1")
	q.MarkActive("1", "corr")

	n := q.CancelAll()
	if n != 2 {
		t.Fatalf("expected 2 cancelled, got %d", n)
	}
	// Active entry should remain.
	if q.Len() != 1 {
		t.Fatalf("expected 1 entry remaining, got %d", q.Len())
	}
}

func TestTogglePause(t *testing.T) {
	q := New(MaxCapacity)
	if q.IsPaused() {
		t.Fatal("should start unpaused")
	}
	paused := q.TogglePause()
	if !paused || !q.IsPaused() {
		t.Fatal("should be paused after toggle")
	}
	paused = q.TogglePause()
	if paused || q.IsPaused() {
		t.Fatal("should be unpaused after second toggle")
	}
}

func TestReorderForward(t *testing.T) {
	q := New(MaxCapacity)
	q.Enqueue("1", "a", "", "s1")
	q.Enqueue("2", "b", "", "s1")
	q.Enqueue("3", "c", "", "s1")

	q.Reorder("1", 1) // Move "1" one position toward tail.
	entries := q.PendingEntries()
	if len(entries) != 3 {
		t.Fatalf("expected 3 pending, got %d", len(entries))
	}
	if entries[0].ID != "2" || entries[1].ID != "1" || entries[2].ID != "3" {
		t.Fatalf("unexpected order: %s, %s, %s", entries[0].ID, entries[1].ID, entries[2].ID)
	}
}

func TestReorderBackward(t *testing.T) {
	q := New(MaxCapacity)
	q.Enqueue("1", "a", "", "s1")
	q.Enqueue("2", "b", "", "s1")
	q.Enqueue("3", "c", "", "s1")

	q.Reorder("3", -2) // Move "3" two positions toward head.
	entries := q.PendingEntries()
	if entries[0].ID != "3" || entries[1].ID != "1" || entries[2].ID != "2" {
		t.Fatalf("unexpected order: %s, %s, %s", entries[0].ID, entries[1].ID, entries[2].ID)
	}
}

func TestReorderClampsBounds(t *testing.T) {
	q := New(MaxCapacity)
	q.Enqueue("1", "a", "", "s1")
	q.Enqueue("2", "b", "", "s1")

	q.Reorder("1", 100) // Clamp to end.
	entries := q.PendingEntries()
	if entries[0].ID != "2" || entries[1].ID != "1" {
		t.Fatalf("unexpected order: %s, %s", entries[0].ID, entries[1].ID)
	}
}

func TestStateTransitions(t *testing.T) {
	q := New(MaxCapacity)
	q.Enqueue("1", "a", "", "s1")

	q.MarkDispatching("1")
	e := q.entries[0]
	if e.State != StateDispatching || e.DispatchedAt.IsZero() {
		t.Fatalf("expected dispatching, got %v", e.State)
	}

	q.MarkActive("1", "corr-1")
	e = q.entries[0]
	if e.State != StateActive || e.CorrelationID != "corr-1" {
		t.Fatalf("expected active with corr-1, got %v/%s", e.State, e.CorrelationID)
	}

	q.MarkCompleted("1")
	e = q.entries[0]
	if e.State != StateCompleted {
		t.Fatalf("expected completed, got %v", e.State)
	}
}

func TestMarkFailedFromActive(t *testing.T) {
	q := New(MaxCapacity)
	q.Enqueue("1", "a", "", "s1")
	q.MarkDispatching("1")
	q.MarkActive("1", "corr")
	q.MarkFailed("1")

	e := q.entries[0]
	if e.State != StateFailed {
		t.Fatalf("expected failed, got %v", e.State)
	}
}

func TestMarkFailedFromDispatching(t *testing.T) {
	q := New(MaxCapacity)
	q.Enqueue("1", "a", "", "s1")
	q.MarkDispatching("1")
	q.MarkFailed("1")

	e := q.entries[0]
	if e.State != StateFailed {
		t.Fatalf("expected failed, got %v", e.State)
	}
}

func TestPendingEntries(t *testing.T) {
	q := New(MaxCapacity)
	q.Enqueue("1", "a", "", "s1")
	q.Enqueue("2", "b", "", "s1")
	q.Enqueue("3", "c", "", "s1")
	q.MarkDispatching("2")

	pending := q.PendingEntries()
	if len(pending) != 2 {
		t.Fatalf("expected 2 pending, got %d", len(pending))
	}
	if pending[0].ID != "1" || pending[1].ID != "3" {
		t.Fatalf("unexpected IDs: %s, %s", pending[0].ID, pending[1].ID)
	}
}

func TestActiveEntry(t *testing.T) {
	q := New(MaxCapacity)
	q.Enqueue("1", "a", "", "s1")
	q.Enqueue("2", "b", "", "s1")

	_, ok := q.ActiveEntry()
	if ok {
		t.Fatal("no active entry expected")
	}

	q.MarkDispatching("1")
	q.MarkActive("1", "corr")

	active, ok := q.ActiveEntry()
	if !ok || active.ID != "1" {
		t.Fatal("expected active entry 1")
	}
}

func TestIsEmpty(t *testing.T) {
	q := New(MaxCapacity)
	if !q.IsEmpty() {
		t.Fatal("expected empty")
	}
	q.Enqueue("1", "a", "", "s1")
	if q.IsEmpty() {
		t.Fatal("expected non-empty")
	}
}

func TestCapacityClamp(t *testing.T) {
	q := New(0) // Clamped to 1.
	_, ok := q.Enqueue("1", "a", "", "s1")
	if !ok {
		t.Fatal("capacity should be clamped to 1")
	}
	_, ok = q.Enqueue("2", "b", "", "s1")
	if ok {
		t.Fatal("should be at capacity")
	}

	q2 := New(100) // Clamped to MaxCapacity.
	for i := range MaxCapacity {
		q2.Enqueue(fmt.Sprintf("%d", i), "x", "", "s1")
	}
	_, ok = q2.Enqueue("overflow", "y", "", "s1")
	if ok {
		t.Fatal("should be at MaxCapacity")
	}
}

func TestSetPaused(t *testing.T) {
	q := New(MaxCapacity)
	q.SetPaused(true)
	if !q.IsPaused() {
		t.Fatal("expected paused")
	}
	q.SetPaused(false)
	if q.IsPaused() {
		t.Fatal("expected unpaused")
	}
}

func TestReorderSingleEntry(t *testing.T) {
	q := New(MaxCapacity)
	q.Enqueue("1", "a", "", "s1")
	q.Reorder("1", 1) // No-op with single entry.
	if q.PendingEntries()[0].ID != "1" {
		t.Fatal("single entry should be unchanged")
	}
}

func TestReorderNonExistentID(t *testing.T) {
	q := New(MaxCapacity)
	q.Enqueue("1", "a", "", "s1")
	q.Enqueue("2", "b", "", "s1")
	q.Reorder("999", 1) // No-op.
	entries := q.PendingEntries()
	if entries[0].ID != "1" || entries[1].ID != "2" {
		t.Fatal("order should be unchanged")
	}
}

func TestCompactAfterCancelAll(t *testing.T) {
	q := New(MaxCapacity)
	for i := range 5 {
		q.Enqueue(fmt.Sprintf("%d", i), "x", "", "s1")
	}
	q.CancelAll()
	if !q.IsEmpty() {
		t.Fatalf("expected empty after cancel all, got %d", q.Len())
	}
}

func TestEntryStateIsTerminal(t *testing.T) {
	terminal := []EntryState{StateCompleted, StateCancelled, StateFailed}
	for _, s := range terminal {
		if !s.IsTerminal() {
			t.Fatalf("%v should be terminal", s)
		}
	}
	nonTerminal := []EntryState{StatePending, StateDispatching, StateActive}
	for _, s := range nonTerminal {
		if s.IsTerminal() {
			t.Fatalf("%v should not be terminal", s)
		}
	}
}

func TestEntryStateString(t *testing.T) {
	states := map[EntryState]string{
		StatePending:     "pending",
		StateDispatching: "dispatching",
		StateActive:      "active",
		StateCompleted:   "completed",
		StateCancelled:   "cancelled",
		StateFailed:      "failed",
		EntryState(99):   "unknown",
	}
	for s, want := range states {
		if got := s.String(); got != want {
			t.Errorf("State(%d).String() = %q, want %q", s, got, want)
		}
	}
}
