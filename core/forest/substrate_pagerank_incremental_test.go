package forest

import (
	"context"
	"math"
	"testing"
)

// ────────────────────────────────────────────────────────────────────
// Layer 3 — incremental PageRank: push correctness + reconcile
// ────────────────────────────────────────────────────────────────────

// TestIncrementalPR_PushMatchesPowerIteration verifies that running
// push from r=0, p=personalization to convergence produces the same
// rank vector (within ε) as the power-iteration baseline.
func TestIncrementalPR_PushMatchesPowerIteration(t *testing.T) {
	graph := &pageRankGraph{
		indexByID:    map[string]int{"a": 0, "b": 1, "c": 2},
		branchIDs:    []string{"a", "b", "c"},
		outEdges:     [][]pageRankEdge{{{1, 1}, {2, 1}}, {{0, 1}, {2, 1}}, {{0, 1}, {1, 1}}},
		outDegrees:   []float64{2, 2, 2},
		inhibitorIn:  make([][]pageRankEdge, 3),
		conductances: make([][]pageRankEdge, 3),
	}
	graph.normalizeOutgoing()

	personalization := []float64{1.0 / 3, 1.0 / 3, 1.0 / 3}
	powerRank := powerIteratePageRank(graph, personalization)

	state := &pageRankSessionState{
		branchIDs:       graph.branchIDs,
		indexByID:       graph.indexByID,
		outEdges:        graph.outEdges,
		outDegrees:      graph.outDegrees,
		personalization: personalization,
		r:               make([]float64, 3),
		p:               append([]float64(nil), personalization...),
	}
	ops := state.runPushBudgeted(1e-9, 100000)
	if ops == 0 {
		t.Fatal("expected push to do work; got 0 ops")
	}
	for i := 0; i < 3; i++ {
		if math.Abs(state.r[i]-powerRank[i]) > 1e-3 {
			t.Errorf("push diverged from power iteration at %d: push=%.6f power=%.6f", i, state.r[i], powerRank[i])
		}
	}
}

// TestIncrementalPR_PushDanglingNode verifies a dangling node's mass
// is redistributed via the personalization vector — no leakage.
func TestIncrementalPR_PushDanglingNode(t *testing.T) {
	state := &pageRankSessionState{
		branchIDs:       []string{"a", "b"},
		indexByID:       map[string]int{"a": 0, "b": 1},
		outEdges:        [][]pageRankEdge{{{1, 1.0}}, nil},
		outDegrees:      []float64{1, 0},
		personalization: []float64{0.5, 0.5},
		r:               []float64{0, 0},
		p:               []float64{0.5, 0.5},
	}
	state.runPushBudgeted(1e-9, 10000)
	var sum float64
	for _, v := range state.r {
		sum += v
	}
	if sum < 0.95 || sum > 1.05 {
		t.Errorf("dangling-corrected sum: got %v want ~1.0", sum)
	}
}

// TestIncrementalPR_PushBudgetCap verifies the budget bound prevents
// runaway iterations even when residual stays above threshold.
func TestIncrementalPR_PushBudgetCap(t *testing.T) {
	state := &pageRankSessionState{
		branchIDs:       []string{"a"},
		indexByID:       map[string]int{"a": 0},
		outEdges:        [][]pageRankEdge{{{0, 1.0}}}, // self-loop keeps residual flowing
		outDegrees:      []float64{1},
		personalization: []float64{1.0},
		r:               []float64{0},
		p:               []float64{1.0},
	}
	ops := state.runPushBudgeted(1e-12, 100)
	if ops > 100 {
		t.Errorf("budget cap violated: got %d ops want <= 100", ops)
	}
}

