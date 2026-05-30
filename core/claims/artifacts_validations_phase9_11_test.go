package claims_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/adalundhe/sylk/core/claims"
)

func TestPhase9ArtifactTerminalSuccessAggregatesToTestamentAndClaim(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "phase9-aggregate-success", SessionID: "phase9", TaskID: "task"})
	claimID := phase678PostedClaim(t, board, "architect", "engineer", "")
	artifactID := phase678AttachedPlanArtifact(t, board, claimID, "engineer")
	testament := phase678OnlyTestament(t, board, claimID)
	validation := phase678OnlyValidation(t, board, claimID)

	if err := board.BeginTestamentValidation(context.Background(), testament.ID, "architect"); err != nil {
		t.Fatalf("BeginTestamentValidation: %v", err)
	}
	if err := board.BeginArtifactValidation(context.Background(), artifactID, "architect"); err != nil {
		t.Fatalf("BeginArtifactValidation: %v", err)
	}
	if err := board.BeginValidation(context.Background(), claimID, validation.ID, "architect", artifactID); err != nil {
		t.Fatalf("BeginValidation: %v", err)
	}
	if err := board.CompleteValidationLifecycle(context.Background(), claimID, validation.ID, "architect", claims.ValidationStatusValidated, claims.ValidationLifecycleOptions{
		Reason:           "plan validated",
		TargetArtifactID: artifactID,
	}); err != nil {
		t.Fatalf("CompleteValidationLifecycle: %v", err)
	}
	if err := board.CompleteArtifactValidation(context.Background(), artifactID, "architect", true, nil); err != nil {
		t.Fatalf("CompleteArtifactValidation: %v", err)
	}

	gotTestament, _ := board.CloneTestament(testament.ID)
	if gotTestament.LifecycleStatus != claims.TestamentLifecycleValidated {
		t.Fatalf("testament lifecycle = %s, want validated", gotTestament.LifecycleStatus)
	}
	gotClaim, _ := board.CloneClaim(claimID)
	if gotClaim.LifecycleStatus != claims.ClaimLifecycleSatisfied || gotClaim.Status != claims.ClaimStatusAccepted {
		t.Fatalf("claim = %+v, want satisfied/accepted", gotClaim)
	}
}

func TestPhase11DurableReplayPreservesArtifactAggregatedTerminalOutcome(t *testing.T) {
	cfg := claims.ClaimsBoardConfig{
		BoardID:    "phase11-replay-aggregate",
		SessionID:  "phase11",
		TaskID:     "task",
		SessionDir: filepath.Join(t.TempDir(), "session"),
	}
	db, err := claims.OpenDurableBoard(cfg)
	if err != nil {
		t.Fatalf("OpenDurableBoard: %v", err)
	}
	board := db.Board()
	claimID := phase678PostedClaim(t, board, "architect", "engineer", "")
	artifactID := phase678AttachedPlanArtifact(t, board, claimID, "engineer")
	testament := phase678OnlyTestament(t, board, claimID)
	validation := phase678OnlyValidation(t, board, claimID)
	if err := board.BeginTestamentValidation(context.Background(), testament.ID, "architect"); err != nil {
		t.Fatalf("BeginTestamentValidation: %v", err)
	}
	if err := board.BeginArtifactValidation(context.Background(), artifactID, "architect"); err != nil {
		t.Fatalf("BeginArtifactValidation: %v", err)
	}
	if err := board.BeginValidation(context.Background(), claimID, validation.ID, "architect", artifactID); err != nil {
		t.Fatalf("BeginValidation: %v", err)
	}
	if err := board.CompleteValidationLifecycle(context.Background(), claimID, validation.ID, "architect", claims.ValidationStatusValidated, claims.ValidationLifecycleOptions{
		Reason:           "plan validated",
		TargetArtifactID: artifactID,
	}); err != nil {
		t.Fatalf("CompleteValidationLifecycle: %v", err)
	}
	if err := board.CompleteArtifactValidation(context.Background(), artifactID, "architect", true, nil); err != nil {
		t.Fatalf("CompleteArtifactValidation: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := claims.OpenDurableBoard(cfg)
	if err != nil {
		t.Fatalf("reopen durable board: %v", err)
	}
	defer reopened.Close()
	replayedTestament, ok := reopened.Board().CloneTestament(testament.ID)
	if !ok || replayedTestament.LifecycleStatus != claims.TestamentLifecycleValidated {
		t.Fatalf("replayed testament = %+v ok=%v, want validated", replayedTestament, ok)
	}
	replayedClaim, ok := reopened.Board().CloneClaim(claimID)
	if !ok || replayedClaim.LifecycleStatus != claims.ClaimLifecycleSatisfied {
		t.Fatalf("replayed claim = %+v ok=%v, want satisfied", replayedClaim, ok)
	}
}

func TestPhase9MissingArtifactOutcomePostsBoundedCorrectiveClaimWhenEnabled(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:   "phase9-missing-corrective",
		SessionID: "phase9",
		TaskID:    "task",
		RemediationPolicy: &claims.RemediationPolicy{
			EnableMissingArtifactCorrectives: true,
			MaxCorrectiveClaimsPerOutcome:    1,
		},
	})
	claimID := phase9PostedClaimWithTargets(t, board, []string{"plan", "tests"})
	testamentID := phase9EmptyTestament(t, board, claimID)

	if err := board.BeginTestamentValidation(context.Background(), testamentID, "architect"); err != nil {
		t.Fatalf("BeginTestamentValidation: %v", err)
	}
	if err := board.CompleteTestamentValidation(context.Background(), testamentID, "architect", claims.TestamentLifecycleValidationIncomplete, "required artifact missing"); err != nil {
		t.Fatalf("CompleteTestamentValidation: %v", err)
	}
	if err := board.CompleteTestamentValidation(context.Background(), testamentID, "architect", claims.TestamentLifecycleValidationIncomplete, "required artifact missing"); err != nil {
		t.Fatalf("second CompleteTestamentValidation: %v", err)
	}

	correctives := phase9CorrectiveClaims(board, claimID)
	if len(correctives) != 1 {
		t.Fatalf("corrective claims = %d, want exactly one bounded/idempotent corrective", len(correctives))
	}
	corrective := correctives[0]
	if !claims.HasRelation(corrective.Relations, claims.RelationshipCorrects, claimID) {
		t.Fatalf("corrective missing corrects relation to claim: %+v", corrective.Relations)
	}
	if !claims.HasRelation(corrective.Relations, claims.RelationshipCausedBy, testamentID) {
		t.Fatalf("corrective missing caused_by relation to testament: %+v", corrective.Relations)
	}
}

