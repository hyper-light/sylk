package forest

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// Issue #11 Phase 3 — Family taxonomy collapse.
//
// The canonicalization layer is a load-bearing invariant: every author
// boundary (AppendEvent, content-entry routing) and every read boundary
// (normalizeQuery, loadPrimingRootIDs) routes deprecated families into
// their post-Phase-3 successors. This file pins the contract directly
// so a regression in the mapping or in the boundary application surfaces
// here even when the higher-level integration tests still happen to
// pass via other code paths.

func TestCanonicalizeFamily_MapsDeprecatedToCanonical(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input TreeFamily
		want  TreeFamily
	}{
		{TreeFamilyDecision, TreeFamilyIntent},
		{TreeFamilyCapability, TreeFamilyIntent},
		{TreeFamilyOpportunity, TreeFamilyIntent},
		{TreeFamilyPreference, TreeFamilyConstraint},
		{TreeFamilyConflict, TreeFamilyAntiPattern},
	}
	for _, c := range cases {
		if got := canonicalizeFamily(c.input); got != c.want {
			t.Errorf("canonicalizeFamily(%q) = %q, want %q", c.input, got, c.want)
		}
	}
}

func TestCanonicalizeFamily_IdempotentForCanonical(t *testing.T) {
	t.Parallel()

	canonical := []TreeFamily{
		TreeFamilyIntent,
		TreeFamilyConstraint,
		TreeFamilyEvidence,
		TreeFamilyOutcome,
		TreeFamilyAntiPattern,
	}
	for _, family := range canonical {
		if got := canonicalizeFamily(family); got != family {
			t.Errorf("canonicalizeFamily(%q) = %q, want unchanged", family, got)
		}
	}
}

func TestCanonicalizeFamily_PassesThroughUnknown(t *testing.T) {
	t.Parallel()

	unknown := TreeFamily("policy")
	if got := canonicalizeFamily(unknown); got != unknown {
		t.Errorf("canonicalizeFamily(unknown) = %q, want passthrough %q", got, unknown)
	}
	if got := canonicalizeFamily(""); got != "" {
		t.Errorf("canonicalizeFamily(empty) = %q, want empty", got)
	}
}

func TestCanonicalizeFamilies_DedupesAcrossCollapse(t *testing.T) {
	t.Parallel()

	got := canonicalizeFamilies([]TreeFamily{
		TreeFamilyDecision,
		TreeFamilyCapability,
		TreeFamilyOpportunity,
		TreeFamilyConstraint,
		TreeFamilyPreference,
	})
	want := []TreeFamily{TreeFamilyIntent, TreeFamilyConstraint}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("canonicalizeFamilies dedup = %v, want %v", got, want)
	}
}

func TestCanonicalizeFamilies_NilAndEmptyReturnNil(t *testing.T) {
	t.Parallel()

	if got := canonicalizeFamilies(nil); got != nil {
		t.Errorf("canonicalizeFamilies(nil) = %v, want nil", got)
	}
	if got := canonicalizeFamilies([]TreeFamily{}); got != nil {
		t.Errorf("canonicalizeFamilies(empty) = %v, want nil", got)
	}
}

func TestCanonicalizeFamilies_DropsEmptyEntries(t *testing.T) {
	t.Parallel()

	got := canonicalizeFamilies([]TreeFamily{"", TreeFamilyDecision, "", TreeFamilyConflict})
	want := []TreeFamily{TreeFamilyIntent, TreeFamilyAntiPattern}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("canonicalizeFamilies skip-empty = %v, want %v", got, want)
	}
}

func TestDefaultFamilies_IsCanonicalFiveOnly(t *testing.T) {
	t.Parallel()

	got := defaultFamilies()
	want := []TreeFamily{
		TreeFamilyIntent,
		TreeFamilyConstraint,
		TreeFamilyEvidence,
		TreeFamilyOutcome,
		TreeFamilyAntiPattern,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("defaultFamilies() = %v, want %v", got, want)
	}
	for _, family := range got {
		if canonicalizeFamily(family) != family {
			t.Errorf("defaultFamilies() contains non-canonical %q", family)
		}
	}
}

