//go:build forest_legacy_archive

package forest

import (
	"context"
	"testing"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Issue #7 — Substrate mode + PageRank tests
// ────────────────────────────────────────────────────────────────────

// TestSubstrateMode_ResolverDefaults exercises the resolve helpers.
func TestSubstrateMode_ResolverDefaults(t *testing.T) {
	if got := resolveSubstrateMode(""); got != defaultSubstrateMode {
		t.Errorf("empty mode default: got %v want %v", got, defaultSubstrateMode)
	}
	if got := resolveSubstrateMode("garbage"); got != defaultSubstrateMode {
		t.Errorf("invalid mode: got %v want %v", got, defaultSubstrateMode)
	}
	if got := resolveSubstrateMode(SubstrateModePageRank); got != SubstrateModePageRank {
		t.Errorf("valid mode passthrough: got %v", got)
	}
	if got := resolveSubstrateABRate(-1); got != defaultSubstrateABRate {
		t.Errorf("negative AB rate default: got %v", got)
	}
	if got := resolveSubstrateABRate(2.0); got != 1.0 {
		t.Errorf("AB rate clamp >1: got %v want 1.0", got)
	}
}

// TestSubstrateMode_DominantWithABRateZero verifies that with
// SubstrateABRate=0 every retrieval returns the dominant mode.
func TestSubstrateMode_DominantWithABRateZero(t *testing.T) {
	forest, _ := newTestForestWithConfig(t, Config{
		SubstrateMode:   SubstrateModePageRank,
		SubstrateABRate: 0,
	})
	for i := 0; i < 50; i++ {
		if mode := forest.resolveRetrieveSubstrateMode(); mode != SubstrateModePageRank {
			t.Errorf("iter %d: got %v, want pagerank", i, mode)
		}
	}
}

// TestSubstrateMode_ABRateOneAlwaysSwaps verifies that ABRate=1 picks
// uniformly from the non-dominant modes.
func TestSubstrateMode_ABRateOneAlwaysSwaps(t *testing.T) {
	forest, _ := newTestForestWithConfig(t, Config{
		SubstrateMode:     SubstrateModeFull,
		SubstrateABRate:   1.0,
		SubstrateModeSeed: 7,
	})
	for i := 0; i < 50; i++ {
		mode := forest.resolveRetrieveSubstrateMode()
		if mode == SubstrateModeFull {
			t.Errorf("iter %d: ABRate=1 should never produce dominant; got %v", i, mode)
		}
	}
}

// TestSubstrateMode_DeterministicWithSeed verifies a seeded RNG
// produces identical mode sequences across runs.
func TestSubstrateMode_DeterministicWithSeed(t *testing.T) {
	const trials = 32
	collect := func() []SubstrateMode {
		f := &MemoryForest{
			substrateMode:    SubstrateModeFull,
			substrateABRate:  0.5,
			substrateModeRng: newExplorationRng(42),
		}
		out := make([]SubstrateMode, trials)
		for i := 0; i < trials; i++ {
			out[i] = f.resolveRetrieveSubstrateMode()
		}
		return out
	}
	a := collect()
	b := collect()
	for i := 0; i < trials; i++ {
		if a[i] != b[i] {
			t.Errorf("seeded RNG diverged at %d: %v vs %v", i, a[i], b[i])
		}
	}
}

// TestSubstrateMode_NonDominantHelper verifies the non-dominant
// candidate set excludes the dominant mode.
func TestSubstrateMode_NonDominantHelper(t *testing.T) {
	got := nonDominantSubstrateModes(SubstrateModeFull)
	for _, m := range got {
		if m == SubstrateModeFull {
			t.Errorf("nonDominant included dominant: %v", got)
		}
	}
	if len(got) != 2 {
		t.Errorf("expected 2 alternatives; got %d", len(got))
	}
}

// TestSubstrateMode_AuditCapturesMode verifies that the substrate
// mode chosen at Retrieve time persists in the audit row.
func TestSubstrateMode_AuditCapturesMode(t *testing.T) {
	t.Skip("legacy branch retrieval audit removed from production retrieval")
	forest, _ := newAsyncTestForestWithConfig(t, Config{
		SubstrateMode: SubstrateModePageRank,
	})
	ctx := context.Background()

	event := &Event{
		SessionID: "sess-mode",
		BranchID:  "branch-x",
		AgentID:   "engineer", AgentType: "engineer",
		EventType: EventTypeDecisionRecorded,
		Family:    TreeFamilyDecision,
		Title:     "x",
	}
	if err := forest.AppendEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := forest.WaitForBranchSeq(ctx, event.Seq, 5*time.Second); err != nil {
		t.Fatal(err)
	}

	if _, err := forest.retrieveBranchPackets(ctx, Query{
		SessionID: "sess-mode",
		Query:     "x",
		Limit:     1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := forest.WaitForRetrievalAuditDrain(ctx, 5*time.Second); err != nil {
		t.Fatal(err)
	}

	results, err := forest.QueryRetrievalAudits(ctx, RetrievalAuditFilter{SessionID: "sess-mode"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 audit; got %d", len(results))
	}
	if results[0].SubstrateMode != SubstrateModePageRank {
		t.Errorf("audit substrate_mode: got %q want pagerank", results[0].SubstrateMode)
	}
}

// ────────────────────────────────────────────────────────────────────
// PageRank correctness
// ────────────────────────────────────────────────────────────────────

// TestPageRank_UniformGraphSumsToOne verifies the standard PageRank
// invariant: sum(rank) ≈ 1 on a connected graph.
func TestPageRank_UniformGraphSumsToOne(t *testing.T) {
	graph := &pageRankGraph{
		indexByID:    map[string]int{"a": 0, "b": 1, "c": 2},
		branchIDs:    []string{"a", "b", "c"},
		outEdges:     [][]pageRankEdge{{{1, 1.0}, {2, 1.0}}, {{0, 1.0}, {2, 1.0}}, {{0, 1.0}, {1, 1.0}}},
		outDegrees:   []float64{2, 2, 2},
		inhibitorIn:  make([][]pageRankEdge, 3),
		conductances: make([][]pageRankEdge, 3),
	}
	graph.normalizeOutgoing()

	pers := []float64{1.0 / 3, 1.0 / 3, 1.0 / 3}
	rank := powerIteratePageRank(graph, pers)
	var sum float64
	for _, r := range rank {
		sum += r
	}
	if sum < 0.99 || sum > 1.01 {
		t.Errorf("sum != ~1.0: got %v", sum)
	}
}

// TestPageRank_PersonalizationConcentratesMass verifies that
// personalization heavily favoring node A (canopy root) produces a
// rank vector where A has the highest mass.
func TestPageRank_PersonalizationConcentratesMass(t *testing.T) {
	branches := []*Branch{
		{ID: "a", RootID: "root-a"},
		{ID: "b", RootID: "other"},
		{ID: "c", RootID: "other"},
	}
	graph := &pageRankGraph{
		indexByID:    map[string]int{"a": 0, "b": 1, "c": 2},
		branchIDs:    []string{"a", "b", "c"},
		outEdges:     [][]pageRankEdge{{{1, 1}, {2, 1}}, {{0, 1}}, {{0, 1}}},
		outDegrees:   []float64{2, 1, 1},
		inhibitorIn:  make([][]pageRankEdge, 3),
		conductances: make([][]pageRankEdge, 3),
	}
	graph.normalizeOutgoing()
	canopyRoots := map[string]struct{}{"root-a": {}}
	pers := buildPersonalizationVector(branches, canopyRoots)
	rank := powerIteratePageRank(graph, pers)
	if rank[0] <= rank[1] || rank[0] <= rank[2] {
		t.Errorf("a should dominate via personalization; got rank=%v", rank)
	}
}

// TestPageRank_DanglingNodeRedistribution verifies a graph with a
// dangling node (no outgoing edges) doesn't leak mass.
func TestPageRank_DanglingNodeRedistribution(t *testing.T) {
	graph := &pageRankGraph{
		indexByID:    map[string]int{"a": 0, "b": 1},
		branchIDs:    []string{"a", "b"},
		outEdges:     [][]pageRankEdge{{{1, 1.0}}, nil}, // b is dangling
		outDegrees:   []float64{1, 0},
		inhibitorIn:  make([][]pageRankEdge, 2),
		conductances: make([][]pageRankEdge, 2),
	}
	graph.normalizeOutgoing()
	pers := []float64{0.5, 0.5}
	rank := powerIteratePageRank(graph, pers)
	var sum float64
	for _, r := range rank {
		sum += r
	}
	if sum < 0.95 || sum > 1.05 {
		t.Errorf("dangling-corrected sum: got %v want ~1.0", sum)
	}
}

// TestPageRank_UniformFallbackOnEmptyCanopy verifies the
// personalization vector falls back to uniform when no canopy roots
// match — required for cold-start sessions.
func TestPageRank_UniformFallbackOnEmptyCanopy(t *testing.T) {
	branches := []*Branch{
		{ID: "a", RootID: "x"},
		{ID: "b", RootID: "y"},
	}
	pers := buildPersonalizationVector(branches, map[string]struct{}{})
	if pers[0] != 0.5 || pers[1] != 0.5 {
		t.Errorf("uniform fallback: got %v want [0.5, 0.5]", pers)
	}
}

// ────────────────────────────────────────────────────────────────────
// PageRank cache
// ────────────────────────────────────────────────────────────────────

// TestPageRankIncrementalCache_HitMiss verifies the cache returns
// putIfAbsent values and reports miss on uncached sessions.
func TestPageRankIncrementalCache_HitMiss(t *testing.T) {
	c := newPageRankIncrementalCache(4)
	if _, ok := c.get("s"); ok {
		t.Errorf("empty cache returned hit")
	}
	state := &pageRankSessionState{sessionID: "s", branchIDs: []string{"a"}, r: []float64{0.3}}
	stored := c.putIfAbsent("s", state)
	if stored != state {
		t.Errorf("first putIfAbsent should store the new state")
	}
	got, ok := c.get("s")
	if !ok {
		t.Fatalf("after put: cache reported miss")
	}
	if got.r[0] != 0.3 {
		t.Errorf("cache returned wrong state: %v", got.r)
	}
}

// TestPageRankIncrementalCache_LRUEviction verifies oldest sessions
// are evicted when capacity is exceeded.
func TestPageRankIncrementalCache_LRUEviction(t *testing.T) {
	c := newPageRankIncrementalCache(2)
	c.putIfAbsent("s1", &pageRankSessionState{sessionID: "s1"})
	c.putIfAbsent("s2", &pageRankSessionState{sessionID: "s2"})
	c.putIfAbsent("s3", &pageRankSessionState{sessionID: "s3"})

	if _, ok := c.get("s1"); ok {
		t.Errorf("s1 should have been evicted (LRU)")
	}
	if _, ok := c.get("s2"); !ok {
		t.Errorf("s2 should still be cached")
	}
}

// TestPageRankIncrementalCache_PutIfAbsentReturnsExisting verifies
// that a second putIfAbsent for the same sessionID returns the
// existing entry — used by the get-or-init pattern to avoid races
// where two goroutines compute fresh state for the same session.
func TestPageRankIncrementalCache_PutIfAbsentReturnsExisting(t *testing.T) {
	c := newPageRankIncrementalCache(4)
	first := &pageRankSessionState{sessionID: "s", lastReconciledSeq: 1}
	second := &pageRankSessionState{sessionID: "s", lastReconciledSeq: 2}
	c.putIfAbsent("s", first)
	got := c.putIfAbsent("s", second)
	if got != first {
		t.Errorf("putIfAbsent should return existing entry; got %p want %p", got, first)
	}
	if got.lastReconciledSeq != 1 {
		t.Errorf("existing entry preserved: got seq=%d want 1", got.lastReconciledSeq)
	}
}

// ────────────────────────────────────────────────────────────────────
// SubstrateModeStatsSince diagnostic
// ────────────────────────────────────────────────────────────────────

// TestSubstrateStats_RetrievalSideAggregates verifies the diagnostic
// rolls up retrieval counts + means per mode.
func TestSubstrateStats_RetrievalSideAggregates(t *testing.T) {
	forest, db := newTestForest(t)
	ctx := context.Background()

	now := time.Now().UTC().Unix()
	insert := func(id string, mode SubstrateMode, candidates, returned, duration int) {
		t.Helper()
		_, err := db.Exec(`
			INSERT INTO forest_retrieval_events
			(id, session_id, query, requested_at, requested_limit,
			 candidate_count, returned_count, model_version,
			 branch_projection_seq, candidates_blob,
			 include_counter_evidence, duration_micros, exploration_mode,
			 substrate_mode)
			VALUES (?, 's', 'q', ?, 1, ?, ?, '', 0, '[]', 0, ?, 0, ?)
		`, id, now, candidates, returned, duration, string(mode))
		if err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
		_, err = db.Exec(`
			INSERT INTO forest_retrieval_event_seq_log (event_id, appended_at)
			VALUES (?, ?)
		`, id, now)
		if err != nil {
			t.Fatalf("seq %s: %v", id, err)
		}
	}
	insert("ev1", SubstrateModeFull, 50, 5, 1000)
	insert("ev2", SubstrateModeFull, 60, 6, 1500)
	insert("ev3", SubstrateModePageRank, 40, 4, 800)

	stats, err := forest.SubstrateModeStatsSince(ctx, time.Now().Add(-1*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	byMode := make(map[SubstrateMode]SubstrateModeStats, len(stats))
	for _, s := range stats {
		byMode[s.Mode] = s
	}
	if byMode[SubstrateModeFull].RetrievalCount != 2 {
		t.Errorf("full count: got %v want 2", byMode[SubstrateModeFull].RetrievalCount)
	}
	if byMode[SubstrateModePageRank].RetrievalCount != 1 {
		t.Errorf("pagerank count: got %v want 1", byMode[SubstrateModePageRank].RetrievalCount)
	}
	if byMode[SubstrateModeFull].MeanDurationMicros != 1250 {
		t.Errorf("full mean duration: got %v want 1250", byMode[SubstrateModeFull].MeanDurationMicros)
	}
}

// TestSubstrateStats_NilForestSafe verifies the diagnostic doesn't
// crash on a nil receiver.
func TestSubstrateStats_NilForestSafe(t *testing.T) {
	var f *MemoryForest
	stats, err := f.SubstrateModeStatsSince(context.Background(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if stats != nil {
		t.Errorf("nil-receiver should return nil; got %v", stats)
	}
}

// TestSubstrateMode_DispatchWarmthOnlyEmpty verifies that the
// warmth_only mode short-circuits substrate to an empty signal map.
func TestSubstrateMode_DispatchWarmthOnlyEmpty(t *testing.T) {
	forest, _ := newTestForest(t)
	signals, err := forest.loadSubstrateSignals(context.Background(), SubstrateModeWarmthOnly,
		[]*Branch{{ID: "a"}, {ID: "b"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(signals) != 0 {
		t.Errorf("warmth_only should return empty; got %v", signals)
	}
}

// TestPageRank_ProjectCachedSignalsZerosMissing verifies that when
// the cache holds signals for a subset of branches, the projection
// returns zero-signal entries for missing branches (rather than
// silently dropping them, which would break scoring on branches
// added between cache fill and read).
func TestPageRank_ProjectCachedSignalsZerosMissing(t *testing.T) {
	cached := map[string]substrateSignal{
		"a": {Potential: 0.4, Frontier: 0.2},
	}
	requested := []*Branch{
		{ID: "a"},
		{ID: "b"}, // not in cache — should get zero-signal, not be dropped
		nil,       // nil-tolerant
	}
	out := projectCachedSignals(cached, requested)
	if got := out["a"]; got.Potential != 0.4 {
		t.Errorf("a present: got %v want 0.4", got.Potential)
	}
	if got, ok := out["b"]; !ok {
		t.Errorf("b should have a zero-signal entry, not be missing")
	} else if got.Potential != 0 {
		t.Errorf("b zero-signal: got %v want 0", got.Potential)
	}
	if _, ok := out[""]; ok {
		t.Errorf("nil branch produced an empty-id entry: %v", out)
	}
}

// TestSubstrateStats_OutcomeStatusFromPayloadJSON verifies the
// JSON parser fallback (no SQLite json1 dependency).
func TestSubstrateStats_OutcomeStatusFromPayloadJSON(t *testing.T) {
	cases := []struct {
		raw    string
		expect OutcomeStatus
	}{
		{`{"status":"succeeded"}`, OutcomeStatusSucceeded},
		{`{"status":"failed"}`, OutcomeStatusFailed},
		{`{"status":"mixed"}`, OutcomeStatusMixed},
		{``, OutcomeStatusMixed},
		{`not json`, OutcomeStatusMixed},
		{`{"other":"field"}`, OutcomeStatusMixed},
	}
	for _, c := range cases {
		got := outcomeStatusFromPayloadJSON(c.raw)
		if got != c.expect {
			t.Errorf("%q: got %v want %v", c.raw, got, c.expect)
		}
	}
}
