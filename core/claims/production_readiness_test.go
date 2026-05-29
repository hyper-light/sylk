package claims

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBuildProductionReadinessReportRequiresAllEvidence(t *testing.T) {
	report := BuildProductionReadinessReport(ProductionReadinessRequest{
		Evidence: []ProductionReadinessEvidence{
			{Category: ReadinessEvidenceUnit, Reference: "go test ./core/claims", Status: "passed"},
		},
	})
	if report.Data.Ready {
		t.Fatal("report should not be ready with missing evidence")
	}
	if len(report.Data.Missing) != len(RequiredProductionReadinessEvidence())-1 {
		t.Fatalf("missing = %v", report.Data.Missing)
	}
}

func TestBuildProductionReadinessArtifactHappyPath(t *testing.T) {
	artifact, report, err := BuildProductionReadinessArtifact(ProductionReadinessRequest{Evidence: completeReadinessEvidence()})
	if err != nil {
		t.Fatalf("BuildProductionReadinessArtifact: %v", err)
	}
	if !report.Data.Ready || len(report.Data.Missing) != 0 {
		t.Fatalf("report = %+v", report.Data)
	}
	if artifact.DataType != ArtifactDataTypeProductionReadiness || artifact.ContentHash == "" {
		t.Fatalf("artifact missing typed payload: %+v", artifact)
	}
	decoded, err := ArtifactData[ProductionReadinessArtifactData](artifact)
	if err != nil {
		t.Fatalf("ArtifactData: %v", err)
	}
	if !decoded.Ready || len(decoded.Evidence) != len(RequiredProductionReadinessEvidence()) {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestBuildProductionReadinessArtifactRejectsInvalidWaiver(t *testing.T) {
	_, report, err := BuildProductionReadinessArtifact(ProductionReadinessRequest{
		Evidence: []ProductionReadinessEvidence{{Category: ReadinessEvidenceUnit, Reference: "unit", Status: "passed"}},
		Waivers:  []ProductionReadinessWaiver{{Owner: "release", Scope: ReadinessEvidenceRace}},
	})
	if !errors.Is(err, ErrProductionReadinessInvalid) {
		t.Fatalf("error = %v, want ErrProductionReadinessInvalid", err)
	}
	if len(report.Invalid) == 0 {
		t.Fatalf("invalid waiver was not reported: %+v", report)
	}
}

func TestProductionReadinessWaiverCanCoverScopedEvidence(t *testing.T) {
	req := ProductionReadinessRequest{
		Evidence: evidenceExcept(ReadinessEvidenceRollback),
		Waivers: []ProductionReadinessWaiver{{
			Owner:               "release",
			Scope:               ReadinessEvidenceRollback,
			Reason:              "staging environment unavailable",
			ExpiresAt:           time.Now().UTC().Add(time.Hour),
			CompensatingControl: "manual WAL restore drill attached",
		}},
	}
	report := BuildProductionReadinessReport(req)
	if !report.Data.Ready {
		t.Fatalf("waived report should be ready: %+v", report.Data)
	}
}

func TestRecordProductionReadinessEvidenceCommitsTypedArtifact(t *testing.T) {
	board := NewClaimsBoard(ClaimsBoardConfig{BoardID: "readiness-board", SessionID: "session", TaskID: "task"})
	result, report, err := RecordProductionReadinessEvidence(context.Background(), ProductionReadinessRequest{
		Board:    board,
		Evidence: completeReadinessEvidence(),
	})
	if err != nil {
		t.Fatalf("RecordProductionReadinessEvidence: %v", err)
	}
	if result.ClaimID == "" || result.TestamentID == "" || !report.Data.Ready {
		t.Fatalf("result=%+v report=%+v", result, report.Data)
	}
	testament, ok := board.CloneTestament(result.TestamentID)
	if !ok || len(testament.Artifacts) != 1 {
		t.Fatalf("testament not recorded: %+v", testament)
	}
	if _, err := ArtifactData[ProductionReadinessArtifactData](testament.Artifacts[0]); err != nil {
		t.Fatalf("readiness artifact was not typed: %v", err)
	}
}

func completeReadinessEvidence() []ProductionReadinessEvidence {
	evidence := make([]ProductionReadinessEvidence, 0, len(RequiredProductionReadinessEvidence()))
	for _, category := range RequiredProductionReadinessEvidence() {
		evidence = append(evidence, ProductionReadinessEvidence{Category: category, Reference: category + "-artifact", Status: "passed"})
	}
	return evidence
}

func evidenceExcept(skip string) []ProductionReadinessEvidence {
	var evidence []ProductionReadinessEvidence
	for _, category := range RequiredProductionReadinessEvidence() {
		if category == skip {
			continue
		}
		evidence = append(evidence, ProductionReadinessEvidence{Category: category, Reference: category + "-artifact", Status: "passed"})
	}
	return evidence
}
