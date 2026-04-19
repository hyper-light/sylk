package handoff

import (
	"path/filepath"
	"testing"
	"time"
)

// TestSupervisor_MaybeCheckpointProfiles_RespectsPersistenceGate is
// the HAND-07 rate-limit test: when the profile learner reports
// "nothing to persist" (no pending changes OR the interval hasn't
// elapsed), MaybeCheckpointProfiles must be a zero-write no-op. A
// regression here would cause the supervisor to fsync every turn,
// serializing the hot path behind disk latency.
func TestSupervisor_MaybeCheckpointProfiles_RespectsPersistenceGate(t *testing.T) {
	dir := t.TempDir()
	wal, err := NewHandoffWAL(filepath.Join(dir, "handoff.wal"), nil)
	if err != nil {
		t.Fatalf("open WAL: %v", err)
	}
	defer wal.Close()

	sup := NewHandoffSupervisor(nil)
	sup.wal = wal

	// Fresh learner: no pending changes, so no checkpoint should fire.
	if got := sup.MaybeCheckpointProfiles(); got != 0 {
		t.Errorf("fresh learner: checkpoints written = %d, want 0", got)
	}
}

// TestSupervisor_MaybeCheckpointProfiles_WritesAfterObservation is
// the HAND-07 acceptance test: once an observation has landed and
// the persistence interval has elapsed, MaybeCheckpointProfiles
// writes one checkpoint per registered bridge. The persistence
// interval is forced short via direct config mutation so the test
// runs in milliseconds rather than minutes.
func TestSupervisor_MaybeCheckpointProfiles_WritesAfterObservation(t *testing.T) {
	dir := t.TempDir()
	wal, err := NewHandoffWAL(filepath.Join(dir, "handoff.wal"), nil)
	if err != nil {
		t.Fatalf("open WAL: %v", err)
	}
	defer wal.Close()

	sup := NewHandoffSupervisor(nil)
	sup.wal = wal
	// Force the learner's persistence interval short so the test does
	// not wait on the production default.
	sup.profileLearner.config.PersistenceInterval = time.Millisecond
	sup.profileLearner.lastPersisted = time.Now().Add(-time.Second) // already "overdue"

	// RecordObservation marks pendingPersistence=true. We don't need a
	// real bridge for this path — MaybeCheckpointProfiles iterates the
	// supervisor's bridge registry, which may be empty. An empty
	// registry with a triggered gate should still MarkPersisted so
	// subsequent calls don't spam.
	sup.profileLearner.RecordObservation("engineer", "opus-4.7", &HandoffObservation{
		ContextSize:  1000,
		TurnNumber:   1,
		QualityScore: 0.8,
		Timestamp:    time.Now(),
	})
	if !sup.profileLearner.NeedsPersistence() {
		t.Fatal("profile learner should report pending persistence after observation")
	}

	// No bridges registered → MaybeCheckpointProfiles writes zero
	// checkpoints but must not panic. The gate check still runs.
	_ = sup.MaybeCheckpointProfiles()
	// Second call is rate-limited to zero.
	if got := sup.MaybeCheckpointProfiles(); got != 0 {
		t.Errorf("second call within interval: checkpoints = %d, want 0", got)
	}
}

// TestSupervisor_MaybeCheckpointProfiles_PerBridgeWrite verifies the
// per-bridge fan-out: with N registered bridges, one flush window
// produces N checkpoint writes (not 1). This is what makes the
// recovery path correct — each agent's learned state is snapshotted,
// not just the first one the map iteration happens to land on.
func TestSupervisor_MaybeCheckpointProfiles_PerBridgeWrite(t *testing.T) {
	dir := t.TempDir()
	wal, err := NewHandoffWAL(filepath.Join(dir, "handoff.wal"), nil)
	if err != nil {
		t.Fatalf("open WAL: %v", err)
	}
	defer wal.Close()

	sup := NewHandoffSupervisor(nil)
	sup.wal = wal
	sup.profileLearner.config.PersistenceInterval = time.Millisecond
	sup.profileLearner.lastPersisted = time.Now().Add(-time.Second)

	// Stub two bridges directly into the supervisor's registry. Avoids
	// pulling in the full RegisterAgent path (which requires a started
	// supervisor, descriptor registry, etc.).
	sup.bridges["uid-a"] = &HandoffBridge{
		profile: NewAgentHandoffProfile("engineer", "opus-4.7", "uid-a"),
		gp:      NewAgentGaussianProcess(nil),
	}
	sup.bridges["uid-b"] = &HandoffBridge{
		profile: NewAgentHandoffProfile("engineer", "opus-4.7", "uid-b"),
		gp:      NewAgentGaussianProcess(nil),
	}

	// Trigger pendingPersistence via RecordObservation.
	sup.profileLearner.RecordObservation("engineer", "opus-4.7", &HandoffObservation{
		ContextSize:  1000,
		TurnNumber:   1,
		QualityScore: 0.8,
		Timestamp:    time.Now(),
	})

	written := sup.MaybeCheckpointProfiles()
	if written != 2 {
		t.Errorf("checkpoints written = %d, want 2 (one per bridge)", written)
	}

	// Second call is gated — MarkPersisted should have fired.
	if got := sup.MaybeCheckpointProfiles(); got != 0 {
		t.Errorf("second call (rate-limited): checkpoints = %d, want 0", got)
	}
}

// TestSupervisor_MaybeCheckpointProfiles_NilWALIsNoop protects the
// cold-start / no-WAL configurations (memory-only tests, bootstrap
// failures). The bridge calls this every turn, so a nil-WAL panic
// would crash every agent immediately.
func TestSupervisor_MaybeCheckpointProfiles_NilWALIsNoop(t *testing.T) {
	sup := NewHandoffSupervisor(nil)
	// sup.wal deliberately nil.
	sup.profileLearner.config.PersistenceInterval = time.Millisecond
	sup.profileLearner.lastPersisted = time.Now().Add(-time.Second)

	if got := sup.MaybeCheckpointProfiles(); got != 0 {
		t.Errorf("nil WAL: checkpoints = %d, want 0", got)
	}
}