func TestPhase9ValidationFailureCorrectiveIncludesArtifactSupersessionWhenPolicyEnabled(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:   "phase9-failure-corrective",
		SessionID: "phase9",
		TaskID:    "task",
		RemediationPolicy: &claims.RemediationPolicy{
			EnableValidationFailureCorrectives: true,
			MaxCorrectiveClaimsPerOutcome:      2,
		},
	})
	claimID := phase678PostedClaim(t, board, "architect", "engineer", "")
	artifactID := phase678AttachedPlanArtifact(t, board, claimID, "engineer")
	testament := phase678OnlyTestament(t, board, claimID)
	validation := phase678OnlyValidation(t, board, claimID)

	if err := board.BeginTestamentValidation(context.Background(), testament.ID, "architect"); err != nil {
		t.Fatalf("BeginTestamentValidation: %v", err)
	}
	if err := board.BeginArtifactValidation(context.Background(), artifactID, "architect"); err != nil {
		t.Fatalf("BeginArtifactValidation: %v", err)
	}
	if err := board.BeginValidation(context.Background(), claimID, validation.ID, "architect", artifactID); err != nil {
		t.Fatalf("BeginValidation: %v", err)
	}
	if err := board.CompleteValidationLifecycle(context.Background(), claimID, validation.ID, "architect", claims.ValidationStatusValidationFailed, claims.ValidationLifecycleOptions{
		Reason:           "plan failed",
		TargetArtifactID: artifactID,
		Error:            &claims.ValidationError{Category: claims.ValidationErrorCategoryHandler, Description: "plan failed"},
	}); err != nil {
		t.Fatalf("CompleteValidationLifecycle: %v", err)
	}
	if err := board.CompleteArtifactValidation(context.Background(), artifactID, "architect", false, &claims.ArtifactError{
		Category:    claims.ArtifactErrorCategoryValidation,
		Description: "plan failed",
	}); err != nil {
		t.Fatalf("CompleteArtifactValidation: %v", err)
	}

	gotTestament, _ := board.CloneTestament(testament.ID)
	if gotTestament.LifecycleStatus != claims.TestamentLifecycleValidationFailed {
		t.Fatalf("testament lifecycle = %s, want validation_failed", gotTestament.LifecycleStatus)
	}
	correctives := phase9CorrectiveClaims(board, claimID)
	if len(correctives) != 1 {
		t.Fatalf("corrective claims = %d, want 1", len(correctives))
	}
	if !claims.HasRelation(correctives[0].Relations, claims.RelationshipSupersedes, artifactID) {
		t.Fatalf("corrective missing supersedes relation to failed artifact: %+v", correctives[0].Relations)
	}
}

