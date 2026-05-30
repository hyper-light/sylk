package claims_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	claimsmocks "github.com/adalundhe/sylk/core/claims/mocks"
	"github.com/stretchr/testify/mock"
)

func TestPhase7StreamingArtifactReceiptAndAtomicAttachment(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "phase7-board", SessionID: "phase7", TaskID: "task"})
	claimID := phase678PostedClaim(t, board, "architect", "engineer", "")
	artifact := phase678PlanArtifact(t, claimID, "engineer")

	generated, err := board.GenerateArtifact(context.Background(), *artifact, "engineer", claims.ArtifactLifecycleOptions{Reason: "streamed"})
	if err != nil {
		t.Fatalf("GenerateArtifact: %v", err)
	}
	if generated.TestamentID != "" || !phase678ProjectionHasArtifact(board.Projection(), generated.ID) {
		t.Fatalf("generated artifact not visible as unattached projection artifact: %+v", generated)
	}
	received, err := board.ReceiveArtifact(context.Background(), generated.ID, "architect")
	if err != nil {
		t.Fatalf("ReceiveArtifact: %v", err)
	}
	if received.Status != claims.ArtifactStatusReceived {
		t.Fatalf("received status = %s", received.Status)
	}
	action, err := board.GenerateTestamentAction(context.Background(), claims.Action{AgentID: "engineer", Type: claims.ActionTypeTestament}, []claims.Testament{{
		AgentID:   "engineer",
		Summary:   "plan ready",
		Relations: []claims.Relation{{Related: claimID, RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipClaim}},
		Artifacts: []*claims.Artifact{{ID: generated.ID, ArtifactName: "plan"}},
	}}, claims.GenerateTestamentActionOptions{})
	if err != nil {
		t.Fatalf("GenerateTestamentAction: %v", err)
	}
	attached := action.Testaments[0].Artifacts[0]
	if attached.Status != claims.ArtifactStatusAttached || attached.TestamentID == "" {
		t.Fatalf("attached artifact = %+v", attached)
	}
}

func TestPhase7AttachmentRejectsWrongClaimWithoutPartialAttach(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "phase7-atomic-board", SessionID: "phase7", TaskID: "task"})
	claimID := phase678PostedClaim(t, board, "architect", "engineer", "")
	otherClaimID := phase678PostedClaim(t, board, "architect", "engineer", "")
	valid := phase678GeneratedArtifact(t, board, claimID, "plan", "engineer")
	wrong := phase678GeneratedArtifact(t, board, otherClaimID, "other-plan", "engineer")
	_, err := board.GenerateTestamentAction(context.Background(), claims.Action{AgentID: "engineer", Type: claims.ActionTypeTestament}, []claims.Testament{{
		AgentID:   "engineer",
		Summary:   "invalid mixed testament",
		Relations: []claims.Relation{{Related: claimID, RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipClaim}},
		Artifacts: []*claims.Artifact{{ID: valid.ID}, {ID: wrong.ID}},
	}}, claims.GenerateTestamentActionOptions{})
	if err == nil || !strings.Contains(err.Error(), "belongs to claim") {
		t.Fatalf("mixed testament error = %v", err)
	}
	stored, _ := board.CloneArtifact(valid.ID)
	if stored.TestamentID != "" || stored.Status != claims.ArtifactStatusGenerated {
		t.Fatalf("valid artifact partially attached after failed testament: %+v", stored)
	}
}

func TestPhase8QualityBarEligibilityUsesResolvedParticipantCategory(t *testing.T) {
	resolver := claims.AgentRefResolverFunc(func(_ context.Context, sessionID, agentID string) (claims.AgentRef, bool) {
		if sessionID != "phase8" {
			return claims.AgentRef{}, false
		}
		if agentID == "svc" {
			return phase678Ref("svc", claims.ParticipantCategoryService), true
		}
		return phase678Ref(agentID, claims.ParticipantCategoryAgent), true
	})
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "phase8-eligibility", SessionID: "phase8", TaskID: "task", AgentRefResolver: resolver})
	claimID := phase678GeneratedClaim(t, board, "svc", "engineer", "agentic review required")
	if err := board.PostGeneratedClaim(context.Background(), claimID, "svc", claims.ClaimPostOptions{}); err == nil {
		t.Fatal("service claimant with quality bar posted successfully")
	}
	claim, _ := board.CloneClaim(claimID)
	if claim.LifecycleStatus != claims.ClaimLifecyclePostFailed {
		t.Fatalf("claim lifecycle = %s, want post_failed", claim.LifecycleStatus)
	}
}

