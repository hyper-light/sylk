package claims_test

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	claimsmocks "github.com/adalundhe/sylk/core/claims/mocks"
	"github.com/stretchr/testify/mock"
)

func TestPhase012IntegrationMockeryCanonicalParticipantDelivery(t *testing.T) {
	service, err := claims.NewServiceParticipantRegistration("tool_runtime", map[string]string{"session": "sess"}, 8, 2, time.Second, []claims.ActionType{claims.ActionTypeTask})
	if err != nil {
		t.Fatalf("service registration: %v", err)
	}
	resolver := &claimsmocks.AgentRefResolver{}
	resolver.On("ResolveAgentRef", mock.Anything, "sess", "architect").Return(claims.AgentRef{
		UID:        "architect-uid",
		Type:       "architect",
		Category:   string(claims.ParticipantCategoryAgent),
		Generation: 1,
	}, true)
	resolver.On("ResolveAgentRef", mock.Anything, "sess", "tool_runtime").Return(service.AgentRef(), true)

	bus := &claimsmocks.DeltaBus{}
	bus.On("PublishDelta", mock.Anything, mock.Anything, mock.Anything).Return(nil)

	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{
		BoardID:          "board",
		SessionID:        "sess",
		TaskID:           "task",
		AgentRefResolver: resolver,
		DeltaBus:         bus,
	})
	generated, err := board.GenerateClaimAction(context.Background(), claims.Action{AgentID: "architect", Type: claims.ActionTypeTask}, []claims.Claim{{
		Title:       "Run service",
		Description: "Run deterministic tool runtime work.",
		ActionType:  claims.ActionTypeTask,
		Relations: []claims.Relation{
			{Related: "architect", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: "tool_runtime", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: []*claims.Validation{{ID: "receipt", Type: claims.ValidationTypeReceipt, Required: true, Description: "receipt", QualityBar: "received"}},
	}}, claims.GenerateClaimActionOptions{IdempotencyKey: "phase012-delivery"})
	if err != nil {
		t.Fatalf("GenerateClaimAction: %v", err)
	}
	if err := board.PostGeneratedClaim(context.Background(), generated.Claims[0].ID, "architect", claims.ClaimPostOptions{Reason: "integration"}); err != nil {
		t.Fatalf("PostGeneratedClaim: %v", err)
	}

	wantTopic := claims.CanonicalAgentRefTopic("sess", service.AgentRef(), claims.DeltaActionClaimPosted)
	var delivered claims.CanonicalDelta
	found := false
	for _, call := range bus.Calls {
		topic, _ := call.Arguments.Get(1).(string)
		delta, _ := call.Arguments.Get(2).(claims.CanonicalDelta)
		if topic == wantTopic {
			delivered = delta
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("mock bus did not receive service topic %q; calls=%#v", wantTopic, bus.Calls)
	}
	if err := claims.ValidateCanonicalDeltaStrict(delivered); err != nil {
		t.Fatalf("delivered canonical delta invalid: %v", err)
	}
	if len(delivered.Delivery.To) != 1 || delivered.Delivery.To[0].UID != service.UID || delivered.Delivery.To[0].Category != string(claims.ParticipantCategoryService) {
		t.Fatalf("delivery ref = %+v, want service uid/category", delivered.Delivery)
	}
	resolver.AssertExpectations(t)
}

func TestPhase012E2EMockeryServiceTypedArtifactAndValidator(t *testing.T) {
	board, claimID := phase012ServiceBoard(t)
	participant, err := claims.NewServiceParticipantRegistration("readiness_service", map[string]string{"session": "sess"}, 8, 1, time.Second, []claims.ActionType{claims.ActionTypeTask})
	if err != nil {
		t.Fatalf("service registration: %v", err)
	}
	scope := &claimsmocks.ScopeProvider{}
	scope.On("Go", mock.Anything, participant.HandlerTimeout, mock.Anything).Run(func(args mock.Arguments) {
		fn := args.Get(2).(func(context.Context) error)
		if err := fn(context.Background()); err != nil {
			t.Fatalf("scoped service fn: %v", err)
		}
	}).Return(nil)

	serviceHandler := &claimsmocks.ServiceHandler{}
	serviceArtifact := &claims.Artifact{ArtifactName: "readiness", Kind: claims.ArtifactKindReadiness, Reference: "ready"}
	if err := claims.SetArtifactData(serviceArtifact, claims.KnowledgeReadinessArtifactData{Component: "readiness_service", QualityBar: "ready", Reference: "ready"}); err != nil {
		t.Fatalf("SetArtifactData service artifact: %v", err)
	}
	serviceHandler.On("HandleServiceClaim", mock.Anything, mock.MatchedBy(func(req claims.ServiceClaimRequest) bool {
		return req.Claim != nil && req.Claim.ID == claimID && req.Participant.UID == participant.UID
	})).Return(claims.ServiceClaimResult{Summary: "ready", Artifacts: []*claims.Artifact{serviceArtifact}}, nil).Once()

	dispatcher, err := claims.NewServiceDispatcher(claims.ServiceDispatcherConfig{
		Board:       board,
		Scope:       scope,
		Participant: participant,
		Handler:     serviceHandler,
	})
	if err != nil {
		t.Fatalf("NewServiceDispatcher: %v", err)
	}
	delta := claims.NewCanonicalDelta(
		claims.DeltaActionClaimPosted,
		board.SessionID(),
		board.BoardID(),
		board.HighWaterSequence(),
		time.Now(),
		claims.DegradedAgentRef("issuer", "test"),
		[]claims.DeltaRef{{Role: "claim", Type: claims.RelatedTypeClaim, ID: claimID}},
		&claims.DeltaDelivery{To: []claims.AgentRef{participant.AgentRef()}, Relationship: claims.RelationshipSubject},
		map[string]any{"claim": map[string]any{"id": claimID, "action": string(claims.ActionTypeTask)}},
	)
	if err := dispatcher.DispatchDelta(context.Background(), delta); err != nil {
		t.Fatalf("DispatchDelta: %v", err)
	}
	serviceHandler.AssertExpectations(t)
	scope.AssertExpectations(t)

	readinessArtifact := findArtifactByName(t, board, claimID, "readiness")
	if _, err := claims.ArtifactData[claims.KnowledgeReadinessArtifactData](readinessArtifact); err != nil {
		t.Fatalf("typed service artifact decode: %v", err)
	}

	validatorHandler := &claimsmocks.ValidatorHandler{}
	resultArtifact := &claims.Artifact{ArtifactName: "readiness_validation", Kind: claims.ArtifactKindReadiness, Reference: "validated"}
	if err := claims.SetArtifactData(resultArtifact, claims.PresentationEvidenceArtifactData{Kind: "validation", Reference: "validated"}); err != nil {
		t.Fatalf("SetArtifactData result artifact: %v", err)
	}
	validatorHandler.On("ValidateArtifact", mock.Anything, mock.MatchedBy(func(req claims.ValidatorHandlerRequest) bool {
		return req.Artifact != nil &&
			req.Artifact.ArtifactName == "readiness" &&
			req.Artifact.DataType == claims.ArtifactDataTypeKnowledgeReadiness &&
			req.Validation != nil &&
			req.Validation.TargetArtifactName == "readiness"
	})).Return(claims.ValidatorHandlerResult{ResultArtifact: resultArtifact}, nil).Once()

	registry := claims.NewValidatorRegistry()
	if _, err := registry.Register(claims.ValidatorRegistration{
		ValidatorID:        "readiness.validator",
		ValidationType:     claims.ValidationTypeInspection,
		ActionType:         claims.ActionTypeTask,
		Determinism:        claims.HandlerDeterminismPure,
		Timeout:            time.Second,
		ConcurrencyBudget:  1,
		TargetArtifactName: "readiness",
		ArtifactDataType:   claims.ArtifactDataTypeKnowledgeReadiness,
		ResultDataType:     claims.ArtifactDataTypePresentationEvidence,
		Handler:            validatorHandler,
	}); err != nil {
		t.Fatalf("Register validator: %v", err)
	}
	validation := &claims.Validation{
		ID:                 "readiness-validation",
		Type:               claims.ValidationTypeInspection,
		Required:           true,
		ValidatorID:        "readiness.validator",
		TargetArtifactName: "readiness",
		ArtifactDataType:   claims.ArtifactDataTypeKnowledgeReadiness,
		ResultDataType:     claims.ArtifactDataTypePresentationEvidence,
	}
	result, err := claims.NewProgrammaticValidatorDispatcher(registry, claims.SystemClock{}).DispatchValidation(context.Background(), claims.ValidationDispatchRequest{
		Claim:      &claims.Claim{ID: claimID, ActionType: claims.ActionTypeTask},
		Artifact:   readinessArtifact,
		Validation: validation,
		StartedAt:  time.Now(),
	})
	if err != nil {
		t.Fatalf("DispatchValidation: %v", err)
	}
	if result.Status != claims.ValidationStatusValidated || result.ResultArtifact == nil {
		t.Fatalf("validation result = %#v, want validated result artifact", result)
	}
	validatorHandler.AssertExpectations(t)
}

func phase012ServiceBoard(t *testing.T) (*claims.ClaimsBoard, string) {
	t.Helper()
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "phase012-board", SessionID: "sess", TaskID: "task"})
	generated, err := board.GenerateClaimAction(context.Background(), claims.Action{AgentID: "issuer", Type: claims.ActionTypeTask}, []claims.Claim{{
		Title:       "Readiness",
		Description: "Produce readiness evidence.",
		ActionType:  claims.ActionTypeTask,
		Relations: []claims.Relation{
			{Related: "issuer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: "readiness_service", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		Validations: []*claims.Validation{{ID: "receipt", Type: claims.ValidationTypeReceipt, Required: true, Description: "receipt", QualityBar: "received"}},
	}}, claims.GenerateClaimActionOptions{IdempotencyKey: "phase012-service"})
	if err != nil {
		t.Fatalf("GenerateClaimAction: %v", err)
	}
	claimID := generated.Claims[0].ID
	if err := board.PostGeneratedClaim(context.Background(), claimID, "issuer", claims.ClaimPostOptions{Reason: "phase012"}); err != nil {
		t.Fatalf("PostGeneratedClaim: %v", err)
	}
	return board, claimID
}

func findArtifactByName(t *testing.T, board *claims.ClaimsBoard, claimID, name string) *claims.Artifact {
	t.Helper()
	for _, testament := range board.TestamentsByClaim(claimID) {
		for _, artifact := range testament.Artifacts {
			if artifact != nil && artifact.ArtifactName == name {
				return artifact
			}
		}
	}
	t.Fatalf("artifact %q for claim %q not found", name, claimID)
	return nil
}