func TestPhase9CurrentArtifactInfrastructureErrorAggregatesToErroredCorrective(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:   "phase9-errored-corrective",
		SessionID: "phase9",
		TaskID:    "task",
		RemediationPolicy: &claims.RemediationPolicy{
			EnableValidationErroredCorrectives: true,
			MaxCorrectiveClaimsPerOutcome:      1,
		},
	})
	claimID := phase678PostedClaim(t, board, "architect", "engineer", "")
	artifactID := phase678AttachedPlanArtifact(t, board, claimID, "engineer")
	testament := phase678OnlyTestament(t, board, claimID)
	validation := phase678OnlyValidation(t, board, claimID)

	if err := board.BeginTestamentValidation(context.Background(), testament.ID, "architect"); err != nil {
		t.Fatalf("BeginTestamentValidation: %v", err)
	}
	if err := board.BeginArtifactValidation(context.Background(), artifactID, "architect"); err != nil {
		t.Fatalf("BeginArtifactValidation: %v", err)
	}
	if err := board.BeginValidation(context.Background(), claimID, validation.ID, "architect", artifactID); err != nil {
		t.Fatalf("BeginValidation: %v", err)
	}
	if err := board.CompleteValidationLifecycle(context.Background(), claimID, validation.ID, "architect", claims.ValidationStatusErrored, claims.ValidationLifecycleOptions{
		Reason:           "validator panicked",
		TargetArtifactID: artifactID,
		Error:            &claims.ValidationError{Category: claims.ValidationErrorCategoryHandler, Description: "validator panicked"},
	}); err != nil {
		t.Fatalf("CompleteValidationLifecycle: %v", err)
	}
	if err := board.CompleteArtifactValidation(context.Background(), artifactID, "architect", false, &claims.ArtifactError{
		Category:    claims.ArtifactErrorCategoryInternal,
		Description: "validator panicked",
	}); err != nil {
		t.Fatalf("CompleteArtifactValidation: %v", err)
	}

	gotTestament, _ := board.CloneTestament(testament.ID)
	if gotTestament.LifecycleStatus != claims.TestamentLifecycleValidationErrored {
		t.Fatalf("testament lifecycle = %s, want validation_errored", gotTestament.LifecycleStatus)
	}
	correctives := phase9CorrectiveClaims(board, claimID)
	if len(correctives) != 1 {
		t.Fatalf("corrective claims = %d, want 1", len(correctives))
	}
	if !claims.HasRelation(correctives[0].Relations, claims.RelationshipSupersedes, artifactID) {
		t.Fatalf("errored corrective missing supersedes relation to artifact: %+v", correctives[0].Relations)
	}
}

func phase9PostedClaimWithTargets(t *testing.T, board *claims.ClaimsBoard, targets []string) string {
	t.Helper()
	validations := make([]*claims.Validation, 0, len(targets))
	for _, target := range targets {
		validations = append(validations, &claims.Validation{
			ID:                 "phase9-" + target,
			Type:               claims.ValidationTypeInspection,
			Description:        "validate " + target,
			Required:           true,
			ValidatorID:        "phase9.validator",
			TargetArtifactName: target,
			ArtifactDataType:   claims.ArtifactDataTypePlanMarkdown,
		})
	}
	generated, err := board.GenerateClaimAction(context.Background(), claims.Action{AgentID: "architect", Type: claims.ActionTypeTask}, []claims.Claim{{
		Title:       "Produce required evidence",
		Description: "Produce all required target artifacts.",
		ActionType:  claims.ActionTypeTask,
		Relations: []claims.Relation{
			{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: "engineer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}}, claims.GenerateClaimActionOptions{IdempotencyKey: "phase9-missing-targets"})
	if err != nil {
		t.Fatalf("GenerateClaimAction: %v", err)
	}
	if err := board.PostGeneratedClaim(context.Background(), generated.Claims[0].ID, "architect", claims.ClaimPostOptions{}); err != nil {
		t.Fatalf("PostGeneratedClaim: %v", err)
	}
	return generated.Claims[0].ID
}

func phase9EmptyTestament(t *testing.T, board *claims.ClaimsBoard, claimID string) string {
	t.Helper()
	generated, err := board.GenerateTestamentAction(context.Background(), claims.Action{AgentID: "engineer", Type: claims.ActionTypeTestament}, []claims.Testament{{
		AgentID:   "engineer",
		Summary:   "missing evidence",
		Relations: []claims.Relation{{Related: claimID, RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipClaim}},
	}}, claims.GenerateTestamentActionOptions{IdempotencyKey: "phase9-empty-testament:" + claimID})
	if err != nil {
		t.Fatalf("GenerateTestamentAction: %v", err)
	}
	if err := board.PostGeneratedTestament(context.Background(), generated.Testaments[0].ID, "engineer", claims.TestamentPostOptions{}); err != nil {
		t.Fatalf("PostGeneratedTestament: %v", err)
	}
	return generated.Testaments[0].ID
}

func phase9CorrectiveClaims(board *claims.ClaimsBoard, originalClaimID string) []*claims.Claim {
	var out []*claims.Claim
	for _, claim := range board.Projection().Claims {
		claim := claim
		if claim.ActionType == claims.ActionTypeCorrective && claims.HasRelation(claim.Relations, claims.RelationshipCorrects, originalClaimID) {
			out = append(out, &claim)
		}
	}
	return out
}
