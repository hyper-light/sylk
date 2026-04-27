package forest

import (
	"context"
	"testing"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Issue #6 — AntiPattern family + anti-precedent quota + promotion
// ────────────────────────────────────────────────────────────────────

// TestAntiPattern_ResolverDefaults exercises the resolve helpers.
func TestAntiPattern_ResolverDefaults(t *testing.T) {
	if got := resolveAntiPatternFailureThreshold(0); got != defaultAntiPatternFailureThreshold {
		t.Errorf("failure threshold default: got %v", got)
	}
	if got := resolveAntiPatternFailureRate(0); got != defaultAntiPatternFailureRate {
		t.Errorf("failure rate default: got %v", got)
	}
	if got := resolveAntiPatternFailureRate(1.5); got != 1.0 {
		t.Errorf("failure rate clamp: got %v", got)
	}
	if got := resolveAntiPatternPromotionInterval(0); got != defaultAntiPatternPromotionInterval {
		t.Errorf("interval default: got %v", got)
	}
}

// TestAntiPattern_FailureRateBounded verifies the bounding behavior.
func TestAntiPattern_FailureRateBounded(t *testing.T) {
	cases := []struct {
		name   string
		s, f   int
		expect float64
	}{
		{"empty", 0, 0, 0},
		{"all failures", 0, 5, 1.0},
		{"all successes", 5, 0, 0},
		{"half", 5, 5, 0.5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			b := &Branch{SuccessCount: c.s, FailureCount: c.f}
			if got := failureRate(b); got != c.expect {
				t.Errorf("failureRate(%d/%d) = %v, want %v",
					c.s, c.f, got, c.expect)
			}
		})
	}
}

// TestAntiPattern_SalienceClampedAtOne verifies salience never
// exceeds 1.0 even with absurdly high failure counts.
func TestAntiPattern_SalienceClampedAtOne(t *testing.T) {
	source := &Branch{
		Salience:     1.0,
		FailureCount: 100000,
	}
	got := computeAntiPatternSalience(source)
	if got > 1.0 {
		t.Errorf("salience > 1.0: got %v", got)
	}
}

// TestAntiPattern_SalienceFloorWithZeroSource ensures we don't return
// 0 for a branch with no salience baseline.
func TestAntiPattern_SalienceFloorWithZeroSource(t *testing.T) {
	got := computeAntiPatternSalience(&Branch{Salience: 0, FailureCount: 0})
	if got <= 0 {
		t.Errorf("expected non-zero floor; got %v", got)
	}
}

// TestAntiPattern_BuildProjection verifies the AntiPattern branch
// shape: proper ID, parent link, family, scope, metadata.
func TestAntiPattern_BuildProjection(t *testing.T) {
	source := &Branch{
		ID:           "src",
		RootID:       "root",
		Family:       TreeFamilyOutcome,
		Title:        "did the bad thing",
		Summary:      "specifics",
		AgentType:    "engineer",
		Salience:     0.4,
		SuccessCount: 1,
		FailureCount: 5,
	}
	now := time.Now().UTC()
	ap := buildAntiPatternProjection(source, now)
	if ap.ParentID != "src" {
		t.Errorf("parent_id: got %s want src", ap.ParentID)
	}
	if ap.Family != TreeFamilyAntiPattern {
		t.Errorf("family: got %s want antipattern", ap.Family)
	}
	if ap.Scope != ScopeSemantic {
		t.Errorf("scope: got %s want semantic", ap.Scope)
	}
	if ap.SessionID != "" {
		t.Errorf("session: got %s want '' (cross-session)", ap.SessionID)
	}
	if ap.FailureCount != 0 {
		t.Errorf("antipattern shouldn't carry source counters; got fc=%d", ap.FailureCount)
	}
	if ap.RootID != "root" {
		t.Errorf("root_id: got %s want root", ap.RootID)
	}
	if ap.Metadata["source_branch_id"] != "src" {
		t.Errorf("metadata.source_branch_id: got %v want src", ap.Metadata["source_branch_id"])
	}
}