// TestIncrementalPR_UnsettleNodesPreservesMass verifies that moving
// settled rank back to residual preserves total r+p mass.
func TestIncrementalPR_UnsettleNodesPreservesMass(t *testing.T) {
	state := &pageRankSessionState{
		r: []float64{0.4, 0.3, 0.3},
		p: []float64{0.0, 0.0, 0.0},
	}
	before := sumFloat(state.r) + sumFloat(state.p)
	state.unsettleNodes(map[int]struct{}{0: {}, 2: {}})
	after := sumFloat(state.r) + sumFloat(state.p)
	if math.Abs(before-after) > 1e-9 {
		t.Errorf("mass changed: before=%v after=%v", before, after)
	}
	if state.r[0] != 0 || state.r[2] != 0 {
		t.Errorf("unsettled nodes should have r=0; got r=%v", state.r)
	}
	if state.p[0] != 0.4 || state.p[2] != 0.3 {
		t.Errorf("residual should hold former r; got p=%v", state.p)
	}
}

func sumFloat(v []float64) float64 {
	var s float64
	for _, x := range v {
		s += x
	}
	return s
}

// TestIncrementalPR_NodeOverThreshold exercises the threshold helper
// for both dangling and edge-bearing nodes.
func TestIncrementalPR_NodeOverThreshold(t *testing.T) {
	state := &pageRankSessionState{
		outDegrees: []float64{2, 0, 1},
		p:          []float64{0.5, 0.0001, 0.0001},
	}
	if !state.nodeOverThreshold(0, 0.01) {
		t.Errorf("node 0: p/d=0.25 > 0.01 should be over threshold")
	}
	if state.nodeOverThreshold(1, 0.001) {
		t.Errorf("dangling node 1: p=0.0001 should be UNDER threshold 0.001")
	}
	if state.nodeOverThreshold(2, 0.001) {
		t.Errorf("node 2: p/d=0.0001 should be UNDER threshold 0.001")
	}
}

// TestIncrementalPR_ApplyEdgeChangesUpdatesNormalization verifies
// that an edge weight change correctly bumps the source's outDegree
// and re-normalizes the source's outgoing edges.
func TestIncrementalPR_ApplyEdgeChangesUpdatesNormalization(t *testing.T) {
	state := &pageRankSessionState{
		indexByID:    map[string]int{"a": 0, "b": 1, "c": 2},
		branchIDs:    []string{"a", "b", "c"},
		outEdges:     [][]pageRankEdge{{{1, 0.5}, {2, 0.5}}, nil, nil},
		rawWeights:   [][]pageRankEdge{{{1, 1.0}, {2, 1.0}}, nil, nil},
		outDegrees:   []float64{2.0, 0, 0},
		inhibitorIn:  make([][]pageRankEdge, 3),
		conductances: [][]pageRankEdge{{{1, 1.0}, {2, 1.0}}, nil, nil},
		// applyEdgeChanges enforces all-slices-bounds; init r/p so
		// the bounds check passes.
		r: make([]float64, 3),
		p: make([]float64, 3),
	}
	// Bump a→b weight from 1.0 to 3.0 (a→b becomes dominant).
	affected := state.applyEdgeChanges([]pageRankRelayEdgeChange{
		{source: "a", target: "b", relation: "supports", weight: 3.0, appliedSeq: 5},
	})
	if _, ok := affected[0]; !ok {
		t.Errorf("source 'a' (idx 0) should be in affected set")
	}
	if _, ok := affected[1]; !ok {
		t.Errorf("target 'b' (idx 1) should be in affected set")
	}
	if state.outDegrees[0] != 4.0 {
		t.Errorf("source out-degree: got %v want 4.0", state.outDegrees[0])
	}
	for _, e := range state.outEdges[0] {
		if e.target == 1 && math.Abs(e.weight-0.75) > 1e-9 {
			t.Errorf("a→b normalized: got %v want 0.75", e.weight)
		}
		if e.target == 2 && math.Abs(e.weight-0.25) > 1e-9 {
			t.Errorf("a→c normalized: got %v want 0.25", e.weight)
		}
	}
}

