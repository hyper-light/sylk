package forest

import (
	"context"
	"strings"
	"testing"
	"time"
)

// ────────────────────────────────────────────────────────────────────
// Issue #9 Phase A — Health surface tests
// ────────────────────────────────────────────────────────────────────

func TestHealth_NilForestReturnsUnhealthy(t *testing.T) {
	var f *MemoryForest
	got := f.Health(context.Background())
	if got.Status != HealthStatusUnhealthy {
		t.Errorf("nil forest: got %s want unhealthy", got.Status)
	}
}

func TestHealth_FreshForestStatusOK(t *testing.T) {
	forest, _ := newTestForest(t)
	got := forest.Health(context.Background())
	if got.Status != HealthStatusOK {
		t.Errorf("fresh forest: got %s want ok (subsystems=%v)", got.Status, got.Subsystems)
	}
	if got.Schema.ExpectedHash == "" {
		t.Error("expected non-empty expected schema hash")
	}
	// AppliedHash should match expected after ensureSchema runs.
	if got.Schema.AppliedHash != got.Schema.ExpectedHash {
		t.Errorf("schema drift: applied=%s expected=%s",
			got.Schema.AppliedHash, got.Schema.ExpectedHash)
	}
}

func TestHealth_AggregatorPicksWorst(t *testing.T) {
	cases := []struct {
		name     string
		input    HealthSnapshot
		expected HealthStatus
	}{
		{
			"all ok",
			HealthSnapshot{
				Subsystems: []SubsystemHealth{{Status: HealthStatusOK}},
				Schema:     SchemaHealth{Status: HealthStatusOK},
				SpotChecks: SpotCheckResults{Status: HealthStatusOK},
			},
			HealthStatusOK,
		},
		{
			"subsystem degraded",
			HealthSnapshot{
				Subsystems: []SubsystemHealth{{Status: HealthStatusDegraded}, {Status: HealthStatusOK}},
				Schema:     SchemaHealth{Status: HealthStatusOK},
				SpotChecks: SpotCheckResults{Status: HealthStatusOK},
			},
			HealthStatusDegraded,
		},
		{
			"schema unhealthy beats degraded subsystem",
			HealthSnapshot{
				Subsystems: []SubsystemHealth{{Status: HealthStatusDegraded}},
				Schema:     SchemaHealth{Status: HealthStatusUnhealthy},
				SpotChecks: SpotCheckResults{Status: HealthStatusOK},
			},
			HealthStatusUnhealthy,
		},
		{
			"high p99 → degraded",
			HealthSnapshot{
				Subsystems:     []SubsystemHealth{{Status: HealthStatusOK}},
				Schema:         SchemaHealth{Status: HealthStatusOK},
				SpotChecks:     SpotCheckResults{Status: HealthStatusOK},
				LatencyP99µs:   600_000, // > healthLatencyDegradeP99
				LatencySamples: 10,
			},
			HealthStatusDegraded,
		},
		{
			"p99 > unhealthy threshold",
			HealthSnapshot{
				Subsystems:     []SubsystemHealth{{Status: HealthStatusOK}},
				Schema:         SchemaHealth{Status: HealthStatusOK},
				SpotChecks:     SpotCheckResults{Status: HealthStatusOK},
				LatencyP99µs:   3_000_000, // > healthLatencyUnhealthyP99
				LatencySamples: 10,
			},
			HealthStatusUnhealthy,
		},
		{
			"high p99 with no samples is ignored",
			HealthSnapshot{
				Subsystems:     []SubsystemHealth{{Status: HealthStatusOK}},
				Schema:         SchemaHealth{Status: HealthStatusOK},
				SpotChecks:     SpotCheckResults{Status: HealthStatusOK},
				LatencyP99µs:   3_000_000,
				LatencySamples: 0,
			},
			HealthStatusOK,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := aggregateHealthStatus(c.input); got != c.expected {
				t.Errorf("got %s want %s", got, c.expected)
			}
		})
	}
}

func TestHealth_ProjectorClassification(t *testing.T) {
	now := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name     string
		health   string
		errorAt  time.Time
		expected HealthStatus
	}{
		{"running, no error", string(ProjectorHealthRunning), time.Time{}, HealthStatusOK},
		{"halted", string(ProjectorHealthHalted), time.Time{}, HealthStatusUnhealthy},
		{"recent error", string(ProjectorHealthRunning), now.Add(-time.Minute), HealthStatusDegraded},
		{"stale error (older than window)", string(ProjectorHealthRunning), now.Add(-time.Hour), HealthStatusOK},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := classifyProjectorStatus(c.health, c.errorAt, now)
			if got != c.expected {
				t.Errorf("got %s want %s", got, c.expected)
			}
		})
	}
}