// TestAntiPattern_QuotaDoesNotExceedHalfPool verifies the slot count
// never displaces more than half the result.
func TestAntiPattern_QuotaDoesNotExceedHalfPool(t *testing.T) {
	cases := []struct {
		name             string
		limit, available int
		expect           int
	}{
		{"limit 1 → 0 slots (ratio kills it)", 1, 5, 0},
		{"limit 8 → 2 slots", 8, 8, 2},
		{"available 3 → 1 slot (half cap)", 8, 3, 1},
		{"available 1 → 0 slots", 8, 1, 0},
		{"limit 16 → 2 slots (capped)", 16, 16, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := antiPrecedentSlotCount(c.limit, c.available); got != c.expect {
				t.Errorf("slot count: got %d want %d", got, c.expect)
			}
		})
	}
}

// TestAntiPattern_QuotaSwapPreservesTopOfList verifies that anti-
// precedent insertions replace ONLY the bottom-N of the result, not
// the top.
func TestAntiPattern_QuotaSwapPreservesTopOfList(t *testing.T) {
	primary := []*BranchPacket{
		{Branch: &Branch{ID: "p1"}},
		{Branch: &Branch{ID: "p2"}},
		{Branch: &Branch{ID: "p3"}},
		{Branch: &Branch{ID: "p4"}},
	}
	insertions := []*BranchPacket{
		{Branch: &Branch{ID: "ap1"}, Source: RetrievalSourceAntiPrecedent},
		{Branch: &Branch{ID: "ap2"}, Source: RetrievalSourceAntiPrecedent},
	}
	out := swapBottomKWithAntiPrecedents(primary, insertions, 2)
	if len(out) != 4 {
		t.Fatalf("output len: got %d want 4", len(out))
	}
	if out[0].Branch.ID != "p1" || out[1].Branch.ID != "p2" {
		t.Errorf("top preserved: got [%s,%s]", out[0].Branch.ID, out[1].Branch.ID)
	}
	if out[2].Branch.ID != "ap1" || out[3].Branch.ID != "ap2" {
		t.Errorf("bottom replaced: got [%s,%s]", out[2].Branch.ID, out[3].Branch.ID)
	}
}

// TestAntiPattern_PromotionScannerEligibilityCheck verifies the
// scanner only picks branches above both thresholds. Exercises the
// SQL eligibility query.
func TestAntiPattern_PromotionScannerEligibilityCheck(t *testing.T) {
	forest, db := newTestForest(t)
	ctx := context.Background()

	// Insert directly into forest_branches so we control counters
	// without needing the projector to apply outcome events.
	cases := []struct {
		id           string
		fc, sc       int
		expectScanned bool
	}{
		{"low-fc", 1, 0, false},                        // below threshold
		{"low-rate", 4, 6, false},                      // below failure rate
		{"qualifies", 5, 1, true},                       // both thresholds met
	}
	now := time.Now().UTC()
	for _, c := range cases {
		_, err := db.Exec(`
			INSERT INTO forest_branches
			(id, root_id, parent_id, family, scope, state, session_id, task_id, agent_id, agent_type,
			 intent_id, title, summary, confidence, salience, utility, success_rate, scope_risk,
			 conflict_score, support_count, counter_count, success_count, failure_count,
			 access_count, last_accessed_at, created_at, updated_at, metadata, last_applied_seq)
			VALUES (?, ?, '', ?, ?, ?, '', '', '', '', '', ?, '', 0.5, 0.5, 0, 0, 0, 0, 1, 0, ?, ?, 0, ?, ?, ?, '{}', 0)
		`, c.id, c.id+"-root", string(TreeFamilyOutcome), string(ScopeSemantic), string(BranchStateActive),
			c.id, c.sc, c.fc, now.Unix(), now.Unix(), now.Unix())
		if err != nil {
			t.Fatalf("insert %s: %v", c.id, err)
		}
	}

	// Set thresholds explicitly: failure_count >= 5, failure_rate >= 0.6.
	forest.antiPatternFailureThreshold = 5
	forest.antiPatternFailureRate = 0.6

	candidates, err := forest.loadAntiPatternPromotionCandidates(ctx, 32)
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]struct{}, len(candidates))
	for _, c := range candidates {
		got[c.ID] = struct{}{}
	}
	for _, c := range cases {
		_, found := got[c.id]
		if found != c.expectScanned {
			t.Errorf("%s: scanned=%v want %v", c.id, found, c.expectScanned)
		}
	}
}

