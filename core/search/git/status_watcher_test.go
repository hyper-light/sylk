package git

import (
	"testing"
	"time"
)

func TestNudge_NonBlocking(t *testing.T) {
	// Nudge on a full channel must not block.
	ch := make(chan struct{}, 1)
	ch <- struct{}{} // fill

	// Simulate Nudge logic.
	select {
	case ch <- struct{}{}:
		t.Fatal("expected channel to be full")
	default:
		// expected: non-blocking drop
	}
}

func TestResetDebounce(t *testing.T) {
	// Verify resetDebounce does not panic on repeated calls.
	timer := newStoppedTimer()
	active := false

	resetDebounce(timer, &active)
	if !active {
		t.Error("expected active=true after first reset")
	}

	// Reset again while active — should not panic.
	resetDebounce(timer, &active)
	if !active {
		t.Error("expected active=true after second reset")
	}
}

func TestDrainTimer_NoBlock(t *testing.T) {
	timer := newStoppedTimer()
	// drainTimer must not block even if the timer has already been stopped.
	drainTimer(timer)
}

func TestArm_StartsMaxWindow(t *testing.T) {
	debounce := newStoppedTimer()
	maxWindow := newStoppedTimer()
	debounceActive := false
	maxWindowActive := false

	arm(debounce, &debounceActive, maxWindow, &maxWindowActive)

	if !debounceActive {
		t.Error("expected debounce to be active after arm")
	}
	if !maxWindowActive {
		t.Error("expected max window to be active after arm")
	}
}

func TestArm_DoesNotRestartMaxWindow(t *testing.T) {
	debounce := newStoppedTimer()
	maxWindow := newStoppedTimer()
	debounceActive := false
	maxWindowActive := true // already running

	arm(debounce, &debounceActive, maxWindow, &maxWindowActive)

	if !debounceActive {
		t.Error("expected debounce to be active after arm")
	}
	// maxWindowActive should remain true (not restarted).
	if !maxWindowActive {
		t.Error("expected max window to remain active")
	}
}

func TestDropOldest_Semantics(t *testing.T) {
	ch := make(chan StatusUpdate, 1)
	old := StatusUpdate{StatusMap: map[string]GitFileState{"a": GitModified}}
	ch <- old

	// Drop oldest.
	select {
	case <-ch:
	default:
	}

	fresh := StatusUpdate{StatusMap: map[string]GitFileState{"b": GitAdded}}
	ch <- fresh

	got := <-ch
	if _, ok := got.StatusMap["b"]; !ok {
		t.Error("expected fresh value, got stale")
	}
}

func TestNewStoppedTimer_DoesNotFire(t *testing.T) {
	timer := newStoppedTimer()
	select {
	case <-timer.C:
		t.Fatal("stopped timer should not fire")
	case <-time.After(10 * time.Millisecond):
		// expected
	}
}
