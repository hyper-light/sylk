package versioning

import (
	"testing"
	"time"
)

// TestSessionClock_AccumulatesOnlyWhenOpen verifies the clock
// advances during open intervals and freezes during closed
// intervals. docs/PARALLEL_GLOBAL_VFS.md §8.
func TestSessionClock_AccumulatesOnlyWhenOpen(t *testing.T) {
	c := NewSessionClock()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	// Open at t=0, closed at t=5s → accumulator = 5s.
	c.Start(base)
	c.Stop(base.Add(5 * time.Second))
	if got := c.Elapsed(base.Add(5 * time.Second)); got != 5*time.Second {
		t.Errorf("after first close, Elapsed = %v, want 5s", got)
	}

	// Closed for 100s. Clock should not advance.
	if got := c.Elapsed(base.Add(105 * time.Second)); got != 5*time.Second {
		t.Errorf("while closed, Elapsed = %v, want still 5s", got)
	}

	// Reopen at t=105s. At t=110s, accumulator = 5s (old) + 5s (new) = 10s.
	c.Start(base.Add(105 * time.Second))
	if got := c.Elapsed(base.Add(110 * time.Second)); got != 10*time.Second {
		t.Errorf("after reopen + 5s, Elapsed = %v, want 10s", got)
	}
}

// TestSessionClock_MarkSinceHonorsClosure verifies that a Mark taken
// during an open interval and checked after a close + reopen cycle
// sees only the open-time delta, not the wall-clock delta.
func TestSessionClock_MarkSinceHonorsClosure(t *testing.T) {
	c := NewSessionClock()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c.Start(base)

	// Mark at t=2s (open-time = 2s).
	markPoint := base.Add(2 * time.Second)
	mk := c.MarkNow(markPoint)

	// Close at t=3s (open-time accumulator = 3s).
	c.Stop(base.Add(3 * time.Second))

	// 1000 seconds of wall-clock elapse while closed.
	// Reopen at t=1003s. At t=1005s, open-time = 3 (before) + 2 (after reopen) = 5s.
	// mk.Since should report 5s - 2s = 3s.
	c.Start(base.Add(1003 * time.Second))
	got := mk.Since(c, base.Add(1005*time.Second))
	if got != 3*time.Second {
		t.Errorf("Mark.Since = %v, want 3s (open-time only, not wall-clock)", got)
	}
}