// TestIncrementalPR_ApplyEdgeChangesIgnoresOutOfSubgraph verifies
// edges crossing out of the cached session subgraph are silently
// skipped — they don't influence this session's PR.
func TestIncrementalPR_ApplyEdgeChangesIgnoresOutOfSubgraph(t *testing.T) {
	state := &pageRankSessionState{
		indexByID:    map[string]int{"a": 0},
		branchIDs:    []string{"a"},
		outEdges:     make([][]pageRankEdge, 1),
		rawWeights:   make([][]pageRankEdge, 1),
		outDegrees:   []float64{0},
		inhibitorIn:  make([][]pageRankEdge, 1),
		conductances: make([][]pageRankEdge, 1),
		r:            make([]float64, 1),
		p:            make([]float64, 1),
	}
	affected := state.applyEdgeChanges([]pageRankRelayEdgeChange{
		{source: "a", target: "outsider", weight: 1.0},
		{source: "outsider", target: "a", weight: 1.0},
	})
	if len(affected) != 0 {
		t.Errorf("out-of-subgraph edges should be ignored; got affected=%v", affected)
	}
}

// TestIncrementalPR_ReconcileNoOpWhenCursorUnchanged verifies that
// reconcile is a fast no-op when the cursor hasn't advanced.
func TestIncrementalPR_ReconcileNoOpWhenCursorUnchanged(t *testing.T) {
	forest, _ := newTestForest(t)
	ctx := context.Background()
	state := &pageRankSessionState{
		sessionID:         "s",
		lastReconciledSeq: 999999, // ahead of any actual seq
		indexByID:         map[string]int{"a": 0},
		r:                 []float64{0.5},
		p:                 []float64{0},
		outDegrees:        []float64{0},
	}
	// Cursor is "ahead", so reconcile is a no-op.
	if err := forest.reconcilePageRankState(ctx, state); err != nil {
		t.Fatal(err)
	}
	if state.r[0] != 0.5 {
		t.Errorf("no-op reconcile changed state: r=%v", state.r)
	}
}

// TestIncrementalPR_ServeFromStateNilTolerant verifies the read-path
// tolerates nil branches and missing entries.
func TestIncrementalPR_ServeFromStateNilTolerant(t *testing.T) {
	state := &pageRankSessionState{
		indexByID: map[string]int{"a": 0},
		branchIDs: []string{"a"},
		r:         []float64{0.7},
		p:         []float64{0},
		outDegrees:   []float64{0},
		inhibitorIn:  make([][]pageRankEdge, 1),
		conductances: make([][]pageRankEdge, 1),
	}
	out := state.signalsFor([]*Branch{
		{ID: "a"},
		nil,
		{ID: "absent"},
	})
	if out["a"].Potential != 0.7 {
		t.Errorf("a potential: got %v want 0.7", out["a"].Potential)
	}
	if _, ok := out["absent"]; !ok {
		t.Errorf("absent branch should get zero-signal entry, not be missing")
	}
	if out["absent"].Potential != 0 {
		t.Errorf("absent zero-signal: got %v", out["absent"].Potential)
	}
}

// TestIncrementalPR_PageRankCacheStatsReports verifies the operator-
// visible stats surface reports current cache occupancy.
func TestIncrementalPR_PageRankCacheStatsReports(t *testing.T) {
	forest, _ := newTestForest(t)
	cache := forest.pageRankIncrementalCache
	cache.putIfAbsent("s1", &pageRankSessionState{sessionID: "s1", branchIDs: []string{"a", "b"}, lastReconciledSeq: 5})
	cache.putIfAbsent("s2", &pageRankSessionState{sessionID: "s2", branchIDs: []string{"c"}, lastReconciledSeq: 7})

	stats := forest.PageRankCacheStats()
	if stats.SessionCount != 2 {
		t.Errorf("session count: got %d want 2", stats.SessionCount)
	}
	if stats.Capacity == 0 {
		t.Errorf("capacity not reported")
	}
	if len(stats.Sessions) != 2 {
		t.Errorf("session detail: got %d want 2", len(stats.Sessions))
	}
}

