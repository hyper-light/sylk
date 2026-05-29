package claims_test

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	claimsmocks "github.com/adalundhe/sylk/core/claims/mocks"
	"github.com/stretchr/testify/mock"
)

func TestInfrastructureServiceE2EWithMockeryBusAndScope(t *testing.T) {
	board := claims.NewClaimsBoard(claims.ClaimsBoardConfig{BoardID: "mockery-infra-board", SessionID: "mockery-session", TaskID: "task"})
	participant, err := claims.NewParticipantRegistration(claims.ParticipantCategoryService, "sys:provider_gateway", map[string]string{"session": "mockery-session"}, 8, 1, time.Second, claims.HandlerDeterminismNondeterministic, []claims.ActionType{claims.ActionTypeTask})
	if err != nil {
		t.Fatalf("NewParticipantRegistration: %v", err)
	}

	subscription := &claimsmocks.DeltaSubscription{}
	subscription.On("Unsubscribe").Return(nil).Once()
	subscriber := &claimsmocks.DeltaSubscriber{}
	var captured claims.DeltaHandler
	wantTopic := claims.CanonicalAgentRefTopic(board.SessionID(), participant.AgentRef(), claims.DeltaActionClaimPosted)
	subscriber.On("SubscribeDelta", wantTopic, mock.Anything).Run(func(args mock.Arguments) {
		captured = args.Get(1).(claims.DeltaHandler)
	}).Return(subscription, nil).Once()

	scope := &claimsmocks.ScopeProvider{}
	scope.On("Go", mock.Anything, mock.Anything, mock.Anything).Run(func(args mock.Arguments) {
		fn := args.Get(2).(func(context.Context) error)
		if err := fn(context.Background()); err != nil {
			t.Errorf("scoped provider gateway dispatch: %v", err)
		}
	}).Return(nil).Once()

	dispatcher, err := claims.NewServiceDispatcher(claims.ServiceDispatcherConfig{
		Board:       board,
		Subscriber:  subscriber,
		Scope:       scope,
		Participant: participant,
		Handler:     claims.NewProviderGatewayService(claims.InfrastructureServiceConfig{}),
	})
	if err != nil {
		t.Fatalf("NewServiceDispatcher: %v", err)
	}
	if err := dispatcher.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if captured == nil {
		t.Fatal("mockery subscriber did not capture handler")
	}

	claimID := postMockeryInfrastructureClaim(t, board, participant.RouteKey, claims.ProviderGatewayToolComplete, map[string]any{
		"model":               "mock-model",
		"identity":            "guide",
		"budget_tokens":       float64(100),
		"input_tokens":        float64(10),
		"output_tokens":       float64(20),
		"response_summary":    "mock response",
		"finish_reason":       "stop",
		"provider_request_id": "req-1",
	})
	captured(mockeryClaimPostedDelta(board, claimID, participant, claims.ActionTypeTask))

	testaments := board.TestamentsByClaim(claimID)
	if len(testaments) != 1 {
		t.Fatalf("testaments = %d, want 1", len(testaments))
	}
	if testaments[0].LifecycleStatus != claims.TestamentLifecycleValidated {
		t.Fatalf("testament lifecycle = %s, want validated", testaments[0].LifecycleStatus)
	}
	artifact := testaments[0].Artifacts[0]
	data, err := claims.ArtifactData[claims.ProviderGatewayCallArtifactData](artifact)
	if err != nil {
		t.Fatalf("provider artifact data: %v", err)
	}
	if data.ProviderRequestID != "req-1" || data.TotalTokens != 30 || data.Status != claims.InfrastructureStatusOK {
		t.Fatalf("provider data = %+v, want successful mocked provider gateway evidence", data)
	}

	if err := dispatcher.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	subscriber.AssertExpectations(t)
	subscription.AssertExpectations(t)
	scope.AssertExpectations(t)
}

