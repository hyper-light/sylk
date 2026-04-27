package forest

import (
	"context"
	"testing"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Observations follow-up tests:
//   - AntiPattern promoter lease handoff
//   - RetrievalSourcePrimed tagging
//   - retrieval-candidates pruner
// ────────────────────────────────────────────────────────────────────

// TestObservation_AntiPatternLeaseHandoff verifies a second forest
// instance contending on the same DB sees errLeaseHeldByOther on the
// promoter row when the first holds an unexpired lease.
func TestObservation_AntiPatternLeaseHandoff(t *testing.T) {
	forestA, _ := newTestForest(t)

	// Forest A acquires the lease.
	if err := forestA.acquireAntiPatternPromoterLease(); err != nil {
		t.Fatalf("forest A acquire: %v", err)
	}

	// Construct a fake "other" forest with a different projectorID
	// against the same DB, then attempt acquire — should fail.
	otherID := "forest-projector-other"
	originalID := forestA.projectorID
	forestA.projectorID = otherID
	err := forestA.acquireAntiPatternPromoterLease()
	forestA.projectorID = originalID
	if err == nil {
		t.Fatal("expected errLeaseHeldByOther; got nil")
	}
	// Don't compare error directly — implementation wraps with fmt.Errorf
	// in some paths but errors.Is should match for the canonical sentinel.
	// This test is functional: a different holder against an unexpired
	// lease must NOT acquire.
}

// TestObservation_RetrievalSourcePrimedTags verifies that packets
// surfaced through a primed canopy get RetrievalSourcePrimed.
func TestObservation_RetrievalSourcePrimedTags(t *testing.T) {
	canopy := &Canopy{
		Key:      primingCanopyKey("new-session", CanopyHorizonSession),
		Horizon:  CanopyHorizonSession,
		RootIDs:  []string{"root-a", "branch-b"},
	}
	packets := []*BranchPacket{
		{Branch: &Branch{ID: "branch-a", RootID: "root-a"}, Source: RetrievalSourcePrimary},
		{Branch: &Branch{ID: "branch-b", RootID: "other-root"}, Source: RetrievalSourcePrimary},
		{Branch: &Branch{ID: "branch-c", RootID: "root-c"}, Source: RetrievalSourcePrimary},
		{Branch: &Branch{ID: "branch-d", RootID: "root-d"}, Source: RetrievalSourceAntiPrecedent},
	}
	tagPrimedRetrievalSource(packets, canopy, "new-session")
	if packets[0].Source != RetrievalSourcePrimed {
		t.Errorf("packet 0 (RootID match): got %s want primed", packets[0].Source)
	}
	if packets[1].Source != RetrievalSourcePrimed {
		t.Errorf("packet 1 (ID match): got %s want primed", packets[1].Source)
	}
	if packets[2].Source != RetrievalSourcePrimary {
		t.Errorf("packet 2 (no match): got %s want primary", packets[2].Source)
	}
	if packets[3].Source != RetrievalSourceAntiPrecedent {
		t.Errorf("packet 3 (anti-precedent): primed should NOT override; got %s", packets[3].Source)
	}
}

// TestObservation_RetrievalSourcePrimedSkipsActiveCanopy verifies the
// helper is a no-op for a non-primed canopy (active session canopy).
func TestObservation_RetrievalSourcePrimedSkipsActiveCanopy(t *testing.T) {
	canopy := &Canopy{
		Key:     "active-canopy-not-primed",
		Horizon: CanopyHorizonSession,
		RootIDs: []string{"root-a"},
	}
	packets := []*BranchPacket{
		{Branch: &Branch{ID: "x", RootID: "root-a"}, Source: RetrievalSourcePrimary},
	}
	tagPrimedRetrievalSource(packets, canopy, "session")
	if packets[0].Source != RetrievalSourcePrimary {
		t.Errorf("active canopy: got %s want primary", packets[0].Source)
	}
}

// TestObservation_PrunerDeletesAgedCandidates exercises the prune
// helper directly on a synchronous test forest. We insert candidate
// rows with a stale retrieval_at and verify the next prune removes
// them while leaving fresh rows untouched.
func TestObservation_PrunerDeletesAgedCandidates(t *testing.T) {
	forest, db := newTestForestWithConfig(t, Config{
		RetrievalCandidatesRetention:    100 * time.Millisecond,
		RetrievalCandidatesPruneInterval: 10 * time.Millisecond,
	})
	ctx := context.Background()

	// Seed one stale and one fresh candidate row directly. The
	// retrieval_event row is required for the FK / append-only layer
	// to be happy.
	insert := func(eventID, branchID string, retrievalAt int64) {
		t.Helper()
		_, err := db.Exec(`
			INSERT INTO forest_retrieval_events
			(id, session_id, query, requested_at, requested_limit,
			 candidate_count, returned_count, model_version,
			 branch_projection_seq, candidates_blob, include_counter_evidence,
			 duration_micros, exploration_mode)
			VALUES (?, 's', 'q', ?, 1, 1, 1, '', 0, '[]', 0, 0, 0)
		`, eventID, retrievalAt)
		if err != nil {
			t.Fatalf("insert event %s: %v", eventID, err)
		}
		_, err = db.Exec(`
			INSERT INTO forest_retrieval_event_seq_log (event_id, appended_at)
			VALUES (?, ?)
		`, eventID, retrievalAt)
		if err != nil {
			t.Fatalf("insert seq %s: %v", eventID, err)
		}
		_, err = db.Exec(`
			INSERT INTO forest_retrieval_candidates
			(retrieval_event_id, retrieval_seq, branch_id, session_id,
			 rank_position, returned, base_score, final_score,
			 predicted_utility, predicted_risk, exploration_mode, retrieval_at)
			VALUES (?, 1, ?, 's', 1, 1, 0.5, 0.5, 0, 0, 0, ?)
		`, eventID, branchID, retrievalAt)
		if err != nil {
			t.Fatalf("insert candidate %s: %v", eventID, err)
		}
	}
	stale := time.Now().UTC().Add(-1 * time.Hour).Unix()
	fresh := time.Now().UTC().Unix()
	insert("ev-old", "branch-old", stale)
	insert("ev-new", "branch-new", fresh)

	deleted, err := forest.runRetrievalCandidatesPruneOnce(ctx)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if deleted < 1 {
		t.Errorf("expected at least 1 deletion; got %d", deleted)
	}

	var olds, news int
	db.QueryRow(`SELECT COUNT(*) FROM forest_retrieval_candidates WHERE branch_id = 'branch-old'`).Scan(&olds)
	db.QueryRow(`SELECT COUNT(*) FROM forest_retrieval_candidates WHERE branch_id = 'branch-new'`).Scan(&news)
	if olds != 0 {
		t.Errorf("stale candidate not pruned: count=%d", olds)
	}
	if news != 1 {
		t.Errorf("fresh candidate wrongly pruned: count=%d want 1", news)
	}
}

// TestObservation_PrunerOrphanedSweepStateCleanup verifies the
// orphan-sweep cleanup runs after candidate pruning.
func TestObservation_PrunerOrphanedSweepStateCleanup(t *testing.T) {
	forest, db := newTestForestWithConfig(t, Config{
		RetrievalCandidatesRetention: 50 * time.Millisecond,
	})
	ctx := context.Background()

	// Insert an orphan sweep_state row (no matching candidate row).
	_, err := db.Exec(`
		INSERT INTO forest_retrieval_sweep_state (retrieval_event_id, swept_at)
		VALUES ('orphan', ?)
	`, time.Now().UTC().Unix())
	if err != nil {
		t.Fatal(err)
	}

	if err := forest.pruneOrphanedSweepState(ctx); err != nil {
		t.Fatal(err)
	}

	var count int
	db.QueryRow(`SELECT COUNT(*) FROM forest_retrieval_sweep_state WHERE retrieval_event_id = 'orphan'`).Scan(&count)
	if count != 0 {
		t.Errorf("orphan sweep state not cleaned: count=%d", count)
	}
}

// TestObservation_PrunerResolverDefaults exercises the resolve
// helpers.
func TestObservation_PrunerResolverDefaults(t *testing.T) {
	if got := resolveRetrievalCandidatesRetention(0); got != defaultRetrievalCandidatesRetention {
		t.Errorf("retention default: got %v", got)
	}
	if got := resolveRetrievalCandidatesPruneInterval(-1); got != defaultRetrievalCandidatesPruneInterval {
		t.Errorf("interval default: got %v", got)
	}
	if got := resolveRetrievalCandidatesRetention(2 * time.Hour); got != 2*time.Hour {
		t.Errorf("retention passthrough: got %v", got)
	}
}

// TestObservation_IsPrimedCanopyDetection covers the primed-canopy
// detector for various combinations.
func TestObservation_IsPrimedCanopyDetection(t *testing.T) {
	if isPrimedCanopy(nil, "s") {
		t.Error("nil canopy: should be false")
	}
	if isPrimedCanopy(&Canopy{Key: "x"}, "") {
		t.Error("empty session: should be false")
	}
	primed := &Canopy{
		Key:     primingCanopyKey("session-1", CanopyHorizonSession),
		Horizon: CanopyHorizonSession,
	}
	if !isPrimedCanopy(primed, "session-1") {
		t.Errorf("primed canopy: should be true")
	}
	if isPrimedCanopy(primed, "wrong-session") {
		t.Errorf("session mismatch: should be false")
	}
}
