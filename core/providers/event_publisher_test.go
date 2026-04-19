package providers

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/agents/identity"
	"github.com/adalundhe/sylk/core/events"
)

// =============================================================================
// Test helpers — build identities via RebuildForReplay so tests don't
// need a live Factory + registries. RebuildForReplay bypasses
// validation and is the intended construction path for synthesized
// values (session restore, tests, fixtures).
// =============================================================================

func testIdent(kind identity.AgentType, uid string, model identity.ModelID) *identity.AgentIdentity {
	return identity.RebuildForReplay(identity.ReplayAgentIdentity{
		UID:       identity.UID(uid),
		Namespace: "session-123",
		Pod:       identity.PodRef{ID: "guide", Type: identity.PodTypeDaemon},
		Name:      identity.Name("coder-agent"),
		Kind:      kind,
		Category:  identity.CategoryStandalone,
		Model:     model,
		Window:    200_000,
		Labels: identity.Labels{
			identity.LabelKind:      kind.String(),
			identity.LabelPod:       "guide",
			identity.LabelCategory:  identity.CategoryStandalone.String(),
			identity.LabelNamespace: "session-123",
		},
	})
}

func testTask(corr identity.CorrelationID) *identity.TaskRef {
	return identity.RebuildTaskForReplay(identity.ReplayTaskRef{
		UID:         identity.TaskUID("task-" + string(corr)),
		DisplayID:   "task-display",
		Correlation: corr,
		Namespace:   "session-123",
		Labels:      identity.Labels{identity.LabelNamespace: "session-123"},
	})
}

// =============================================================================
// LLMMetrics Tests
// =============================================================================

