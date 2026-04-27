package forest

import (
	"context"
	"testing"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Issue #4 — Diversity reranking + retrieval cooldown
// ────────────────────────────────────────────────────────────────────

// TestDiversity_ResolverDefaults exercises the resolve* helpers
// (clamping, defaulting). Pure functions — no DB.
func TestDiversity_ResolverDefaults(t *testing.T) {
	if got := resolveDiversityLambda(-0.5); got != defaultDiversityLambda {
		t.Errorf("negative lambda default: got %v want %v", got, defaultDiversityLambda)
	}
	if got := resolveDiversityLambda(2.0); got != 1.0 {
		t.Errorf("lambda > 1 clamp: got %v want 1.0", got)
	}
	if got := resolveDiversityLambda(0.5); got != 0.5 {
		t.Errorf("valid lambda passthrough: got %v want 0.5", got)
	}
	if got := resolveRetrievalCooldownWindow(0); got != defaultRetrievalCooldownWindow {
		t.Errorf("cooldown window default: got %v", got)
	}
	if got := resolveRetrievalCooldownPenalty(-1); got != defaultRetrievalCooldownPenalty {
		t.Errorf("cooldown penalty default: got %v", got)
	}
	if got := resolveRetrievalCooldownPenalty(1.5); got != 1.0 {
		t.Errorf("cooldown penalty clamp >1: got %v want 1.0", got)
	}
}

// TestDiversity_MMRPicksDiversePool verifies that with three highly
// similar candidates and one orthogonal candidate, MMR picks the
// orthogonal one in second position rather than the next-similar.
func TestDiversity_MMRPicksDiversePool(t *testing.T) {
	forest := &MemoryForest{diversityLambda: 0.5}

	clusterA := []float32{1, 0, 0, 0}
	clusterAcopy := []float32{0.95, 0.05, 0, 0}
	clusterAdup := []float32{0.9, 0.1, 0, 0}
	orthogonal := []float32{0, 1, 0, 0}

	pool := []*BranchPacket{
		{Branch: &Branch{ID: "a1"}, Score: PacketScore{Total: 0.9}},
		{Branch: &Branch{ID: "a2"}, Score: PacketScore{Total: 0.85}},
		{Branch: &Branch{ID: "b"}, Score: PacketScore{Total: 0.80}},
		{Branch: &Branch{ID: "a3"}, Score: PacketScore{Total: 0.75}},
	}
	features := map[string][]float32{
		"a1": clusterA,
		"a2": clusterAcopy,
		"b":  orthogonal,
		"a3": clusterAdup,
	}
	out := forest.applyMMRReranking(pool, features, 2)
	if len(out) < 2 {
		t.Fatalf("expected >= 2 packets, got %d", len(out))
	}
	if out[0].Branch.ID != "a1" {
		t.Errorf("first pick: got %s want a1 (highest relevance)", out[0].Branch.ID)
	}
	if out[1].Branch.ID != "b" {
		t.Errorf("second pick: got %s want b (orthogonal); MMR didn't diversify", out[1].Branch.ID)
	}
}

// TestDiversity_MMRLambdaOneIsTopK verifies that lambda=1.0
// degenerates to "always pick highest relevance" — i.e., diversity is
// disabled.
func TestDiversity_MMRLambdaOneIsTopK(t *testing.T) {
	forest := &MemoryForest{diversityLambda: 1.0}
	pool := []*BranchPacket{
		{Branch: &Branch{ID: "a1"}, Score: PacketScore{Total: 0.9}},
		{Branch: &Branch{ID: "a2"}, Score: PacketScore{Total: 0.8}},
		{Branch: &Branch{ID: "b"}, Score: PacketScore{Total: 0.7}},
	}
	features := map[string][]float32{
		"a1": {1, 0, 0},
		"a2": {0.99, 0.01, 0},
		"b":  {0, 1, 0},
	}
	out := forest.applyMMRReranking(pool, features, 2)
	// MMR with lambda=1.0 should not modify ordering; topK trim happens
	// at the call site, not inside MMR.
	if out[0].Branch.ID != "a1" || out[1].Branch.ID != "a2" {
		t.Errorf("lambda=1.0 should preserve relevance order; got [%s, %s]",
			out[0].Branch.ID, out[1].Branch.ID)
	}
}

// TestDiversity_MMRSmallPoolNoOp verifies that when len(pool) <= limit
// MMR is a no-op (no diversity choice to make).
func TestDiversity_MMRSmallPoolNoOp(t *testing.T) {
	forest := &MemoryForest{diversityLambda: 0.5}
	pool := []*BranchPacket{
		{Branch: &Branch{ID: "a"}, Score: PacketScore{Total: 0.9}},
		{Branch: &Branch{ID: "b"}, Score: PacketScore{Total: 0.8}},
	}
	out := forest.applyMMRReranking(pool, map[string][]float32{}, 5)
	if len(out) != 2 {
		t.Errorf("small-pool no-op: len=%d want 2", len(out))
	}
}

// TestDiversity_MMRZeroFeaturesProducesNoPenalty verifies that if all
// candidates lack feature vectors, MMR falls through to relevance-
// only ordering without crashing or producing nonsense.
func TestDiversity_MMRZeroFeaturesProducesNoPenalty(t *testing.T) {
	forest := &MemoryForest{diversityLambda: 0.5}
	pool := []*BranchPacket{
		{Branch: &Branch{ID: "a"}, Score: PacketScore{Total: 0.9}},
		{Branch: &Branch{ID: "b"}, Score: PacketScore{Total: 0.8}},
		{Branch: &Branch{ID: "c"}, Score: PacketScore{Total: 0.7}},
	}
	out := forest.applyMMRReranking(pool, map[string][]float32{}, 2)
	if out[0].Branch.ID != "a" || out[1].Branch.ID != "b" {
		t.Errorf("zero-features fallback to relevance: got [%s,%s]",
			out[0].Branch.ID, out[1].Branch.ID)
	}
}

// TestDiversity_VectorPrimitives exercises the cosine helpers with
// hand-checkable inputs.
func TestDiversity_VectorPrimitives(t *testing.T) {
	if got := vectorDot([]float32{1, 0, 0}, []float32{0, 1, 0}); got != 0 {
		t.Errorf("orthogonal dot: got %v want 0", got)
	}
	if got := vectorDot([]float32{1, 1, 0}, []float32{1, 1, 0}); got != 2 {
		t.Errorf("self dot: got %v want 2", got)
	}
	if got := vectorNorm([]float32{}); got != 0 {
		t.Errorf("empty norm: got %v want 0", got)
	}
	if got := vectorNorm([]float32{3, 4}); got != 5 {
		t.Errorf("3-4-5 norm: got %v want 5", got)
	}
}

// TestRetrievalCooldown_PenaltyAppliesProportionally verifies the
// scale-down formula: a branch present in every recent retrieval is
// scaled down; an absent branch is untouched. Uses direct INSERT into
// forest_retrieval_candidates to avoid async-projection timing.
func TestRetrievalCooldown_PenaltyAppliesProportionally(t *testing.T) {
	forest, db := newTestForestWithConfig(t, Config{
		RetrievalCooldownWindow:  4,
		RetrievalCooldownPenalty: 0.5,
	})
	ctx := context.Background()

	// Insert 4 retrieval events + corresponding candidate rows where
	// branch-hot was returned in all 4 and branch-cold never was. The
	// cooldown query reads forest_retrieval_candidates directly; we
	// don't need the projector.
	for i := 0; i < 4; i++ {
		eventID := stableID("retrieval", "cooldown", "iter", time.Now().UTC().Format(time.RFC3339Nano), string(rune('a'+i)))
		ts := time.Now().UTC().Unix() - int64(i)
		if _, err := db.Exec(`
			INSERT INTO forest_retrieval_events
			(id, session_id, query, requested_at, requested_limit,
			 candidate_count, returned_count, model_version,
			 branch_projection_seq, candidates_blob, include_counter_evidence,
			 duration_micros, exploration_mode)
			VALUES (?, ?, 'cooldown', ?, 1, 2, 1, '', 0, '[]', 0, 0, 0)
		`, eventID, "sess-cooldown", ts); err != nil {
			t.Fatalf("insert audit %d: %v", i, err)
		}
		if _, err := db.Exec(`
			INSERT INTO forest_retrieval_event_seq_log (event_id, appended_at)
			VALUES (?, ?)
		`, eventID, ts); err != nil {
			t.Fatalf("insert seq %d: %v", i, err)
		}
		if _, err := db.Exec(`
			INSERT INTO forest_retrieval_candidates
			(retrieval_event_id, retrieval_seq, branch_id, session_id,
			 rank_position, returned, base_score, final_score,
			 predicted_utility, predicted_risk, exploration_mode, retrieval_at)
			VALUES (?, ?, 'branch-hot', 'sess-cooldown', 1, 1, 0.9, 0.9, 0, 0, 0, ?),
			       (?, ?, 'branch-cold', 'sess-cooldown', 2, 0, 0.5, 0.5, 0, 0, 0, ?)
		`, eventID, int64(i+1), ts, eventID, int64(i+1), ts); err != nil {
			t.Fatalf("insert candidates %d: %v", i, err)
		}
	}

	hot := &BranchPacket{
		Branch: &Branch{ID: "branch-hot"},
		Score:  PacketScore{Total: 1.0, Base: 1.0},
	}
	cold := &BranchPacket{
		Branch: &Branch{ID: "branch-cold"},
		Score:  PacketScore{Total: 1.0, Base: 1.0},
	}
	forest.applyRetrievalCooldownPenalty(ctx, "sess-cooldown", []*BranchPacket{hot, cold})

	// hot returned 4/4 → ratio 1.0 → scale (1 - 0.5*1.0) = 0.5
	if hot.Score.Total > 0.6 {
		t.Errorf("hot branch should be scaled to ~0.5; got %v", hot.Score.Total)
	}
	if cold.Score.Total != 1.0 {
		t.Errorf("cold branch (returned=0) should be untouched; got %v", cold.Score.Total)
	}
}

// TestRetrievalCooldown_DisabledByZeroPenalty verifies the penalty=0
// short-circuit doesn't even hit the DB.
func TestRetrievalCooldown_DisabledByZeroPenalty(t *testing.T) {
	forest := &MemoryForest{retrievalCooldownPenalty: 0, retrievalCooldownWindow: 32}
	pkt := &BranchPacket{Branch: &Branch{ID: "any"}, Score: PacketScore{Total: 1.0}}
	// Should not panic, should not modify scores, should not hit nil DB.
	forest.applyRetrievalCooldownPenalty(context.Background(), "any", []*BranchPacket{pkt})
	if pkt.Score.Total != 1.0 {
		t.Errorf("penalty=0 should not modify scores; got %v", pkt.Score.Total)
	}
}

// TestRetrievalCooldown_EmptyPacketsNoOp verifies the empty-input
// short-circuit doesn't crash on nil DB.
func TestRetrievalCooldown_EmptyPacketsNoOp(t *testing.T) {
	forest := &MemoryForest{retrievalCooldownPenalty: 0.5, retrievalCooldownWindow: 32}
	forest.applyRetrievalCooldownPenalty(context.Background(), "any", nil)
}