func TestHealth_PercentileOfSorted(t *testing.T) {
	samples := []int64{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	cases := []struct {
		q    float64
		want int64
	}{
		{0.0, 1},
		{0.5, 6}, // index = int(0.5*10) = 5 → samples[5] = 6
		{0.95, 10},
		{0.99, 10},
		{1.0, 10},
		{-0.1, 1}, // clamps to 0
		{2.0, 10}, // clamps to last
	}
	for _, c := range cases {
		got := percentileOfSorted(samples, c.q)
		if got != c.want {
			t.Errorf("q=%v: got %d want %d", c.q, got, c.want)
		}
	}
	if got := percentileOfSorted(nil, 0.5); got != 0 {
		t.Errorf("empty input: got %d want 0", got)
	}
}

func TestHealth_SchemaDriftDetection(t *testing.T) {
	forest, db := newTestForest(t)
	ctx := context.Background()

	// Tamper with the recorded schema_hash so it diverges from the
	// expected one.
	if _, err := db.Exec(`
		UPDATE forest_schema_versions SET schema_hash = 'tampered'
	`); err != nil {
		t.Fatal(err)
	}

	got := forest.probeSchemaHealth(ctx)
	if got.Status != HealthStatusUnhealthy {
		t.Errorf("drift detection: got %s want unhealthy", got.Status)
	}
	if got.AppliedHash != "tampered" {
		t.Errorf("applied hash: got %q want tampered", got.AppliedHash)
	}
	if got.ExpectedHash == got.AppliedHash {
		t.Error("expected != applied should differ")
	}
}

func TestHealth_MissingTriggersDetected(t *testing.T) {
	forest, db := newTestForest(t)
	ctx := context.Background()

	// Drop one of the append-only triggers to simulate a partial
	// migration.
	if _, err := db.Exec(`DROP TRIGGER IF EXISTS forest_events_no_update`); err != nil {
		t.Fatal(err)
	}
	got := forest.probeSchemaHealth(ctx)
	if got.Status != HealthStatusUnhealthy {
		t.Errorf("missing trigger: got %s want unhealthy", got.Status)
	}
	found := false
	for _, name := range got.MissingTriggers {
		if name == "forest_events_no_update" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected missing trigger reported; got %v", got.MissingTriggers)
	}
}

func TestHealth_SpotCheckDetectsCorruption(t *testing.T) {
	forest, db := newTestForest(t)
	ctx := context.Background()

	// Seed a branch through the normal append path so its
	// support_count is correct.
	event := &Event{
		SessionID: "sess-spot",
		BranchID:  "branch-spot",
		AgentID:   "engineer", AgentType: "engineer",
		EventType: EventTypeDecisionRecorded,
		Family:    TreeFamilyDecision,
		Title:     "spot",
	}
	if err := forest.AppendEvent(ctx, event); err != nil {
		t.Fatal(err)
	}

	// Deliberately corrupt the projection: set support_count to a
	// value that doesn't match the ledger.
	if _, err := db.Exec(`UPDATE forest_branches SET support_count = 999 WHERE id = ?`, "branch-spot"); err != nil {
		t.Fatal(err)
	}

	got := forest.runSpotChecks(ctx, 32)
	if got.Mismatched == 0 {
		t.Errorf("expected mismatch; sampled=%d mismatched=0", got.Sampled)
	}
	hasOurBranch := false
	for _, m := range got.Mismatches {
		if m.BranchID == "branch-spot" {
			hasOurBranch = true
			if !strings.Contains(m.Detail, "support_count") {
				t.Errorf("expected support_count in detail; got %q", m.Detail)
			}
		}
	}
	if !hasOurBranch {
		t.Errorf("our corrupted branch not in mismatches list: %+v", got.Mismatches)
	}
}

func TestHealth_StatusSeverityOrder(t *testing.T) {
	if healthStatusSeverity(HealthStatusOK) >= healthStatusSeverity(HealthStatusDegraded) {
		t.Error("ok severity should be less than degraded")
	}
	if healthStatusSeverity(HealthStatusDegraded) >= healthStatusSeverity(HealthStatusUnhealthy) {
		t.Error("degraded severity should be less than unhealthy")
	}
}

// TestHealth_SpotCheckIgnoresReplayEvents is a regression test:
// EventTypeReplayPromoted and EventTypeReplayConsolidated do NOT
// increment SupportCount in the branch projector. The spot-check
// SQL must exclude them from its support_count re-derivation, or
// every branch with replay events triggers a false-positive
// mismatch.
func TestHealth_SpotCheckIgnoresReplayEvents(t *testing.T) {
	forest, db := newTestForest(t)
	ctx := context.Background()

	// Seed one decision (support_count == 1).
	if err := forest.AppendEvent(ctx, &Event{
		SessionID: "sess-replay",
		BranchID:  "branch-replay",
		AgentID:   "engineer", AgentType: "engineer",
		EventType: EventTypeDecisionRecorded,
		Family:    TreeFamilyDecision,
		Title:     "decision",
	}); err != nil {
		t.Fatal(err)
	}

	// Inject a replay-promoted event directly into forest_events
	// (the test forest's projector applies events in-line). The
	// branch's support_count should remain 1; spot-check should not
	// flag a mismatch.
	now := time.Now().UTC().Unix()
	if _, err := db.Exec(`
		INSERT INTO forest_events
		(id, session_id, event_type, family, scope, root_id, branch_id,
		 confidence, salience, timestamp, title)
		VALUES ('replay-evt', 'sess-replay', ?, ?, 'episodic', 'r', 'branch-replay',
		        0.5, 0.5, ?, 'replay')
	`, string(EventTypeReplayPromoted), string(TreeFamilyDecision), now); err != nil {
		t.Fatal(err)
	}

	// Run the spot-check on this exact branch.
	branch := spotCheckBranch{
		id:           "branch-replay",
		supportCount: 1, // 1 decision; replay doesn't increment
		counterCount: 0,
	}
	_, mismatch := forest.spotCheckOneBranch(ctx, &branch)
	if mismatch {
		t.Errorf("replay event should not produce a mismatch; got mismatch")
	}
}
