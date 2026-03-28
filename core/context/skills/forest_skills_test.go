package skills

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/adalundhe/sylk/core/forest"
)

type mockForestService struct {
	resolveIntent func(ctx context.Context, input forest.ResolveIntentInput) (*forest.IntentResolution, error)
	retrieve      func(ctx context.Context, query forest.Query) ([]*forest.BranchPacket, error)
	predict       func(ctx context.Context, query forest.Query) ([]*forest.BranchPacket, error)
	recordOutcome func(ctx context.Context, record forest.OutcomeRecord) error
}

func (m *mockForestService) ResolveIntent(ctx context.Context, input forest.ResolveIntentInput) (*forest.IntentResolution, error) {
	if m.resolveIntent != nil {
		return m.resolveIntent(ctx, input)
	}
	return &forest.IntentResolution{}, nil
}

func (m *mockForestService) Retrieve(ctx context.Context, query forest.Query) ([]*forest.BranchPacket, error) {
	if m.retrieve != nil {
		return m.retrieve(ctx, query)
	}
	return nil, nil
}

func (m *mockForestService) PredictNextBranches(ctx context.Context, query forest.Query) ([]*forest.BranchPacket, error) {
	if m.predict != nil {
		return m.predict(ctx, query)
	}
	return nil, nil
}

func (m *mockForestService) RecordOutcome(ctx context.Context, record forest.OutcomeRecord) error {
	if m.recordOutcome != nil {
		return m.recordOutcome(ctx, record)
	}
	return nil
}

func TestForestResolveIntentSkill(t *testing.T) {
	t.Parallel()

	deps := &RetrievalDependencies{
		Forest: &mockForestService{
			resolveIntent: func(_ context.Context, input forest.ResolveIntentInput) (*forest.IntentResolution, error) {
				return &forest.IntentResolution{
					Query:         input.Query,
					PrimaryIntent: "ship safer retries",
					ActiveRoots:   []string{"root-1"},
				}, nil
			},
		},
	}

	skill := NewForestResolveIntentSkill(deps)
	input, _ := json.Marshal(forest.ResolveIntentInput{Query: "make retries safer"})
	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	output := result.(*forest.IntentResolution)
	if output.PrimaryIntent != "ship safer retries" {
		t.Fatalf("unexpected primary intent: %s", output.PrimaryIntent)
	}
}

func TestForestRecallSkill(t *testing.T) {
	t.Parallel()

	deps := &RetrievalDependencies{
		Forest: &mockForestService{
			retrieve: func(_ context.Context, query forest.Query) ([]*forest.BranchPacket, error) {
				return []*forest.BranchPacket{
					{Branch: &forest.Branch{ID: "branch-1", Family: forest.TreeFamilyDecision, Summary: query.Query}},
				}, nil
			},
		},
	}

	skill := NewForestRecallSkill(deps)
	input, _ := json.Marshal(ForestRecallInput{Query: "retry policy"})
	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}

	output := result.(*ForestRecallOutput)
	if len(output.Packets) != 1 {
		t.Fatalf("expected 1 packet, got %d", len(output.Packets))
	}
}

func TestForestRecordOutcomeSkill(t *testing.T) {
	t.Parallel()

	recorded := false
	deps := &RetrievalDependencies{
		Forest: &mockForestService{
			recordOutcome: func(_ context.Context, record forest.OutcomeRecord) error {
				recorded = record.BranchID == "branch-1" && record.Status == forest.OutcomeStatusSucceeded
				return nil
			},
		},
	}

	skill := NewForestRecordOutcomeSkill(deps)
	input, _ := json.Marshal(ForestOutcomeInput{
		BranchID: "branch-1",
		Status:   "succeeded",
		Summary:  "tests passed",
	})
	result, err := skill.Handler(context.Background(), input)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !recorded {
		t.Fatal("expected record outcome to be invoked")
	}
	if !result.(*ForestOutcomeOutput).Recorded {
		t.Fatal("expected recorded=true")
	}
}
