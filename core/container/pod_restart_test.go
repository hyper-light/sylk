package container

import (
	"testing"
	"time"
)

// TestPodRestartCoordinator_TripsAtThreshold pins the basic semantics:
// N back-to-back failures in the same window triggers the demote
// signal exactly once they cross the threshold.
func TestPodRestartCoordinator_TripsAtThreshold(t *testing.T) {
	c := NewPodRestartCoordinator(PodRestartCoordinatorConfig{
		Threshold: 3,
		Window:    time.Second,
	})

	if c.RecordRestart("engineer") {
		t.Fatal("first failure should not trip")
	}
	if c.RecordRestart("designer") {
		t.Fatal("second failure should not trip")
	}
	if !c.RecordRestart("engineer") {
		t.Fatal("third failure should trip")
	}
}

// TestPodRestartCoordinator_StaleFailuresPruned ensures events outside
// the window don't count: a long-quiet pod that hiccups once doesn't
// inherit two-minute-old crash debt.
func TestPodRestartCoordinator_StaleFailuresPruned(t *testing.T) {
	c := NewPodRestartCoordinator(PodRestartCoordinatorConfig{
		Threshold: 3,
		Window:    50 * time.Millisecond,
	})

	c.RecordRestart("engineer")
	c.RecordRestart("designer")
	time.Sleep(80 * time.Millisecond)
	if c.RecordRestart("engineer") {
		t.Fatal("stale failures should have been pruned; one new failure in window must not trip")
	}
}

// TestPodRestartCoordinator_ResetClearsHistory verifies Reset enables
// a clean-slate restart after the architect has remediated.
func TestPodRestartCoordinator_ResetClearsHistory(t *testing.T) {
	c := NewPodRestartCoordinator(PodRestartCoordinatorConfig{
		Threshold: 2,
		Window:    time.Second,
	})

	c.RecordRestart("engineer")
	if !c.RecordRestart("designer") {
		t.Fatal("expected trip at threshold 2")
	}
	c.Reset()
	if c.RecordRestart("engineer") {
		t.Fatal("post-reset first failure should not trip")
	}
}

// TestPodRestartCoordinator_RecentFailuresReportsBreakdown validates
// the per-container breakdown the architect needs to author a
// corrective action.
func TestPodRestartCoordinator_RecentFailuresReportsBreakdown(t *testing.T) {
	c := NewPodRestartCoordinator(PodRestartCoordinatorConfig{
		Threshold: 5,
		Window:    time.Second,
	})

	c.RecordRestart("engineer")
	c.RecordRestart("engineer")
	c.RecordRestart("designer")

	failures := c.RecentFailures()
	if len(failures) != 3 {
		t.Fatalf("recent failures = %d, want 3", len(failures))
	}
	counts := map[string]int{}
	for _, f := range failures {
		counts[f.ContainerID]++
	}
	if counts["engineer"] != 2 || counts["designer"] != 1 {
		t.Fatalf("breakdown = %v, want engineer=2 designer=1", counts)
	}
}