func TestInfrastructureValidatorDispatcherWithMockeryHandler(t *testing.T) {
	artifact := &claims.Artifact{ArtifactName: "provider_gateway_call", Kind: claims.ArtifactKindProviderGatewayCall, Reference: "req-1"}
	if err := claims.SetArtifactData(artifact, claims.ProviderGatewayCallArtifactData{Operation: claims.ProviderGatewayToolComplete, Model: "mock", Identity: "guide", ResponseSummary: "ok"}); err != nil {
		t.Fatalf("SetArtifactData: %v", err)
	}
	validation := &claims.Validation{
		ID:                 "provider-contract",
		Type:               claims.ValidationTypeContract,
		ValidatorID:        "mock.provider.response",
		TargetArtifactName: "provider_gateway_call",
		ArtifactDataType:   claims.ArtifactDataTypeProviderGatewayCall,
		ResultDataType:     claims.ArtifactDataTypePresentationEvidence,
		Required:           true,
		Description:        "provider contract",
		QualityBar:         "provider.response",
	}
	claim := &claims.Claim{ID: "provider-claim", ActionType: claims.ActionTypeTask, Validations: []*claims.Validation{validation}}
	resultArtifact := &claims.Artifact{ArtifactName: "mock_validator_result", Kind: claims.ArtifactKindReadiness, Reference: "ok"}
	if err := claims.SetArtifactData(resultArtifact, claims.PresentationEvidenceArtifactData{Kind: claims.ArtifactKindReadiness, Reference: "ok"}); err != nil {
		t.Fatalf("SetArtifactData result: %v", err)
	}

	handler := &claimsmocks.ValidatorHandler{}
	handler.On("ValidateArtifact", mock.Anything, mock.MatchedBy(func(req claims.ValidatorHandlerRequest) bool {
		return req.Artifact.ArtifactName == artifact.ArtifactName && req.Validation.ID == validation.ID
	})).Return(claims.ValidatorHandlerResult{ResultArtifact: resultArtifact}, nil).Once()

	registry := claims.NewValidatorRegistry()
	_, err := registry.Register(claims.ValidatorRegistration{
		ValidatorID:        "mock.provider.response",
		ValidationType:     claims.ValidationTypeContract,
		Determinism:        claims.HandlerDeterminismPure,
		Timeout:            time.Second,
		ConcurrencyBudget:  1,
		TargetArtifactName: "provider_gateway_call",
		ArtifactDataType:   claims.ArtifactDataTypeProviderGatewayCall,
		ResultDataType:     claims.ArtifactDataTypePresentationEvidence,
		Handler:            handler,
	})
	if err != nil {
		t.Fatalf("Register validator: %v", err)
	}
	dispatcher := claims.NewProgrammaticValidatorDispatcher(registry, claims.SystemClock{})
	result, err := dispatcher.DispatchValidation(context.Background(), claims.ValidationDispatchRequest{Claim: claim, Validation: validation, Artifact: artifact})
	if err != nil {
		t.Fatalf("DispatchValidation: %v", err)
	}
	if result.Status != claims.ValidationStatusValidated || result.Error != nil {
		t.Fatalf("dispatch result = %+v, want validated", result)
	}
	handler.AssertExpectations(t)
}

func postMockeryInfrastructureClaim(t *testing.T, board *claims.ClaimsBoard, subject, tool string, args map[string]any) string {
	t.Helper()
	generated, err := board.GenerateClaimAction(context.Background(), claims.Action{AgentID: "issuer", Type: claims.ActionTypeTask}, []claims.Claim{{
		Title:       "Run infrastructure service",
		Description: "Infrastructure service should produce evidence.",
		ActionType:  claims.ActionTypeTask,
		Relations: []claims.Relation{
			{Related: "issuer", RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipIssuer},
			{Related: subject, RelatedType: claims.RelatedTypeAgent, Relationship: claims.RelationshipSubject},
		},
		ExpectedToolCalls: []claims.ExpectedToolCall{{Tool: tool, Arguments: args}},
		Validations:       []*claims.Validation{{ID: "receipt", Type: claims.ValidationTypeReceipt, Required: true, Description: "receipt", QualityBar: "received"}},
	}}, claims.GenerateClaimActionOptions{IdempotencyKey: "mockery-" + tool})
	if err != nil {
		t.Fatalf("GenerateClaimAction: %v", err)
	}
	claimID := generated.Claims[0].ID
	if err := board.PostGeneratedClaim(context.Background(), claimID, "issuer", claims.ClaimPostOptions{Reason: "mockery"}); err != nil {
		t.Fatalf("PostGeneratedClaim: %v", err)
	}
	return claimID
}

func mockeryClaimPostedDelta(board *claims.ClaimsBoard, claimID string, participant claims.ParticipantRegistration, action claims.ActionType) claims.CanonicalDelta {
	return claims.NewCanonicalDelta(
		claims.DeltaActionClaimPosted,
		board.SessionID(),
		board.BoardID(),
		board.HighWaterSequence(),
		time.Now(),
		claims.DegradedAgentRef("issuer", "test"),
		[]claims.DeltaRef{{Role: "claim", Type: claims.RelatedTypeClaim, ID: claimID}},
		&claims.DeltaDelivery{To: []claims.AgentRef{participant.AgentRef()}, Relationship: claims.RelationshipSubject},
		map[string]any{"claim": map[string]any{"id": claimID, "action": string(action)}},
	)
}