func TestPhase8DeterministicSuccessTransitionsToQualityBarAndCapturesVerdict(t *testing.T) {
	resolver := claims.AgentRefResolverFunc(func(_ context.Context, sessionID, agentID string) (claims.AgentRef, bool) {
		if sessionID != "phase8-qb" {
			return claims.AgentRef{}, false
		}
		return phase678Ref(agentID, claims.ParticipantCategoryAgent), true
	})
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "phase8-quality", SessionID: "phase8-qb", TaskID: "task", AgentRefResolver: resolver})
	claimID := phase678PostedClaim(t, board, "architect", "engineer", "plan is clear")
	artifactID := phase678AttachedPlanArtifact(t, board, claimID, "engineer")
	registry := claims.NewValidatorRegistry()
	if _, err := claims.RegisterValidator[claims.PlanMarkdownArtifactData, claims.PresentationEvidenceArtifactData](registry, claims.ValidatorConfig{
		ID:                 "phase8.plan",
		ValidationType:     claims.ValidationTypeInspection,
		ActionType:         claims.ActionTypeTask,
		Determinism:        claims.HandlerDeterminismPure,
		Timeout:            time.Second,
		ConcurrencyBudget:  1,
		TargetArtifactName: "plan",
	}, func(_ context.Context, data claims.PlanMarkdownArtifactData) (*claims.Artifact, error) {
		result := &claims.Artifact{ArtifactName: "plan.validation", Kind: claims.ArtifactKindReadiness, Reference: data.Title}
		return result, claims.SetArtifactData(result, claims.PresentationEvidenceArtifactData{Kind: "validation", Reference: data.Markdown})
	}); err != nil {
		t.Fatalf("RegisterValidator: %v", err)
	}
	dispatcher, err := claims.NewBoardValidatorDispatcher(claims.BoardValidatorDispatcherConfig{Board: board, Registry: registry, MaxInputBytes: 4096, MaxOutputBytes: 4096})
	if err != nil {
		t.Fatalf("NewBoardValidatorDispatcher: %v", err)
	}
	validation := phase678OnlyValidation(t, board, claimID)
	result, err := dispatcher.DispatchValidationByID(context.Background(), claimID, validation.ID)
	if err != nil {
		t.Fatalf("DispatchValidationByID: %v", err)
	}
	if result.Status != claims.ValidationStatusValidatingQualityBar {
		t.Fatalf("deterministic status = %s, want validating_quality_bar", result.Status)
	}
	evaluator := &claimsmocks.AgentTurnEvaluator{}
	evaluator.On("EvaluateArtifactQualityBar", mock.Anything, mock.MatchedBy(func(req claims.QualityBarEvaluationRequest) bool {
		return req.Validation.ID == validation.ID && req.Artifact.ID == artifactID && req.Context.QualityBar == "plan is clear" && req.Context.ArtifactInline != ""
	})).Return(claims.QualityBarEvaluationResult{Status: claims.ValidationStatusValidated, Evaluator: phase678Ref("architect", claims.ParticipantCategoryAgent), EvaluatedAt: time.Now().UTC()}, nil).Once()
	quality, err := claims.NewQualityBarDispatcher(claims.QualityBarDispatcherConfig{Board: board, Evaluator: evaluator, ActorID: "architect", MaxInlineBytes: 4096})
	if err != nil {
		t.Fatalf("NewQualityBarDispatcher: %v", err)
	}
	if _, err := quality.DispatchQualityBarByID(context.Background(), claimID, validation.ID); err != nil {
		t.Fatalf("DispatchQualityBarByID: %v", err)
	}
	stored, _, _ := board.CloneValidation(validation.ID)
	if stored.Status != claims.ValidationStatusValidated {
		t.Fatalf("quality-bar status = %s, want validated", stored.Status)
	}
	evaluator.AssertExpectations(t)
}