func TestLLMMetrics_TokensPerSecond(t *testing.T) {
	tests := []struct {
		name         string
		outputTokens int
		duration     time.Duration
		wantMin      float64
		wantMax      float64
	}{
		{"normal_rate", 100, 1 * time.Second, 99, 101},
		{"high_rate", 1000, 500 * time.Millisecond, 1990, 2010},
		{"zero_duration", 100, 0, 0, 0},
		{"negative_duration", 100, -1 * time.Second, 0, 0},
		{"zero_tokens", 0, 1 * time.Second, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &LLMMetrics{
				OutputTokens: tt.outputTokens,
				Duration:     tt.duration,
			}
			got := m.TokensPerSecond()
			if got < tt.wantMin || got > tt.wantMax {
				t.Errorf("TokensPerSecond() = %v, want between %v and %v", got, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestLLMMetrics_TotalTokens(t *testing.T) {
	tests := []struct {
		name         string
		inputTokens  int
		outputTokens int
		want         int
	}{
		{"both_positive", 100, 50, 150},
		{"zero_input", 0, 50, 50},
		{"zero_output", 100, 0, 100},
		{"both_zero", 0, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &LLMMetrics{
				InputTokens:  tt.inputTokens,
				OutputTokens: tt.outputTokens,
			}
			if got := m.TotalTokens(); got != tt.want {
				t.Errorf("TotalTokens() = %v, want %v", got, tt.want)
			}
		})
	}
}

// =============================================================================
// LLMEventPublisher Tests
// =============================================================================

func TestNewLLMEventPublisher(t *testing.T) {
	t.Run("with_valid_bus", func(t *testing.T) {
		collector := events.NewTestActivityCollector()
		publisher := NewLLMEventPublisher(collector)
		if publisher == nil {
			t.Fatal("expected non-nil publisher")
		}
		if publisher.bus != collector {
			t.Error("expected publisher to have the provided bus")
		}
	})

	t.Run("with_nil_bus", func(t *testing.T) {
		publisher := NewLLMEventPublisher(nil)
		if publisher != nil {
			t.Error("expected nil publisher for nil bus")
		}
	})
}

func TestLLMEventPublisher_PublishLLMRequest(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewLLMEventPublisher(collector)
	id := testIdent(identity.AgentTypeGuide, "uid-1", "gpt-4")
	task := testTask("corr-1")

	t.Run("publishes_request_event", func(t *testing.T) {
		err := publisher.PublishLLMRequest(id, task, "gpt-4")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		evts := collector.Events()
		if len(evts) == 0 {
			t.Fatal("expected at least one event")
		}
		evt := evts[0]
		if evt.EventType != events.EventTypeLLMRequest {
			t.Errorf("expected EventTypeLLMRequest, got %v", evt.EventType)
		}
		if evt.SessionID != "session-123" {
			t.Errorf("expected session-123, got %s", evt.SessionID)
		}
		if evt.AgentID != id.Panel() {
			t.Errorf("expected %s, got %s", id.Panel(), evt.AgentID)
		}
		if evt.Identity != id {
			t.Error("expected typed Identity pointer attached")
		}
		if evt.Task != task {
			t.Error("expected typed Task pointer attached")
		}
		if evt.Data["model"] != "gpt-4" {
			t.Errorf("expected gpt-4, got %v", evt.Data["model"])
		}
		if evt.Data["agent_uid"] != "uid-1" {
			t.Errorf("expected agent_uid uid-1, got %v", evt.Data["agent_uid"])
		}
		if evt.Data["task_uid"] != "task-corr-1" {
			t.Errorf("expected task_uid, got %v", evt.Data["task_uid"])
		}
		if evt.Category != "llm" {
			t.Errorf("expected category 'llm', got %s", evt.Category)
		}
	})

	t.Run("nil_publisher_returns_error", func(t *testing.T) {
		var nilPublisher *LLMEventPublisher
		err := nilPublisher.PublishLLMRequest(id, task, "model")
		if err == nil {
			t.Error("expected error for nil publisher")
		}
	})
}

func TestLLMEventPublisher_PublishLLMResponse(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewLLMEventPublisher(collector)
	id := testIdent(identity.AgentTypeGuide, "uid-1", "claude-3-opus")
	task := testTask("corr-1")

	t.Run("publishes_response_event", func(t *testing.T) {
		duration := 500 * time.Millisecond
		err := publisher.PublishLLMResponse(id, task, "claude-3-opus",
			&Usage{InputTokens: 1000, OutputTokens: 500}, duration)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		evts := collector.Events()
		if len(evts) == 0 {
			t.Fatal("expected at least one event")
		}
		evt := evts[0]
		if evt.EventType != events.EventTypeLLMResponse {
			t.Errorf("expected EventTypeLLMResponse, got %v", evt.EventType)
		}
		if evt.Outcome != events.OutcomeSuccess {
			t.Errorf("expected OutcomeSuccess, got %v", evt.Outcome)
		}
		if evt.Data["input_tokens"] != 1000 {
			t.Errorf("expected 1000 input tokens, got %v", evt.Data["input_tokens"])
		}
		if evt.Data["output_tokens"] != 500 {
			t.Errorf("expected 500 output tokens, got %v", evt.Data["output_tokens"])
		}
		if evt.Data["duration_ms"] != int64(500) {
			t.Errorf("expected 500 duration_ms, got %v", evt.Data["duration_ms"])
		}
		tokensPerSec, ok := evt.Data["tokens_per_sec"].(float64)
		if !ok {
			t.Error("expected tokens_per_sec to be float64")
		}
		if tokensPerSec < 900 || tokensPerSec > 1100 {
			t.Errorf("expected tokens_per_sec around 1000, got %v", tokensPerSec)
		}
	})

	t.Run("zero_duration_no_panic", func(t *testing.T) {
		err := publisher.PublishLLMResponse(id, task, "model",
			&Usage{InputTokens: 100, OutputTokens: 50}, 0)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("nil_publisher_returns_error", func(t *testing.T) {
		var nilPublisher *LLMEventPublisher
		err := nilPublisher.PublishLLMResponse(id, task, "model",
			&Usage{InputTokens: 100, OutputTokens: 50}, time.Second)
		if err == nil {
			t.Error("expected error for nil publisher")
		}
	})
}

func TestLLMEventPublisher_PublishLLMError(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewLLMEventPublisher(collector)
	id := testIdent(identity.AgentTypeGuide, "uid-1", "gpt-4")
	task := testTask("corr-1")

	t.Run("publishes_error_event", func(t *testing.T) {
		testErr := errors.New("API rate limit exceeded")
		err := publisher.PublishLLMError(id, task, "gpt-4", testErr, 100*time.Millisecond)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		evts := collector.Events()
		if len(evts) == 0 {
			t.Fatal("expected at least one event")
		}
		evt := evts[0]
		if evt.EventType != events.EventTypeAgentError {
			t.Errorf("expected EventTypeAgentError, got %v", evt.EventType)
		}
		if evt.Outcome != events.OutcomeFailure {
			t.Errorf("expected OutcomeFailure, got %v", evt.Outcome)
		}
		if evt.Content != "LLM error from gpt-4: API rate limit exceeded" {
			t.Errorf("expected friendly event content, got %q", evt.Content)
		}
		if evt.Data["error"] != "API rate limit exceeded" {
			t.Errorf("expected error message, got %v", evt.Data["error"])
		}
		if evt.Data["duration_ms"] != int64(100) {
			t.Errorf("expected duration_ms=100, got %v", evt.Data["duration_ms"])
		}
	})

	t.Run("formats_embedded_provider_json_in_event_content", func(t *testing.T) {
		id2 := testIdent(identity.AgentTypeGuide, "uid-2", "claude-sonnet-4-6")
		task2 := testTask("corr-2")
		testErr := errors.New(`anthropic stream: received error while streaming: {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"},"request_id":"req_test"}`)
		err := publisher.PublishLLMError(id2, task2, "claude-sonnet-4-6", testErr, time.Second)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		evts := collector.Events()
		evt := evts[len(evts)-1]
		if evt.Content != "LLM error from claude-sonnet-4-6: Overloaded (overloaded_error) [req_test]" {
			t.Errorf("expected friendly overloaded content, got %q", evt.Content)
		}
	})

	t.Run("nil_error_returns_error", func(t *testing.T) {
		err := publisher.PublishLLMError(id, task, "model", nil, time.Second)
		if err == nil {
			t.Error("expected error for nil error parameter")
		}
	})

	t.Run("nil_publisher_returns_error", func(t *testing.T) {
		var nilPublisher *LLMEventPublisher
		err := nilPublisher.PublishLLMError(id, task, "model", errors.New("x"), time.Second)
		if err == nil {
			t.Error("expected error for nil publisher")
		}
	})
}

// =============================================================================
// LLMEventPublisherHook Tests
// =============================================================================

func TestNewLLMEventPublisherHook(t *testing.T) {
	t.Run("with_valid_publisher", func(t *testing.T) {
		collector := events.NewTestActivityCollector()
		publisher := NewLLMEventPublisher(collector)
		hook := NewLLMEventPublisherHook(publisher)
		if hook == nil {
			t.Fatal("expected non-nil hook")
		}
	})

	t.Run("with_nil_publisher", func(t *testing.T) {
		hook := NewLLMEventPublisherHook(nil)
		if hook != nil {
			t.Error("expected nil hook for nil publisher")
		}
	})
}

func TestLLMEventPublisherHook_OnRequest(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewLLMEventPublisher(collector)
	hook := NewLLMEventPublisherHook(publisher)
	id := testIdent(identity.AgentTypeGuide, "uid-1", "gpt-4")
	task := testTask("corr-1")

	hook.OnRequest(context.Background(), id, task, "gpt-4")

	evts := collector.Events()
	if len(evts) == 0 {
		t.Fatal("expected at least one event")
	}
	if evts[0].EventType != events.EventTypeLLMRequest {
		t.Errorf("expected EventTypeLLMRequest, got %v", evts[0].EventType)
	}
}

func TestLLMEventPublisherHook_OnResponse(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewLLMEventPublisher(collector)
	hook := NewLLMEventPublisherHook(publisher)
	id := testIdent(identity.AgentTypeGuide, "uid-1", "claude-3")
	task := testTask("corr-1")

	hook.OnResponse(context.Background(), id, task, "claude-3",
		&Usage{InputTokens: 1000, OutputTokens: 500}, 500*time.Millisecond)

	evts := collector.Events()
	if len(evts) == 0 {
		t.Fatal("expected at least one event")
	}
	if evts[0].EventType != events.EventTypeLLMResponse {
		t.Errorf("expected EventTypeLLMResponse, got %v", evts[0].EventType)
	}
}

func TestLLMEventPublisherHook_OnError(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewLLMEventPublisher(collector)
	hook := NewLLMEventPublisherHook(publisher)
	id := testIdent(identity.AgentTypeGuide, "uid-1", "gpt-4")
	task := testTask("corr-1")

	hook.OnError(context.Background(), id, task, "gpt-4",
		errors.New("connection timeout"), 100*time.Millisecond)

	evts := collector.Events()
	if len(evts) == 0 {
		t.Fatal("expected at least one event")
	}
	if evts[0].EventType != events.EventTypeAgentError {
		t.Errorf("expected EventTypeAgentError, got %v", evts[0].EventType)
	}
}

func TestLLMEventPublisherHook_NilSafety(t *testing.T) {
	ctx := context.Background()
	id := testIdent(identity.AgentTypeGuide, "uid-1", "model")
	task := testTask("corr-1")

	t.Run("nil_hook", func(t *testing.T) {
		var hook *LLMEventPublisherHook
		hook.OnRequest(ctx, id, task, "model")
		hook.OnResponse(ctx, id, task, "model", &Usage{InputTokens: 100, OutputTokens: 50}, time.Second)
		hook.OnError(ctx, id, task, "model", errors.New("test"), time.Second)
	})

	t.Run("hook_with_nil_publisher", func(t *testing.T) {
		hook := &LLMEventPublisherHook{publisher: nil}
		hook.OnRequest(ctx, id, task, "model")
		hook.OnResponse(ctx, id, task, "model", &Usage{InputTokens: 100, OutputTokens: 50}, time.Second)
		hook.OnError(ctx, id, task, "model", errors.New("test"), time.Second)
	})
}

// =============================================================================
// NoOpLLMEventHook Tests
// =============================================================================

func TestNoOpLLMEventHook(t *testing.T) {
	hook := &NoOpLLMEventHook{}
	id := testIdent(identity.AgentTypeGuide, "uid-1", "model")
	task := testTask("corr-1")

	hook.OnRequest(context.Background(), id, task, "model")
	hook.OnResponse(context.Background(), id, task, "model",
		&Usage{InputTokens: 100, OutputTokens: 50}, time.Second)
	hook.OnError(context.Background(), id, task, "model",
		errors.New("test"), time.Second)
}

// =============================================================================
// MultiHook Tests
// =============================================================================

type recordingHook struct {
	requests, responses, errors int
	mu                          sync.Mutex
}

func (r *recordingHook) OnRequest(context.Context, *identity.AgentIdentity, *identity.TaskRef, identity.ModelID) {
	r.mu.Lock()
	r.requests++
	r.mu.Unlock()
}

func (r *recordingHook) OnResponse(context.Context, *identity.AgentIdentity, *identity.TaskRef, identity.ModelID, *Usage, time.Duration) {
	r.mu.Lock()
	r.responses++
	r.mu.Unlock()
}

func (r *recordingHook) OnError(context.Context, *identity.AgentIdentity, *identity.TaskRef, identity.ModelID, error, time.Duration) {
	r.mu.Lock()
	r.errors++
	r.mu.Unlock()
}

func TestMultiHook_FansOutToEverySubHook(t *testing.T) {
	h1 := &recordingHook{}
	h2 := &recordingHook{}
	multi := NewMultiHook(h1, h2)
	id := testIdent(identity.AgentTypeGuide, "uid-1", "model")
	task := testTask("corr-1")
	ctx := context.Background()

	multi.OnRequest(ctx, id, task, "model")
	multi.OnResponse(ctx, id, task, "model", &Usage{InputTokens: 1}, time.Second)
	multi.OnError(ctx, id, task, "model", errors.New("x"), time.Second)

	if h1.requests != 1 || h2.requests != 1 {
		t.Fatalf("requests: h1=%d h2=%d", h1.requests, h2.requests)
	}
	if h1.responses != 1 || h2.responses != 1 {
		t.Fatalf("responses: h1=%d h2=%d", h1.responses, h2.responses)
	}
	if h1.errors != 1 || h2.errors != 1 {
		t.Fatalf("errors: h1=%d h2=%d", h1.errors, h2.errors)
	}
}

func TestMultiHook_DropsNilSubHooks(t *testing.T) {
	h1 := &recordingHook{}
	multi := NewMultiHook(nil, h1, nil)
	id := testIdent(identity.AgentTypeGuide, "uid-1", "model")
	task := testTask("corr-1")

	multi.OnRequest(context.Background(), id, task, "model")
	if h1.requests != 1 {
		t.Fatalf("h1 requests=%d, want 1", h1.requests)
	}
}

// =============================================================================
// Interface Compliance Tests
// =============================================================================

func TestLLMProviderEventHook_InterfaceCompliance(t *testing.T) {
	var _ LLMProviderEventHook = (*LLMEventPublisherHook)(nil)
	var _ LLMProviderEventHook = (*NoOpLLMEventHook)(nil)
	var _ LLMProviderEventHook = (*MultiHook)(nil)
}

// =============================================================================
// Concurrent Publishing Tests
// =============================================================================

func TestLLMEventPublisher_Concurrent(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewLLMEventPublisher(collector)

	const numGoroutines = 50
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(3)
		id := testIdent(identity.AgentTypeGuide, "uid-x", "model")
		task := testTask("corr-x")

		go func(idx int) {
			defer wg.Done()
			_ = publisher.PublishLLMRequest(id, task, "model")
		}(i)

		go func(idx int) {
			defer wg.Done()
			_ = publisher.PublishLLMResponse(id, task, "model",
				&Usage{InputTokens: idx * 100, OutputTokens: idx * 50},
				time.Duration(idx)*time.Millisecond)
		}(i)

		go func(idx int) {
			defer wg.Done()
			_ = publisher.PublishLLMError(id, task, "model",
				errors.New("test error"), time.Millisecond)
		}(i)
	}

	wg.Wait()

	evts := collector.Events()
	if len(evts) == 0 {
		t.Error("expected at least some events to be received")
	}
}

// =============================================================================
// Integration Test
// =============================================================================

func TestLLMEventPublisher_FullWorkflow(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewLLMEventPublisher(collector)
	hook := NewLLMEventPublisherHook(publisher)
	id := testIdent(identity.AgentTypeGuide, "uid-wf", "claude-3-opus")
	task := testTask("corr-wf")
	ctx := context.Background()

	hook.OnRequest(ctx, id, task, "claude-3-opus")
	hook.OnResponse(ctx, id, task, "claude-3-opus",
		&Usage{InputTokens: 1500, OutputTokens: 750}, 500*time.Millisecond)

	evts := collector.Events()
	if len(evts) < 2 {
		t.Fatalf("expected at least 2 events, got %d", len(evts))
	}

	var hasRequest, hasResponse bool
	for _, evt := range evts {
		if evt.EventType == events.EventTypeLLMRequest {
			hasRequest = true
			if evt.SessionID != "session-123" {
				t.Error("request event has wrong session ID")
			}
			if evt.Identity != id {
				t.Error("request event missing typed identity")
			}
		}
		if evt.EventType == events.EventTypeLLMResponse {
			hasResponse = true
			if evt.SessionID != "session-123" {
				t.Error("response event has wrong session ID")
			}
			if evt.Task != task {
				t.Error("response event missing typed task")
			}
		}
	}

	if !hasRequest {
		t.Error("expected request event")
	}
	if !hasResponse {
		t.Error("expected response event")
	}
}

func TestLLMEventPublisher_ErrorWorkflow(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewLLMEventPublisher(collector)
	hook := NewLLMEventPublisherHook(publisher)
	id := testIdent(identity.AgentTypeGuide, "uid-err", "gpt-4")
	task := testTask("corr-err")
	ctx := context.Background()

	hook.OnRequest(ctx, id, task, "gpt-4")
	hook.OnError(ctx, id, task, "gpt-4",
		errors.New("context length exceeded"), 42*time.Millisecond)

	evts := collector.Events()
	if len(evts) < 2 {
		t.Fatalf("expected at least 2 events, got %d", len(evts))
	}

	var hasError bool
	for _, evt := range evts {
		if evt.EventType == events.EventTypeAgentError {
			hasError = true
			if evt.Outcome != events.OutcomeFailure {
				t.Error("error event should have OutcomeFailure")
			}
			if evt.Data["error"] != "context length exceeded" {
				t.Errorf("wrong error message: %v", evt.Data["error"])
			}
		}
	}

	if !hasError {
		t.Error("expected error event")
	}
}
