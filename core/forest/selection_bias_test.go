package forest

import (
	"context"
	"testing"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Counterfactual labeling
// ────────────────────────────────────────────────────────────────────

// TestSelectionBias_CounterfactualLabelingExpands verifies that an
// outcome event on branch B labels the explicit row PLUS every
// co-candidate row from recent retrievals containing B.
func TestSelectionBias_CounterfactualLabelingExpands(t *testing.T) {
	forest, _ := newAsyncTestForest(t)
	ctx := context.Background()

	// Seed three branches.
	for i, title := range []string{"alpha", "beta", "gamma"} {
		event := &Event{
			SessionID: "sess-cf",
			BranchID:  "branch-" + title,
			AgentID:   "engineer", AgentType: "engineer",
			EventType: EventTypeDecisionRecorded,
			Family:    TreeFamilyDecision,
			Title:     "Counterfactual " + title,
		}
		_ = i
		if err := forest.AppendEvent(ctx, event); err != nil {
			t.Fatalf("seed branch %s: %v", title, err)
		}
		if err := forest.WaitForBranchSeq(ctx, event.Seq, 5*time.Second); err != nil {
			t.Fatalf("wait branch %s: %v", title, err)
		}
	}

	// Run a retrieval that scores all three; capture audit so the
	// candidates projector populates forest_retrieval_candidates.
	if _, err := forest.Retrieve(ctx, Query{
		SessionID: "sess-cf",
		Query:     "Counterfactual",
		Limit:     3,
		Families:  []TreeFamily{TreeFamilyDecision},
	}); err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if err := forest.WaitForRetrievalAuditDrain(ctx, 5*time.Second); err != nil {
		t.Fatalf("audit drain: %v", err)
	}
	// Wait for the candidates projector to project the audit event.
	if err := waitForCandidatesProjection(forest, 5*time.Second); err != nil {
		t.Fatalf("candidates projection: %v", err)
	}

	// Record a positive outcome on branch-alpha. Counterfactual labels
	// should land on beta and gamma (the co-candidates).
	if err := forest.RecordOutcome(ctx, OutcomeRecord{
		SessionID: "sess-cf",
		BranchID:  "branch-alpha",
		Status:    OutcomeStatusSucceeded,
		Summary:   "alpha worked",
	}); err != nil {
		t.Fatalf("record outcome: %v", err)
	}
	// Wait for projector to apply the outcome event (which fires
	// the labeling pass).
	time.Sleep(200 * time.Millisecond)

	stats, err := forest.TrainingLabelStatsSince(ctx, time.Now().Add(-1*time.Hour))
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Explicit < 1 {
		t.Errorf("expected at least 1 explicit label, got %d", stats.Explicit)
	}
	if stats.Counterfactual < 2 {
		t.Errorf("expected at least 2 counterfactual labels (beta, gamma), got %d", stats.Counterfactual)
	}
}

// TestSelectionBias_CounterfactualInversion verifies the counterfactual
// label is the INVERSE of the explicit outcome (success → soft-negative
// for losers; failure → soft-positive for losers).
func TestSelectionBias_CounterfactualInversion(t *testing.T) {
	if got := invertOutcomeStatus(OutcomeStatusSucceeded); got != OutcomeStatusFailed {
		t.Errorf("invert(success) = %v, want failed", got)
	}
	if got := invertOutcomeStatus(OutcomeStatusFailed); got != OutcomeStatusSucceeded {
		t.Errorf("invert(failed) = %v, want succeeded", got)
	}
	if got := invertOutcomeStatus(OutcomeStatusMixed); got != OutcomeStatusMixed {
		t.Errorf("invert(mixed) = %v, want mixed", got)
	}
}

// ────────────────────────────────────────────────────────────────────
// ε-greedy exploration
// ────────────────────────────────────────────────────────────────────

// TestSelectionBias_ExplorationRateZeroDisables verifies that with
// ExplorationRate=0, no retrieval ever enters exploration mode.
func TestSelectionBias_ExplorationRateZeroDisables(t *testing.T) {
	forest, _ := newTestForestWithConfig(t, Config{ExplorationRate: 0})
	for i := 0; i < 50; i++ {
		if forest.shouldExplore() {
			t.Errorf("iteration %d: shouldExplore returned true with rate=0", i)
		}
	}
}

// TestSelectionBias_ExplorationRateOneAlwaysExplores verifies that
// with ExplorationRate=1.0, every retrieval enters exploration mode.
func TestSelectionBias_ExplorationRateOneAlwaysExplores(t *testing.T) {
	forest, _ := newTestForestWithConfig(t, Config{ExplorationRate: 1.0})
	for i := 0; i < 50; i++ {
		if !forest.shouldExplore() {
			t.Errorf("iteration %d: shouldExplore returned false with rate=1.0", i)
		}
	}
}

// TestSelectionBias_ExplorationDeterministicWithSeed verifies that a
// seeded RNG produces identical exploration sequences across runs.
// Constructs minimal MemoryForest structs directly so two replays can
// share the test name without colliding on the SQLite shared-cache DB.
func TestSelectionBias_ExplorationDeterministicWithSeed(t *testing.T) {
	const trials = 32
	seed := int64(12345)

	collect := func() []bool {
		forest := &MemoryForest{
			explorationRate: 0.5,
			explorationRng:  newExplorationRng(seed),
		}
		out := make([]bool, trials)
		for i := 0; i < trials; i++ {
			out[i] = forest.shouldExplore()
		}
		return out
	}

	a := collect()
	b := collect()
	for i := 0; i < trials; i++ {
		if a[i] != b[i] {
			t.Errorf("seeded RNG diverged at trial %d: %v vs %v", i, a[i], b[i])
		}
	}
}

// TestSelectionBias_ExplorationShufflesPickReplacesTopK exercises
// explorationShuffleAndPick — must return `limit` packets from the
// input pool, may include any rank position.
func TestSelectionBias_ExplorationShufflesPickReplacesTopK(t *testing.T) {
	forest, _ := newTestForestWithConfig(t, Config{
		ExplorationRate: 1.0,
		ExplorationSeed: 42,
	})

	pool := make([]*BranchPacket, 10)
	for i := range pool {
		pool[i] = &BranchPacket{Branch: &Branch{ID: "b" + string(rune('0'+i))}}
	}

	picked := forest.explorationShuffleAndPick(pool, 3)
	if len(picked) != 3 {
		t.Fatalf("pick count: got %d, want 3", len(picked))
	}
	// All three should be unique
	seen := make(map[string]struct{})
	for _, p := range picked {
		if _, dup := seen[p.Branch.ID]; dup {
			t.Errorf("duplicate in pick: %s", p.Branch.ID)
		}
		seen[p.Branch.ID] = struct{}{}
	}
}

// TestSelectionBias_RetrieveExplorationModeRecorded verifies that
// when exploration fires, the audit event records ExplorationMode=true.
func TestSelectionBias_RetrieveExplorationModeRecorded(t *testing.T) {
	// Use ExplorationRate=1.0 to guarantee exploration on every
	// retrieval. Audit drainer is async so we wait for drain.
	forest, _ := newAsyncTestForestWithExploration(t, 1.0, 99)
	ctx := context.Background()

	// Seed some branches.
	for i, title := range []string{"e1", "e2", "e3"} {
		event := &Event{
			SessionID: "sess-explore",
			BranchID:  "branch-" + title,
			AgentID:   "engineer", AgentType: "engineer",
			EventType: EventTypeDecisionRecorded,
			Family:    TreeFamilyDecision,
			Title:     "Explore " + title,
		}
		_ = i
		if err := forest.AppendEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
		if err := forest.WaitForBranchSeq(ctx, event.Seq, 5*time.Second); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := forest.Retrieve(ctx, Query{
		SessionID: "sess-explore",
		Query:     "Explore",
		Limit:     2,
	}); err != nil {
		t.Fatal(err)
	}
	if err := forest.WaitForRetrievalAuditDrain(ctx, 5*time.Second); err != nil {
		t.Fatal(err)
	}

	results, err := forest.QueryRetrievalAudits(ctx, RetrievalAuditFilter{SessionID: "sess-explore"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 audit, got %d", len(results))
	}
	if !results[0].ExplorationMode {
		t.Errorf("audit.ExplorationMode = false, want true (rate=1.0)")
	}
}

// ────────────────────────────────────────────────────────────────────
// Implicit-negative sweeper
// ────────────────────────────────────────────────────────────────────

// TestSelectionBias_ImplicitNegativeSweepLabelsAged verifies the
// sweeper labels returned branches as Ignored when older than horizon
// and no explicit outcome exists. Calls runImplicitNegativeSweepOnce
// directly to bypass the periodic ticker.
func TestSelectionBias_ImplicitNegativeSweepLabelsAged(t *testing.T) {
	forest, _ := newAsyncTestForestWithExploration(t, 0, 0)
	ctx := context.Background()

	// Seed a branch.
	event := &Event{
		SessionID: "sess-sweep",
		BranchID:  "branch-sweep",
		AgentID:   "engineer", AgentType: "engineer",
		EventType: EventTypeDecisionRecorded,
		Family:    TreeFamilyDecision,
		Title:     "Sweep target",
	}
	if err := forest.AppendEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := forest.WaitForBranchSeq(ctx, event.Seq, 5*time.Second); err != nil {
		t.Fatal(err)
	}

	// Issue a retrieval and DON'T fire an outcome.
	if _, err := forest.Retrieve(ctx, Query{
		SessionID: "sess-sweep",
		Query:     "Sweep",
		Limit:     1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := forest.WaitForRetrievalAuditDrain(ctx, 5*time.Second); err != nil {
		t.Fatal(err)
	}
	if err := waitForCandidatesProjection(forest, 5*time.Second); err != nil {
		t.Fatal(err)
	}

	// Force the cutoff into the future so the recently-emitted
	// retrieval qualifies as "aged."
	forest.implicitNegativeHorizon = -1 * time.Second

	if err := forest.runImplicitNegativeSweepOnce(ctx); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	stats, err := forest.TrainingLabelStatsSince(ctx, time.Now().Add(-1*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if stats.ImplicitNegative < 1 {
		t.Errorf("expected at least 1 implicit_negative label, got %d", stats.ImplicitNegative)
	}
}

// TestSelectionBias_SweepIdempotent verifies a second sweep over the
// same data doesn't add new labels — swept_at is recorded.
func TestSelectionBias_SweepIdempotent(t *testing.T) {
	forest, _ := newAsyncTestForestWithExploration(t, 0, 0)
	ctx := context.Background()

	event := &Event{
		SessionID: "sess-idem",
		BranchID:  "branch-idem",
		AgentID:   "engineer", AgentType: "engineer",
		EventType: EventTypeDecisionRecorded,
		Family:    TreeFamilyDecision,
		Title:     "Idempotent",
	}
	if err := forest.AppendEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	_ = forest.WaitForBranchSeq(ctx, event.Seq, 5*time.Second)

	if _, err := forest.Retrieve(ctx, Query{SessionID: "sess-idem", Query: "Idem", Limit: 1}); err != nil {
		t.Fatal(err)
	}
	_ = forest.WaitForRetrievalAuditDrain(ctx, 5*time.Second)
	_ = waitForCandidatesProjection(forest, 5*time.Second)

	forest.implicitNegativeHorizon = -1 * time.Second

	if err := forest.runImplicitNegativeSweepOnce(ctx); err != nil {
		t.Fatal(err)
	}
	stats1, _ := forest.TrainingLabelStatsSince(ctx, time.Now().Add(-1*time.Hour))

	if err := forest.runImplicitNegativeSweepOnce(ctx); err != nil {
		t.Fatal(err)
	}
	stats2, _ := forest.TrainingLabelStatsSince(ctx, time.Now().Add(-1*time.Hour))

	if stats2.ImplicitNegative != stats1.ImplicitNegative {
		t.Errorf("sweep not idempotent: implicit_negative count changed %d → %d",
			stats1.ImplicitNegative, stats2.ImplicitNegative)
	}
}

// TestSelectionBias_TrainingLabelStatsAggregates exercises the
// diagnostics surface — sum of all four sources.
func TestSelectionBias_TrainingLabelStatsAggregates(t *testing.T) {
	forest, _ := newTestForest(t)
	ctx := context.Background()

	stats, err := forest.TrainingLabelStatsSince(ctx, time.Now().Add(-1*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if stats.Explicit < 0 || stats.Counterfactual < 0 ||
		stats.ImplicitNegative < 0 || stats.Exploration < 0 {
		t.Errorf("negative counts not allowed: %+v", stats)
	}
}

// TestSelectionBias_ResolverDefaults verifies the resolve* helpers
// substitute defaults when zero-valued config fields arrive.
func TestSelectionBias_ResolverDefaults(t *testing.T) {
	if got := resolveCounterfactualLabelWeight(0); got != defaultCounterfactualLabelWeight {
		t.Errorf("counterfactual weight default: got %v want %v", got, defaultCounterfactualLabelWeight)
	}
	if got := resolveCounterfactualWindow(0); got != defaultCounterfactualWindow {
		t.Errorf("counterfactual window default: got %v want %v", got, defaultCounterfactualWindow)
	}
	if got := resolveImplicitNegativeWeight(0); got != defaultImplicitNegativeWeight {
		t.Errorf("implicit negative weight default: got %v", got)
	}
	if got := resolveImplicitNegativeHorizon(0); got != defaultImplicitNegativeHorizon {
		t.Errorf("implicit negative horizon default: got %v", got)
	}
	if got := resolveImplicitNegativeSweepInterval(0); got != defaultImplicitNegativeSweepInterval {
		t.Errorf("implicit negative sweep interval default: got %v", got)
	}
	if got := resolveExplorationRate(-1); got != defaultExplorationRate {
		t.Errorf("exploration rate default for negative: got %v", got)
	}
	if got := resolveExplorationRate(1.5); got != 1.0 {
		t.Errorf("exploration rate clamp >1: got %v want 1.0", got)
	}
	if got := resolveExplorationLabelWeight(0); got != defaultExplorationLabelWeight {
		t.Errorf("exploration label weight default: got %v", got)
	}
}

// ────────────────────────────────────────────────────────────────────
// Helpers
// ────────────────────────────────────────────────────────────────────

// waitForCandidatesProjection polls forest_retrieval_candidates until
// at least one row exists, or the timeout elapses. The projector is
// async; test-side polling is acceptable for these tests because
// they're not asserting timing.
func waitForCandidatesProjection(forest *MemoryForest, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		var count int
		row := forest.db.QueryRow(`SELECT COUNT(*) FROM forest_retrieval_candidates`)
		if err := row.Scan(&count); err == nil && count > 0 {
			return nil
		}
		if time.Now().After(deadline) {
			return errSweepTimeout
		}
		time.Sleep(10 * time.Millisecond)
	}
}

var errSweepTimeout = wrapErr("timeout waiting for retrieval candidates projection")

func wrapErr(msg string) error { return &simpleErr{msg} }

type simpleErr struct{ msg string }

func (e *simpleErr) Error() string { return e.msg }