func TestPhase6ArtifactOrchestratorShortCircuitsRequiredFailure(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "phase6-artifact", SessionID: "phase6", TaskID: "task"})
	claimID := phase678PostedClaimWithValidations(t, board, "architect", "engineer", []string{"first", "second"})
	artifactID := phase678AttachedPlanArtifact(t, board, claimID, "engineer")
	dispatcher := &claimsmocks.ValidationDispatcher{}
	dispatcher.On("DispatchValidation", mock.Anything, mock.MatchedBy(func(req claims.ValidationDispatchRequest) bool {
		return req.Validation.ID == "first"
	})).Return(claims.ValidationDispatchResult{
		ValidationID: "first",
		Status:       claims.ValidationStatusValidationFailed,
		Error:        &claims.ValidationError{Category: claims.ValidationErrorCategoryHandler, Description: "failed", OccurredAt: time.Now().UTC()},
	}, nil).Once()
	scope := &claimsmocks.ScopeProvider{}
	scope.On("Go", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		fn := args.Get(2).(func(context.Context) error)
		if err := fn(context.Background()); err != nil {
			t.Errorf("scope worker: %v", err)
		}
	}).Return(nil).Once()
	orchestrator, err := claims.NewArtifactOrchestrator(claims.ArtifactOrchestratorConfig{Board: board, Dispatcher: dispatcher, Scope: scope, ActorID: "architect", ConcurrencyBudget: 1, Timeout: time.Second})
	if err != nil {
		t.Fatalf("NewArtifactOrchestrator: %v", err)
	}
	outcome, err := orchestrator.ValidateArtifact(context.Background(), claimID, phase678OnlyTestament(t, board, claimID).ID, artifactID)
	if err != nil {
		t.Fatalf("ValidateArtifact: %v", err)
	}
	if outcome.Status != claims.ArtifactStatusValidationFailed || len(outcome.Results) != 1 {
		t.Fatalf("outcome = %+v", outcome)
	}
	dispatcher.AssertExpectations(t)
	scope.AssertExpectations(t)
}

func TestPhase6ResultTestamentBuilderPostsGeneratedResultArtifacts(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "phase6-result", SessionID: "phase6", TaskID: "task"})
	claimID := phase678PostedClaim(t, board, "architect", "engineer", "")
	_ = phase678AttachedPlanArtifact(t, board, claimID, "engineer")
	source := phase678OnlyTestament(t, board, claimID)
	resultArtifact := &claims.Artifact{ArtifactName: "plan.validation", Kind: claims.ArtifactKindReadiness, ClaimID: claimID, Reference: "valid"}
	if err := claims.SetArtifactData(resultArtifact, claims.PresentationEvidenceArtifactData{Kind: "validation", Reference: "valid"}); err != nil {
		t.Fatalf("SetArtifactData: %v", err)
	}
	builder, err := claims.NewResultTestamentBuilder(claims.ResultTestamentBuilderConfig{Board: board, ActorID: "architect"})
	if err != nil {
		t.Fatalf("NewResultTestamentBuilder: %v", err)
	}
	result, err := builder.Build(context.Background(), claims.ResultTestamentRequest{ClaimID: claimID, SourceTestamentID: source.ID, Artifacts: []*claims.Artifact{resultArtifact}})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if result.Skipped || result.Testament.LifecycleStatus != claims.TestamentLifecyclePosted {
		t.Fatalf("result testament = %+v", result)
	}
	if got := result.Testament.Artifacts[0]; got.Status != claims.ArtifactStatusGenerated {
		t.Fatalf("result artifact status = %s, want generated", got.Status)
	}
}

