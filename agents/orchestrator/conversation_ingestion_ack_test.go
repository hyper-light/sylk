package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/providers"
)

type signalingOrchestratorProvider struct {
	called chan struct{}
}

func (p *signalingOrchestratorProvider) Complete(context.Context, *providers.CompletionRequest) (*providers.CompletionResponse, error) {
	select {
	case p.called <- struct{}{}:
	default:
	}
	return &providers.CompletionResponse{Content: "summarized"}, nil
}

func TestRespondToIngestion_DoesNotSpawnDeferredSummary(t *testing.T) {
	scope := concurrency.NewGoroutineScope(context.Background(), "orchestrator-test", nil)
	defer func() {
		if err := scope.Shutdown(100*time.Millisecond, 200*time.Millisecond); err != nil {
			t.Fatalf("scope shutdown: %v", err)
		}
	}()

	provider := &signalingOrchestratorProvider{called: make(chan struct{}, 1)}
	o := &Orchestrator{
		config:   Config{AgentID: "orchestrator"},
		provider: provider,
		scope:    scope,
	}

	result, err := o.respondToIngestion(context.Background(), &guide.ForwardedRequest{
		CorrelationID: "corr-1",
		Input:         `{"plan_id":"plan-123","query":"ship auth checkout","tasks":[],"execution_layers":[]}`,
	}, map[string]any{
		"ingested":         true,
		"receipt_streamed": true,
		"plan_id":          "plan-123",
		"dag_id":           "dag-456",
		"task_count":       3,
		"layer_count":      2,
	})
	if err != nil {
		t.Fatalf("respondToIngestion() error = %v", err)
	}

	cr, ok := result.(*ConversationResult)
	if !ok {
		t.Fatalf("result type = %T, want *ConversationResult", result)
	}
	want := "Plan plan-123 ingested. DAG dag-456 started with 3 tasks across 2 layers."
	if cr.Response != want {
		t.Fatalf("response = %q, want %q", cr.Response, want)
	}

	select {
	case <-provider.called:
		t.Fatal("expected no deferred ingestion summary provider call")
	case <-time.After(150 * time.Millisecond):
	}
}
