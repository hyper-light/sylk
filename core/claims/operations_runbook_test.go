package claims_test

import (
	"context"
	"strings"
	"testing"

	"github.com/adalundhe/sylk/core/claims"
)

func TestClaimsOperationsRunbookScenariosAreExecutable(t *testing.T) {
	scenarios := []struct {
		name string
		run  func(*testing.T)
	}{
		{name: "projection_lag_repair", run: runProjectionLagRunbookScenario},
		{name: "inbox_overflow_audit", run: runInboxOverflowRunbookScenario},
		{name: "readiness_review", run: runReadinessReviewRunbookScenario},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, scenario.run)
	}
}

func runProjectionLagRunbookScenario(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "runbook-projection", SessionID: "runbook-session", TaskID: "task"})
	skill := claims.ClaimsOperationsRepairSkill(claims.ClaimsOperationsSkillConfig{
		BoardProvider: func() (*claims.ClaimsBoard, error) { return board, nil },
	})
	out, err := skill.Handler(context.Background(), []byte(`{"operation":"outbox_projection","dry_run":true}`))
	if err != nil {
		t.Fatalf("dry-run repair: %v", err)
	}
	if out == nil {
		t.Fatal("dry-run repair returned nil output")
	}
}

func runInboxOverflowRunbookScenario(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "runbook-overflow", SessionID: "runbook-session", TaskID: "task"})
	result, err := claims.RunClaimsOperationsAudit(context.Background(), claims.OperationsAuditOptions{
		Config:        board.OperationsConfig(),
		Boards:        []*claims.ClaimsBoard{board},
		EvidenceBoard: board,
		SessionID:     board.SessionID(),
	})
	if err != nil {
		t.Fatalf("RunClaimsOperationsAudit: %v", err)
	}
	if result.GeneratedAt.IsZero() || result.Budgets.InboxSubscriptionQueueCap <= 0 {
		t.Fatalf("audit result missing operator evidence: %+v", result)
	}
}

func runReadinessReviewRunbookScenario(t *testing.T) {
	evidence := make([]claims.ClaimsOperationsReadinessEvidence, 0, len(claims.RequiredProductionReadinessEvidence()))
	for _, kind := range claims.RequiredProductionReadinessEvidence() {
		evidence = append(evidence, claims.ClaimsOperationsReadinessEvidence{Kind: kind, Passed: true, Reference: "runbook"})
	}
	report := claims.BuildProductionReadinessReport(claims.ProductionReadinessRequest{Evidence: claims.ClaimsOperationsReadinessEvidenceAsProduction(evidence)})
	if !report.Data.Ready || strings.TrimSpace(report.Phase) == "" {
		t.Fatalf("report = %+v, want ready phase evidence", report)
	}
}