func TestPhase8QualityBarMalformedVerdictRecordsErroredValidation(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "phase8-malformed", SessionID: "phase8", TaskID: "task"})
	claimID := phase678PostedClaim(t, board, "architect", "engineer", "strict")
	_ = phase678AttachedPlanArtifact(t, board, claimID, "engineer")
	validation := phase678OnlyValidation(t, board, claimID)
	if err := board.BeginValidation(context.Background(), claimID, validation.ID, "validator", ""); err != nil {
		t.Fatalf("BeginValidation: %v", err)
	}
	if err := board.BeginValidationQualityBar(context.Background(), claimID, validation.ID, "validator", ""); err != nil {
		t.Fatalf("BeginValidationQualityBar: %v", err)
	}
	evaluator := &claimsmocks.AgentTurnEvaluator{}
	evaluator.On("EvaluateArtifactQualityBar", mock.Anything, mock.Anything).Return(claims.QualityBarEvaluationResult{}, nil).Once()
	quality, err := claims.NewQualityBarDispatcher(claims.QualityBarDispatcherConfig{Board: board, Evaluator: evaluator, ActorID: "architect"})
	if err != nil {
		t.Fatalf("NewQualityBarDispatcher: %v", err)
	}
	if _, err := quality.DispatchQualityBarByID(context.Background(), claimID, validation.ID); err != nil {
		t.Fatalf("DispatchQualityBarByID: %v", err)
	}
	stored, _, _ := board.CloneValidation(validation.ID)
	if stored.Status != claims.ValidationStatusErrored || stored.ResultArtifactID == "" {
		t.Fatalf("stored malformed verdict validation = %+v", stored)
	}
	if _, ok := board.CloneArtifact(stored.ResultArtifactID); !ok {
		t.Fatal("malformed quality-bar verdict did not record an error artifact")
	}
}

func phase678PostedClaim(t *testing.T, board *claims.ClaimsBoard, issuer, subject, qualityBar string) string {
	t.Helper()
	claimID := phase678GeneratedClaim(t, board, issuer, subject, qualityBar)
	if err := board.PostGeneratedClaim(context.Background(), claimID, issuer, claims.ClaimPostOptions{}); err != nil {
		t.Fatalf("PostGeneratedClaim: %v", err)
	}
	return claimID
}