// TestAppendEvent_CanonicalizesFamilyAtWriteBoundary verifies the
// invariant that no deprecated family persists past AppendEvent: a
// caller passing TreeFamilyDecision lands as TreeFamilyIntent in the
// branch projection.
func TestAppendEvent_CanonicalizesFamilyAtWriteBoundary(t *testing.T) {
	t.Skip("legacy branch projection canonicalization removed; node graph projection owns write-boundary semantics")
	t.Parallel()

	forest, db := newTestForest(t)
	defer forest.Close()
	defer db.Close()

	cases := []struct {
		name     string
		input    TreeFamily
		want     TreeFamily
		branchID string
	}{
		{"decision_to_intent", TreeFamilyDecision, TreeFamilyIntent, "branch-decision"},
		{"capability_to_intent", TreeFamilyCapability, TreeFamilyIntent, "branch-capability"},
		{"opportunity_to_intent", TreeFamilyOpportunity, TreeFamilyIntent, "branch-opportunity"},
		{"preference_to_constraint", TreeFamilyPreference, TreeFamilyConstraint, "branch-preference"},
		{"conflict_to_antipattern", TreeFamilyConflict, TreeFamilyAntiPattern, "branch-conflict"},
	}
	for _, c := range cases {
		event := &Event{
			SessionID: "session-canonical",
			AgentID:   "engineer",
			AgentType: "engineer",
			BranchID:  c.branchID,
			Family:    c.input,
			Title:     c.name,
			Summary:   c.name + " summary",
		}
		if err := forest.AppendEvent(context.Background(), event); err != nil {
			t.Fatalf("%s append: %v", c.name, err)
		}
		var family string
		if err := db.QueryRow(`SELECT family FROM forest_branches WHERE id = ?`, c.branchID).Scan(&family); err != nil {
			t.Fatalf("%s scan: %v", c.name, err)
		}
		if TreeFamily(family) != c.want {
			t.Errorf("%s persisted family = %q, want %q", c.name, family, c.want)
		}
	}
}

// TestRetrieve_CanonicalizesFamiliesFilter verifies that a query
// constrained to a deprecated family still matches branches that were
// stored under the canonical successor.
func TestRetrieve_CanonicalizesFamiliesFilter(t *testing.T) {
	t.Skip("legacy branch filter canonicalization removed from public retrieval")
	t.Parallel()

	forest, db := newTestForest(t)
	defer forest.Close()
	defer db.Close()

	// Author with the canonical family directly so the assertion
	// below isolates the *read-side* canonicalization rather than
	// depending on the write-side path.
	event := &Event{
		SessionID: "session-read-canonical",
		AgentID:   "engineer",
		AgentType: "engineer",
		BranchID:  "branch-read-canonical",
		Family:    TreeFamilyAntiPattern,
		Title:     "stored as antipattern",
		Summary:   "antipattern summary text for retrieval",
	}
	if err := forest.AppendEvent(context.Background(), event); err != nil {
		t.Fatalf("append: %v", err)
	}

	packets, err := forest.Retrieve(context.Background(), Query{
		Query:     "antipattern summary",
		SessionID: "session-read-canonical",
		Limit:     4,
		// Legacy filter — pre-Phase-3 callers used Conflict where
		// Phase 3 callers use AntiPattern. The canonicalization at
		// normalizeQuery rewrites this filter so the query still
		// matches the stored AntiPattern branch.
		Families: []TreeFamily{TreeFamilyConflict},
	})
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	if len(packets) == 0 {
		t.Fatal("expected legacy Conflict filter to canonicalize and match AntiPattern branch")
	}
	if got := treeFamilyForNodeKind(packets[0].Node.Kind); got != TreeFamilyAntiPattern {
		t.Fatalf("returned family = %q, want %q", got, TreeFamilyAntiPattern)
	}
}

