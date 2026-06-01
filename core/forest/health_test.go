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

	node := insertPhase789Node(t, db, phase789NodeSpec{
		ID:      "node-spot",
		Session: "sess-spot",
		Kind:    ForestNodeClaim,
		Grade:   EvidenceGradeValidated,
		Seq:     1,
	})

	// Deliberately corrupt the projection with a grade that the node
	// normalizer would never write.
	if _, err := db.Exec(`UPDATE forest_nodes SET evidence_grade = 'not-a-grade' WHERE node_id = ?`, node.ID); err != nil {
		t.Fatal(err)
	}

	got := forest.runSpotChecks(ctx, 32)
	if got.Mismatched == 0 {
		t.Errorf("expected mismatch; sampled=%d mismatched=0", got.Sampled)
	}
	hasOurNode := false
	for _, m := range got.Mismatches {
		if m.NodeID == node.ID {
			hasOurNode = true
			if !strings.Contains(m.Detail, "evidence_grade") {
				t.Errorf("expected evidence_grade in detail; got %q", m.Detail)
			}
		}
	}
	if !hasOurNode {
		t.Errorf("our corrupted node not in mismatches list: %+v", got.Mismatches)
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

func TestHealth_SpotCheckAcceptsFixtureNodesWithoutLedgerSource(t *testing.T) {
	forest, db := newTestForest(t)
	ctx := context.Background()

	node := insertPhase789Node(t, db, phase789NodeSpec{
		ID:      "node-fixture",
		Session: "sess-fixture",
		Kind:    ForestNodeValidation,
		Grade:   EvidenceGradeValidated,
		Seq:     1,
	})
	spotNode := spotCheckNode{
		id:              node.ID,
		kind:            node.Kind,
		sourceKind:      node.SourceKind,
		sourcePartition: node.SourcePartition,
		sourceKey:       node.SourceKey,
		sourceSeq:       node.SourceSeq,
		evidenceGrade:   node.EvidenceGrade,
	}
	_, mismatch := forest.spotCheckOneNode(ctx, &spotNode)
	if mismatch {
		t.Errorf("fixture node should not require a forest_ledger source row")
	}
}