func phase678GeneratedClaim(t *testing.T, board *claims.ClaimsBoard, issuer, subject, qualityBar string) string {
	t.Helper()
	generated, err := board.GenerateClaimAction(context.Background(), claims.Action{AgentID: issuer, Type: claims.ActionTypeTask}, []claims.Claim{{
		Title:       "Produce plan",
		Description: "Produce a plan artifact.",
		ActionType:  claims.ActionTypeTask,
		Relations: []claims.Relation{
			{Related: issuer, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: subject, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: []*claims.Validation{phase678Validation("phase678-plan", qualityBar)},
	}}, claims.GenerateClaimActionOptions{IdempotencyKey: issuer + ":" + subject + ":" + qualityBar + ":" + time.Now().String()})
	if err != nil {
		t.Fatalf("GenerateClaimAction: %v", err)
	}
	return generated.Claims[0].ID
}

func phase678PostedClaimWithValidations(t *testing.T, board *claims.ClaimsBoard, issuer, subject string, ids []string) string {
	t.Helper()
	validations := make([]*claims.Validation, 0, len(ids))
	for _, id := range ids {
		validations = append(validations, phase678Validation(id, ""))
	}
	generated, err := board.GenerateClaimAction(context.Background(), claims.Action{AgentID: issuer, Type: claims.ActionTypeTask}, []claims.Claim{{
		Title:       "Produce plan",
		Description: "Produce a plan artifact.",
		ActionType:  claims.ActionTypeTask,
		Relations: []claims.Relation{
			{Related: issuer, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: subject, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: validations,
	}}, claims.GenerateClaimActionOptions{IdempotencyKey: "phase678:" + strings.Join(ids, ",")})
	if err != nil {
		t.Fatalf("GenerateClaimAction: %v", err)
	}
	claimID := generated.Claims[0].ID
	if err := board.PostGeneratedClaim(context.Background(), claimID, issuer, claims.ClaimPostOptions{}); err != nil {
		t.Fatalf("PostGeneratedClaim: %v", err)
	}
	return claimID
}

func phase678Validation(id, qualityBar string) *claims.Validation {
	return &claims.Validation{
		ID:                 id,
		Type:               claims.ValidationTypeInspection,
		Description:        "plan validates",
		QualityBar:         qualityBar,
		Required:           true,
		ValidatorID:        "phase8.plan",
		TargetArtifactName: "plan",
		ArtifactDataType:   claims.ArtifactDataTypePlanMarkdown,
		ResultDataType:     claims.ArtifactDataTypePresentationEvidence,
	}
}

func phase678PlanArtifact(t *testing.T, claimID, participant string) *claims.Artifact {
	t.Helper()
	artifact := &claims.Artifact{ClaimID: claimID, ArtifactName: "plan", Kind: claims.ArtifactKindPlanMarkdown, AgentID: participant, ParticipantID: participant, Reference: "# Plan"}
	if err := claims.SetArtifactData(artifact, claims.PlanMarkdownArtifactData{Markdown: "# Plan", Title: "Plan"}); err != nil {
		t.Fatalf("SetArtifactData: %v", err)
	}
	return artifact
}

func phase678GeneratedArtifact(t *testing.T, board *claims.ClaimsBoard, claimID, name, participant string) *claims.Artifact {
	t.Helper()
	artifact := phase678PlanArtifact(t, claimID, participant)
	artifact.ArtifactName = name
	generated, err := board.GenerateArtifact(context.Background(), *artifact, participant, claims.ArtifactLifecycleOptions{Reason: "streamed"})
	if err != nil {
		t.Fatalf("GenerateArtifact: %v", err)
	}
	return generated
}

func phase678AttachedPlanArtifact(t *testing.T, board *claims.ClaimsBoard, claimID, participant string) string {
	t.Helper()
	action, err := board.GenerateTestamentAction(context.Background(), claims.Action{AgentID: participant, Type: claims.ActionTypeTestament}, []claims.Testament{{
		AgentID:   participant,
		Summary:   "plan ready",
		Relations: []claims.Relation{{Related: claimID, RelatedType: claims.RelatedTypeClaim, Relationship: claims.RelationshipClaim}},
		Artifacts: []*claims.Artifact{phase678PlanArtifact(t, claimID, participant)},
	}}, claims.GenerateTestamentActionOptions{IdempotencyKey: "phase678-testament:" + claimID})
	if err != nil {
		t.Fatalf("GenerateTestamentAction: %v", err)
	}
	if err := board.PostGeneratedTestament(context.Background(), action.Testaments[0].ID, participant, claims.TestamentPostOptions{}); err != nil {
		t.Fatalf("PostGeneratedTestament: %v", err)
	}
	return action.Testaments[0].Artifacts[0].ID
}

func phase678OnlyValidation(t *testing.T, board *claims.ClaimsBoard, claimID string) *claims.Validation {
	t.Helper()
	claim, ok := board.CloneClaim(claimID)
	if !ok || len(claim.Validations) == 0 {
		t.Fatalf("claim %q validation not found", claimID)
	}
	return claim.Validations[0]
}

func phase678OnlyTestament(t *testing.T, board *claims.ClaimsBoard, claimID string) *claims.Testament {
	t.Helper()
	testaments := board.TestamentsByClaim(claimID)
	for _, testament := range testaments {
		if len(testament.Artifacts) != 0 {
			return testament
		}
	}
	t.Fatalf("testament for claim %q not found", claimID)
	return nil
}

func phase678ProjectionHasArtifact(proj *claims.ClaimsBoardProjection, artifactID string) bool {
	for _, artifact := range proj.Artifacts {
		if artifact.ID == artifactID {
			return true
		}
	}
	return false
}

func phase678Ref(uid string, category claims.ParticipantCategory) claims.AgentRef {
	return claims.AgentRef{UID: uid, Type: uid, Category: string(category), Generation: 1}.Normalized()
}