// TestEnsureForestFamilyMigration_LeavesAppendOnlyLedgerIntact pins the
// MEM-03 invariant that ensureForestFamilyMigration must NOT rewrite
// rows in forest_events. The append-only trigger rejects UPDATE on the
// ledger; an earlier draft of this migration tried to rewrite both the
// projection (forest_branches) and the ledger (forest_events) and
// crashed every fresh process bootstrap that had any pre-Phase-3
// events on disk.
//
// The invariant: the migration migrates the projection only. Old
// ledger rows keep their historical family values; the projector
// canonicalizes on apply so a from-scratch rebuild still produces
// canonical families on the projection.
func TestEnsureForestFamilyMigration_LeavesAppendOnlyLedgerIntact(t *testing.T) {
	t.Parallel()

	forest, db := newTestForest(t)
	defer forest.Close()
	defer db.Close()

	// Insert a forest_events row directly with a deprecated family
	// value. INSERT is permitted by the append-only trigger; UPDATE
	// is not. Bypasses prepareEvent canonicalization so the row lands
	// with the legacy family value still attached.
	now := time.Now().UTC().Unix()
	if _, err := db.Exec(`
		INSERT INTO forest_events
		    (id, session_id, event_type, family, scope, root_id, branch_id,
		     confidence, salience, timestamp, title)
		VALUES (?, 'session-legacy-ledger', 'decision_recorded', 'decision',
		        'episodic', 'root-legacy', 'branch-legacy', 0.5, 0.5, ?, 'Legacy Decision')
	`, "evt-legacy-1", now); err != nil {
		t.Fatalf("seed legacy event row: %v", err)
	}

	// Re-running the migration must not error and must not rewrite
	// the legacy event family. (Idempotency + ledger-immutability.)
	if err := ensureForestFamilyMigration(db); err != nil {
		t.Fatalf("re-run migration: %v", err)
	}

	var ledgerFamily string
	if err := db.QueryRow(`SELECT family FROM forest_events WHERE id = ?`, "evt-legacy-1").Scan(&ledgerFamily); err != nil {
		t.Fatalf("scan ledger family: %v", err)
	}
	if ledgerFamily != "decision" {
		t.Errorf("ledger family = %q, want %q (append-only invariant)", ledgerFamily, "decision")
	}
}

// TestEnsureForestFamilyMigration_MigratesProjection verifies the
// other half of the invariant: forest_branches DOES get rewritten so
// downstream queries see canonical families.
func TestEnsureForestFamilyMigration_MigratesProjection(t *testing.T) {
	t.Parallel()

	forest, db := newTestForest(t)
	defer forest.Close()
	defer db.Close()

	// Seed the projection directly with a deprecated family. (Bypasses
	// upsertBranchTx so the row keeps its legacy value.)
	now := time.Now().UTC().Unix()
	if _, err := db.Exec(`
		INSERT INTO forest_branches
		    (id, root_id, family, scope, state, session_id, title, summary,
		     confidence, salience, utility, success_rate, scope_risk, conflict_score,
		     support_count, counter_count, success_count, failure_count, access_count,
		     created_at, updated_at)
		VALUES (?, ?, 'preference', 'episodic', 'active', 'session-legacy-projection',
		        'Legacy Preference', 'Legacy projection row',
		        0.5, 0.5, 0, 0, 0, 0, 0, 0, 0, 0, 0, ?, ?)
	`, "branch-legacy-pref", "branch-legacy-pref", now, now); err != nil {
		t.Fatalf("seed legacy projection row: %v", err)
	}

	if err := ensureForestFamilyMigration(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var projectionFamily string
	if err := db.QueryRow(`SELECT family FROM forest_branches WHERE id = ?`, "branch-legacy-pref").Scan(&projectionFamily); err != nil {
		t.Fatalf("scan projection family: %v", err)
	}
	if TreeFamily(projectionFamily) != TreeFamilyConstraint {
		t.Errorf("projection family = %q, want %q", projectionFamily, TreeFamilyConstraint)
	}
}