// TestIncrementalPR_NilForestSafe verifies the stats endpoint doesn't
// crash on a nil receiver.
func TestIncrementalPR_NilForestSafe(t *testing.T) {
	var f *MemoryForest
	stats := f.PageRankCacheStats()
	if stats.SessionCount != 0 {
		t.Errorf("nil-receiver: got %v", stats)
	}
}

// TestIncrementalPR_StalenessTriggersReinit verifies that a session
// branch count mismatch causes the reconcile path to re-init the
// state in place, picking up new branches that the edge-change
// reconcile alone would silently skip.
func TestIncrementalPR_StalenessTriggersReinit(t *testing.T) {
	forest, _ := newTestForest(t)
	ctx := context.Background()

	// Seed two branches in session "s".
	for _, title := range []string{"a", "b"} {
		event := &Event{
			SessionID: "s",
			BranchID:  "branch-" + title,
			AgentID:   "engineer", AgentType: "engineer",
			EventType: EventTypeDecisionRecorded,
			Family:    TreeFamilyDecision,
			Title:     "Stale " + title,
		}
		if err := forest.AppendEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
	}

	// Build cached state pretending the session only had ONE branch.
	state := &pageRankSessionState{
		sessionID: "s",
		branchIDs: []string{"branch-a"},
		indexByID: map[string]int{"branch-a": 0},
		r:         []float64{0.5},
		p:         []float64{0},
		outDegrees: []float64{0},
		inhibitorIn:  make([][]pageRankEdge, 1),
		conductances: make([][]pageRankEdge, 1),
		outEdges:     make([][]pageRankEdge, 1),
		rawWeights:   make([][]pageRankEdge, 1),
		personalization: []float64{1.0},
	}

	stale, err := forest.detectSubgraphStaleness(ctx, state)
	if err != nil {
		t.Fatal(err)
	}
	if !stale {
		t.Fatal("expected stale=true (cached has 1, live has 2)")
	}

	if err := forest.reinitPageRankStateInPlace(ctx, state); err != nil {
		t.Fatal(err)
	}
	if len(state.branchIDs) != 2 {
		t.Errorf("after reinit: branch count got %d want 2", len(state.branchIDs))
	}
	if _, ok := state.indexByID["branch-b"]; !ok {
		t.Errorf("branch-b not in re-init'd index")
	}
}

// TestIncrementalPR_UniformPersonalizationFallback exercises the
// fallback when reinit can't size the existing personalization
// vector to the new branch count.
func TestIncrementalPR_UniformPersonalizationFallback(t *testing.T) {
	got := uniformPersonalizationVector(4)
	if len(got) != 4 {
		t.Fatalf("len: got %d want 4", len(got))
	}
	for _, v := range got {
		if v != 0.25 {
			t.Errorf("uniform value: got %v want 0.25", v)
		}
	}
	if uniformPersonalizationVector(0) != nil {
		t.Errorf("n=0 should return nil")
	}
}

// TestIncrementalPR_ReinitOnEmptySession handles the case where the
// session's branches all get superseded — re-init produces an empty
// state without panicking.
func TestIncrementalPR_ReinitOnEmptySession(t *testing.T) {
	forest, _ := newTestForest(t)
	ctx := context.Background()
	state := &pageRankSessionState{
		sessionID:    "empty",
		branchIDs:    []string{"ghost"},
		indexByID:    map[string]int{"ghost": 0},
		r:            []float64{0.5},
		p:            []float64{0},
		outDegrees:   []float64{0},
		inhibitorIn:  make([][]pageRankEdge, 1),
		conductances: make([][]pageRankEdge, 1),
	}
	if err := forest.reinitPageRankStateInPlace(ctx, state); err != nil {
		t.Fatal(err)
	}
	if len(state.branchIDs) != 0 {
		t.Errorf("empty session: branchIDs got %d want 0", len(state.branchIDs))
	}
}
