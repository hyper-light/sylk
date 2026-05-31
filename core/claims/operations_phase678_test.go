package claims_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/claims"
)

func TestClaimsMetricCatalogCoversOperationsSpecNames(t *testing.T) {
	catalog := claims.ClaimsMetricCatalog()
	if err := claims.ValidateClaimsMetricCatalog(catalog); err != nil {
		t.Fatalf("ValidateClaimsMetricCatalog: %v", err)
	}
	for _, name := range []string{
		"claims_board_transitions_total",
		"claims_board_wal_write_total",
		"claims_delta_emitted_total",
		"claims_claim_generated_total",
		"claims_validation_dispatched_total",
		"claims_dispatcher_queue_depth",
		"claims_cancellation_initiated_total",
		"claims_recovery_initiated_total",
		"claims_boot_phase_completed_total",
		"claims_session_count",
	} {
		if !metricCatalogHas(catalog, name) {
			t.Fatalf("metric catalog missing %s", name)
		}
	}
}

func TestClaimsMetricsSinkIsBoundedAndBoardEmitsLifecycle(t *testing.T) {
	boundedSink := claims.NewInMemoryClaimsMetricsSink(1)
	for _, name := range []string{"first", "second"} {
		if err := boundedSink.ObserveClaimsMetric(context.Background(), claims.ClaimsMetric{Name: name, Kind: claims.ClaimsMetricCounter}); err != nil {
			t.Fatalf("ObserveClaimsMetric: %v", err)
		}
	}
	if snap := boundedSink.Snapshot(); len(snap.Records) != 1 || snap.Dropped != 1 {
		t.Fatalf("bounded sink snapshot = %+v, want one retained and one dropped", snap)
	}

	sink := claims.NewInMemoryClaimsMetricsSink(16)
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "metrics-board", SessionID: "metrics-session", TaskID: "task", Metrics: sink})
	if err := board.PostAction(context.Background(), claims.Action{AgentID: "issuer", Type: claims.ActionTypeTask}, []claims.Claim{{
		ID:         "claim-1",
		Title:      "metrics claim",
		ActionType: claims.ActionTypeTask,
		Relations: []claims.Relation{
			{Related: "issuer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: "worker", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: []*claims.Validation{{ID: "receipt", Type: claims.ValidationTypeReceipt, Required: true, Description: "receipt", QualityBar: "received"}},
	}}); err != nil {
		t.Fatalf("PostAction: %v", err)
	}
	snap := sink.Snapshot()
	if len(snap.Records) == 0 {
		t.Fatalf("records = 0, want lifecycle metrics")
	}
	if !metricsSnapshotHas(snap, "claims_board_transitions_total") {
		t.Fatalf("metrics missing board transition: %+v", snap)
	}
}

func TestClaimsOperationsStatusAndRepairSkills(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "ops-board", SessionID: "ops-session", TaskID: "task"})
	cfg := claims.ClaimsOperationsSkillConfig{
		BoardProvider: func() (*claims.ClaimsBoard, error) { return board, nil },
	}
	statusSkill := claims.ClaimsOperationsStatusSkill(cfg)
	out, err := statusSkill.Handler(context.Background(), json.RawMessage(`{"include_audit":true}`))
	if err != nil {
		t.Fatalf("status skill: %v", err)
	}
	if _, ok := out.(claims.ClaimsOperationsStatus); !ok {
		t.Fatalf("status type = %T, want ClaimsOperationsStatus", out)
	}
	repairSkill := claims.ClaimsOperationsRepairSkill(cfg)
	_, err = repairSkill.Handler(context.Background(), json.RawMessage(`{"operation":"outbox_projection","dry_run":false}`))
	if err == nil || !strings.Contains(err.Error(), "authorized=true") {
		t.Fatalf("repair unauthorized err = %v, want authorization error", err)
	}
}

func TestProductionReadinessRequiresEveryEvidenceKind(t *testing.T) {
	report := claims.BuildProductionReadinessReport(claims.ProductionReadinessRequest{})
	if report.Data.Ready || len(report.Data.Missing) == 0 {
		t.Fatalf("report = %+v, want missing evidence", report.Data)
	}
	evidence := make([]claims.ClaimsOperationsReadinessEvidence, 0, len(claims.RequiredProductionReadinessEvidence()))
	for _, kind := range claims.RequiredProductionReadinessEvidence() {
		evidence = append(evidence, claims.ClaimsOperationsReadinessEvidence{Kind: kind, Passed: true, Reference: "test"})
	}
	report = claims.BuildProductionReadinessReport(claims.ProductionReadinessRequest{Evidence: claims.ClaimsOperationsReadinessEvidenceAsProduction(evidence)})
	if !report.Data.Ready || len(report.Data.Missing) != 0 {
		t.Fatalf("report = %+v, want ready", report.Data)
	}
	_, _, err := claims.BuildProductionReadinessArtifact(claims.ProductionReadinessRequest{Evidence: claims.ClaimsOperationsReadinessEvidenceAsProduction(evidence)})
	if err != nil {
		t.Fatalf("BuildProductionReadinessArtifact: %v", err)
	}
}

func TestOperationsInventoryHasNoDeferredPhase678Surfaces(t *testing.T) {
	for _, entry := range claims.OperationsInventory() {
		switch entry.Requirement {
		case "ops.telemetry.exporters", "ops.ui.observer_intake", "ops.delta.legacy_compatibility", "ops.operator.skills", "ops.runbook.scenarios", "ops.production_readiness":
			if entry.Status != claims.OperationsSurfaceImplemented {
				t.Fatalf("%s status = %s, want implemented", entry.Requirement, entry.Status)
			}
		}
	}
}

func metricCatalogHas(catalog []claims.ClaimsMetricDefinition, name string) bool {
	for _, def := range catalog {
		if def.Name == name {
			return true
		}
	}
	return false
}

func metricsSnapshotHas(snapshot claims.ClaimsMetricsSinkSnapshot, name string) bool {
	for _, record := range snapshot.Records {
		if record.Name == name {
			return true
		}
	}
	return false
}