// TestAntiPattern_PromotionIdempotentViaNotExists verifies running
// the scanner twice doesn't create two AntiPattern rows for the same
// source.
func TestAntiPattern_PromotionIdempotentViaNotExists(t *testing.T) {
	forest, db := newTestForest(t)
	ctx := context.Background()

	now := time.Now().UTC()
	_, err := db.Exec(`
		INSERT INTO forest_branches
		(id, root_id, parent_id, family, scope, state, session_id, task_id, agent_id, agent_type,
		 intent_id, title, summary, confidence, salience, utility, success_rate, scope_risk,
		 conflict_score, support_count, counter_count, success_count, failure_count,
		 access_count, last_accessed_at, created_at, updated_at, metadata, last_applied_seq)
		VALUES ('src1', 'root1', '', ?, ?, ?, '', '', '', '', '', 'lossy', '', 0.5, 0.5, 0, 0, 0, 0, 1, 0, 1, 5, 0, ?, ?, ?, '{}', 0)
	`, string(TreeFamilyOutcome), string(ScopeSemantic), string(BranchStateActive),
		now.Unix(), now.Unix(), now.Unix())
	if err != nil {
		t.Fatal(err)
	}

	forest.antiPatternFailureThreshold = 5
	forest.antiPatternFailureRate = 0.6

	for i := 0; i < 3; i++ {
		if _, err := forest.runAntiPatternPromotionOnce(ctx); err != nil {
			t.Fatalf("iter %d: %v", i, err)
		}
	}

	var apCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM forest_branches WHERE family = ? AND parent_id = 'src1'`, string(TreeFamilyAntiPattern)).Scan(&apCount); err != nil {
		t.Fatal(err)
	}
	if apCount != 1 {
		t.Errorf("expected exactly one AntiPattern; got %d", apCount)
	}
}

// TestAntiPattern_LoadAntiPrecedentBranchesScopedByAgentType ensures
// the anti-precedent loader respects the agent_type filter while
// allowing globally-scoped (empty agent_type) branches through.
func TestAntiPattern_LoadAntiPrecedentBranchesScopedByAgentType(t *testing.T) {
	forest, db := newTestForest(t)
	ctx := context.Background()

	now := time.Now().UTC()
	insert := func(id, agentType string, fc, sc int) {
		t.Helper()
		_, err := db.Exec(`
			INSERT INTO forest_branches
			(id, root_id, parent_id, family, scope, state, session_id, task_id, agent_id, agent_type,
			 intent_id, title, summary, confidence, salience, utility, success_rate, scope_risk,
			 conflict_score, support_count, counter_count, success_count, failure_count,
			 access_count, last_accessed_at, created_at, updated_at, metadata, last_applied_seq)
			VALUES (?, ?, '', ?, ?, ?, '', '', '', ?, '', ?, '', 0.5, 0.5, 0, 0, 0, 0, 1, 0, ?, ?, 0, ?, ?, ?, '{}', 0)
		`, id, id+"-root", string(TreeFamilyOutcome), string(ScopeSemantic), string(BranchStateActive),
			agentType, id, sc, fc, now.Unix(), now.Unix(), now.Unix())
		if err != nil {
			t.Fatalf("insert %s: %v", id, err)
		}
	}
	insert("eng-fail", "engineer", 5, 1)  // engineer-scoped, failed
	insert("global-fail", "", 5, 1)        // global, failed
	insert("designer-fail", "designer", 5, 1) // wrong agent type

	branches, err := forest.loadAntiPrecedentBranches(ctx, Query{
		AgentType: "engineer",
	}, 5, map[string]struct{}{})
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]struct{}, len(branches))
	for _, b := range branches {
		got[b.ID] = struct{}{}
	}
	if _, ok := got["eng-fail"]; !ok {
		t.Errorf("expected engineer-scoped result")
	}
	if _, ok := got["global-fail"]; !ok {
		t.Errorf("expected globally-scoped result")
	}
	if _, ok := got["designer-fail"]; ok {
		t.Errorf("designer result leaked through engineer filter")
	}
}
