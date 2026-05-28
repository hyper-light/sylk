package claims_test

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/claims"
	claimsmocks "github.com/adalundhe/sylk/core/claims/mocks"
	"github.com/stretchr/testify/mock"
)

func TestValidationDispatcherMockDrivesInterfaceOnlyUsage(t *testing.T) {
	dispatcher := &claimsmocks.ValidationDispatcher{}
	req := claims.ValidationDispatchRequest{
		Claim:      &claims.Claim{ID: "c"},
		Artifact:   &claims.Artifact{ID: "a", ArtifactName: "plan"},
		Validation: &claims.Validation{ID: "v", TargetArtifactName: "plan"},
	}
	want := claims.ValidationDispatchResult{
		ValidationID: "v",
		Status:       claims.ValidationStatusValidated,
	}
	dispatcher.On("DispatchValidation", mock.Anything, req).Return(want, nil).Once()

	got, err := useValidationDispatcher(context.Background(), dispatcher, req)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if got.Status != claims.ValidationStatusValidated {
		t.Fatalf("status = %s", got.Status)
	}
	dispatcher.AssertExpectations(t)
}

func useValidationDispatcher(ctx context.Context, dispatcher claims.ValidationDispatcher, req claims.ValidationDispatchRequest) (claims.ValidationDispatchResult, error) {
	return dispatcher.DispatchValidation(ctx, req)
}

func TestPhaseZeroGeneratedMocksCoverDeclaredInterfaces(t *testing.T) {
	now := time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)

	clock := &claimsmocks.ClaimsClock{}
	clock.On("Now").Return(now).Once()
	if got := useClaimsClock(clock); !got.Equal(now) {
		t.Fatalf("mock clock Now = %s, want %s", got, now)
	}
	clock.AssertExpectations(t)

	quality := &claimsmocks.AgentTurnEvaluator{}
	qualityReq := claims.QualityBarEvaluationRequest{
		Claim:      &claims.Claim{ID: "claim"},
		Artifact:   &claims.Artifact{ID: "artifact"},
		Validation: &claims.Validation{ID: "validation"},
	}
	qualityResult := claims.QualityBarEvaluationResult{Status: claims.ValidationStatusValidated, EvaluatedAt: now}
	quality.On("EvaluateArtifactQualityBar", mock.Anything, qualityReq).Return(qualityResult, nil).Once()
	if got, err := useAgentTurnEvaluator(context.Background(), quality, qualityReq); err != nil || got.Status != claims.ValidationStatusValidated {
		t.Fatalf("quality evaluator result = %+v err=%v", got, err)
	}
	quality.AssertExpectations(t)

	validationSink := &claimsmocks.ValidationLifecycleBusSink{}
	validationEvent := claims.ValidationLifecycleEvent{
		Validation: &claims.Validation{ID: "validation"},
		From:       claims.ValidationStatusReady,
		To:         claims.ValidationStatusValidating,
		ActorID:    "architect",
		At:         now,
	}
	validationSink.On("PublishValidationLifecycle", mock.Anything, validationEvent).Return(nil).Once()
	if err := useValidationLifecycleSink(context.Background(), validationSink, validationEvent); err != nil {
		t.Fatalf("validation sink: %v", err)
	}
	validationSink.AssertExpectations(t)

	generator := &claimsmocks.TestamentGenerator{}
	action := claims.Action{ID: "action", AgentID: "architect", Type: claims.ActionTypeTestament}
	testaments := []claims.Testament{{ID: "testament", AgentID: "architect"}}
	opts := claims.GenerateTestamentActionOptions{Reason: "test"}
	want := &claims.GeneratedTestamentAction{Action: action, Testaments: testaments}
	generator.On("GenerateTestamentAction", mock.Anything, action, testaments, opts).Return(want, nil).Once()
	if got, err := useTestamentGenerator(context.Background(), generator, action, testaments, opts); err != nil || got != want {
		t.Fatalf("testament generator result = %+v err=%v", got, err)
	}
	generator.AssertExpectations(t)
}

func useClaimsClock(clock claims.ClaimsClock) time.Time {
	return clock.Now()
}

func useAgentTurnEvaluator(ctx context.Context, evaluator claims.AgentTurnEvaluator, req claims.QualityBarEvaluationRequest) (claims.QualityBarEvaluationResult, error) {
	return evaluator.EvaluateArtifactQualityBar(ctx, req)
}

func useValidationLifecycleSink(ctx context.Context, sink claims.ValidationLifecycleBusSink, event claims.ValidationLifecycleEvent) error {
	return sink.PublishValidationLifecycle(ctx, event)
}

func useTestamentGenerator(ctx context.Context, generator claims.TestamentGenerator, action claims.Action, testaments []claims.Testament, opts claims.GenerateTestamentActionOptions) (*claims.GeneratedTestamentAction, error) {
	return generator.GenerateTestamentAction(ctx, action, testaments, opts)
}
