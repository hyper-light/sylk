package quality

import (
	"testing"

	"github.com/adalundhe/sylk/core/concurrency"
)

// TestWeightStateProvider_RejectsNilManager locks the no-silent-defaults
// invariant: a provider without a manager has nothing to snapshot.
func TestWeightStateProvider_RejectsNilManager(t *testing.T) {
	if _, err := NewWeightStateProvider(nil); err == nil {
		t.Fatal("expected error for nil manager")
	}
}

// TestWeightStateProvider_SnapshotCapturesOverlayAndDirty verifies the
// snapshot path: after observing some signals, the snapshot reflects the
// overlay entries AND the dirty-set exactly.
func TestWeightStateProvider_SnapshotCapturesOverlayAndDirty(t *testing.T) {
	base := PriorQualityWeights(DomainCode)
	m, _ := NewSessionWeightManager("session-1", base)
	m.SetForkVersion(7)

	m.Observe(SignalObservation{Kind: SignalVector, Success: true, EffectiveWeight: 1}, DomainCode)
	m.Observe(SignalObservation{Kind: SignalStaleness, Success: false, EffectiveWeight: 1}, DomainCode)

	provider, _ := NewWeightStateProvider(m)
	snap := provider.Snapshot()

	if snap.SessionID != "session-1" {
		t.Errorf("SessionID = %q, want session-1", snap.SessionID)
	}
	if snap.ForkVersion != 7 {
		t.Errorf("ForkVersion = %v, want 7", snap.ForkVersion)
	}
	if len(snap.Overlay) != 2 {
		t.Errorf("Overlay entries = %d, want 2", len(snap.Overlay))
	}
	if len(snap.DirtyKeys) != 2 {
		t.Errorf("DirtyKeys = %d, want 2", len(snap.DirtyKeys))
	}
}

// TestWeightStateProvider_CollectStateWritesExtras verifies the
// CheckpointStateProvider contract: CollectState writes the snapshot
// into cp.Extras under the expected key.
func TestWeightStateProvider_CollectStateWritesExtras(t *testing.T) {
	base := PriorQualityWeights(DomainCode)
	m, _ := NewSessionWeightManager("session-1", base)
	m.Observe(SignalObservation{Kind: SignalVector, Success: true, EffectiveWeight: 1}, DomainCode)

	provider, _ := NewWeightStateProvider(m)
	cp := concurrency.NewCheckpoint("cp-1", 0)
	if err := provider.CollectState(cp); err != nil {
		t.Fatalf("CollectState: %v", err)
	}

	var restored WeightStateSnapshot
	ok, err := cp.GetExtra(weightStateExtrasKey, &restored)
	if err != nil {
		t.Fatalf("GetExtra: %v", err)
	}
	if !ok {
		t.Fatal("checkpoint missing quality.weight_state.v1 key")
	}
	if restored.SessionID != "session-1" {
		t.Errorf("restored SessionID = %q, want session-1", restored.SessionID)
	}
	if len(restored.Overlay) != 1 {
		t.Errorf("restored Overlay entries = %d, want 1", len(restored.Overlay))
	}
}

// TestRestoreFromCheckpoint_RebuildsSessionManager is the SCORE-03 end-to-
// end invariant: snapshot → checkpoint.Extras → RestoreFromCheckpoint
// produces a SessionWeightManager whose reads match the pre-snapshot
// original exactly.
func TestRestoreFromCheckpoint_RebuildsSessionManager(t *testing.T) {
	base := PriorQualityWeights(DomainCode)
	original, _ := NewSessionWeightManager("session-42", base)
	original.SetForkVersion(13)
	for i := 0; i < 4; i++ {
		original.Observe(SignalObservation{Kind: SignalBleve, Success: true, EffectiveWeight: 1}, DomainCode)
	}
	original.Observe(SignalObservation{Kind: SignalStaleness, Success: false, EffectiveWeight: 1}, DomainCode)

	provider, _ := NewWeightStateProvider(original)
	cp := concurrency.NewCheckpoint("cp-1", 0)
	if err := provider.CollectState(cp); err != nil {
		t.Fatalf("CollectState: %v", err)
	}

	restored, err := RestoreFromCheckpoint(cp)
	if err != nil {
		t.Fatalf("RestoreFromCheckpoint: %v", err)
	}
	if restored == nil {
		t.Fatal("RestoreFromCheckpoint returned nil manager")
	}

	if restored.SessionID() != "session-42" {
		t.Errorf("restored SessionID = %q, want session-42", restored.SessionID())
	}
	if restored.ForkVersion() != 13 {
		t.Errorf("restored ForkVersion = %v, want 13", restored.ForkVersion())
	}
	wantBleve := original.WeightFor(DomainCode, SignalBleve)
	gotBleve := restored.WeightFor(DomainCode, SignalBleve)
	if gotBleve.Alpha != wantBleve.Alpha || gotBleve.Beta != wantBleve.Beta {
		t.Errorf("Bleve restore mismatch: got α=%v β=%v, want α=%v β=%v",
			gotBleve.Alpha, gotBleve.Beta, wantBleve.Alpha, wantBleve.Beta)
	}
	// Dirty set must be preserved so MergeToProject still has work to do.
	if len(restored.DirtyKeys()) != 2 {
		t.Errorf("restored DirtyKeys = %d, want 2 (Bleve + Staleness)", len(restored.DirtyKeys()))
	}
}

// TestRestoreFromCheckpoint_NoExtrasReturnsNil verifies back-compat with
// pre-SCORE-03 checkpoints: a checkpoint without the weight-state key
// returns (nil, nil), letting the caller fall back to priors-only init.
func TestRestoreFromCheckpoint_NoExtrasReturnsNil(t *testing.T) {
	cp := concurrency.NewCheckpoint("cp-legacy", 0)
	restored, err := RestoreFromCheckpoint(cp)
	if err != nil {
		t.Fatalf("RestoreFromCheckpoint on legacy checkpoint: %v", err)
	}
	if restored != nil {
		t.Error("legacy checkpoint with no weight state should return nil manager")
	}
}

// TestRestoreFromCheckpoint_RejectsUnknownDomain verifies that a
// checkpoint with an unparseable domain label fails loudly — silent
// drop would produce a restored session whose reads disagree with the
// pre-crash state.
func TestRestoreFromCheckpoint_RejectsUnknownDomain(t *testing.T) {
	snapshot := WeightStateSnapshot{
		SessionID: "session-corrupt",
		Base:      PriorQualityWeights(DomainCode),
		Overlay: []weightStateOverlayEntry{
			{Domain: "quantum", Signal: "vector", Weight: NewLearnedWeight(5, 5)},
		},
	}
	if _, err := RestoreFromSnapshot(snapshot); err == nil {
		t.Fatal("expected error for unknown domain in overlay")
	}
}

// TestCheckpointHash_IncludesExtras verifies extras participate in the
// content hash. Without this, a checkpoint loaded after a provider
// rewrote its extras would pass Validate() against a stale hash.
func TestCheckpointHash_IncludesExtras(t *testing.T) {
	cp1 := concurrency.NewCheckpoint("cp-1", 0)
	h1 := cp1.ComputeHash()

	cp2 := concurrency.NewCheckpoint("cp-2", 0)
	if err := cp2.SetExtra("test.key", "value"); err != nil {
		t.Fatalf("SetExtra: %v", err)
	}
	h2 := cp2.ComputeHash()

	if h1 == h2 {
		t.Error("extras must participate in ComputeHash — checkpoints with different extras should hash differently")
	}
}
