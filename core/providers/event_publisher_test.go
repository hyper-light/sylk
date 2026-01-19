package providers

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
)

// =============================================================================
// Test Helpers
// =============================================================================

// testEventSubscriber captures events for testing
type testEventSubscriber struct {
	id         string
	eventTypes []events.EventType
	events     []*events.ActivityEvent
	mu         sync.Mutex
}

func newTestSubscriber(id string, eventTypes ...events.EventType) *testEventSubscriber {
	return &testEventSubscriber{
		id:         id,
		eventTypes: eventTypes,
		events:     make([]*events.ActivityEvent, 0),
	}
}

func (s *testEventSubscriber) ID() string {
	return s.id
}

func (s *testEventSubscriber) EventTypes() []events.EventType {
	return s.eventTypes
}

func (s *testEventSubscriber) OnEvent(event *events.ActivityEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *testEventSubscriber) getEvents() []*events.ActivityEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]*events.ActivityEvent, len(s.events))
	copy(result, s.events)
	return result
}

func (s *testEventSubscriber) waitForEvents(count int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		if len(s.events) >= count {
			s.mu.Unlock()
			return true
		}
		s.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// createTestBus creates an event bus for testing
func createTestBus() *events.ActivityEventBus {
	bus := events.NewActivityEventBus(100)
	bus.Start()
	return bus
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
		{
			name:         "normal_rate",
			outputTokens: 100,
			duration:     1 * time.Second,
			wantMin:      99,
			wantMax:      101,
		},
		{
			name:         "high_rate",
			outputTokens: 1000,
			duration:     500 * time.Millisecond,
			wantMin:      1990,
			wantMax:      2010,
		},
		{
			name:         "zero_duration",
			outputTokens: 100,
			duration:     0,
			wantMin:      0,
			wantMax:      0,
		},
		{
			name:         "negative_duration",
			outputTokens: 100,
			duration:     -1 * time.Second,
			wantMin:      0,
			wantMax:      0,
		},
		{
			name:         "zero_tokens",
			outputTokens: 0,
			duration:     1 * time.Second,
			wantMin:      0,
			wantMax:      0,
		},
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
		{
			name:         "both_positive",
			inputTokens:  100,
			outputTokens: 50,
			want:         150,
		},
		{
			name:         "zero_input",
			inputTokens:  0,
			outputTokens: 50,
			want:         50,
		},
		{
			name:         "zero_output",
			inputTokens:  100,
			outputTokens: 0,
			want:         100,
		},
		{
			name:         "both_zero",
			inputTokens:  0,
			outputTokens: 0,
			want:         0,
		},
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
		bus := createTestBus()
		defer bus.Close()

		publisher := NewLLMEventPublisher(bus)
		if publisher == nil {
			t.Fatal("expected non-nil publisher")
		}
		if publisher.bus != bus {
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
	bus := createTestBus()
	defer bus.Close()

	sub := newTestSubscriber("test-sub", events.EventTypeLLMRequest)
	bus.Subscribe(sub)

	publisher := NewLLMEventPublisher(bus)

	t.Run("publishes_request_event", func(t *testing.T) {
		err := publisher.PublishLLMRequest("session-1", "agent-1", "gpt-4", 1000)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !sub.waitForEvents(1, 500*time.Millisecond) {
			t.Fatal("timeout waiting for event")
		}

		evts := sub.getEvents()
		if len(evts) == 0 {
			t.Fatal("expected at least one event")
		}

		evt := evts[0]
		if evt.EventType != events.EventTypeLLMRequest {
			t.Errorf("expected EventTypeLLMRequest, got %v", evt.EventType)
		}
		if evt.SessionID != "session-1" {
			t.Errorf("expected session-1, got %s", evt.SessionID)
		}
		if evt.AgentID != "agent-1" {
			t.Errorf("expected agent-1, got %s", evt.AgentID)
		}
		if evt.Data["model"] != "gpt-4" {
			t.Errorf("expected gpt-4, got %v", evt.Data["model"])
		}
		if evt.Data["input_tokens"] != 1000 {
			t.Errorf("expected 1000 tokens, got %v", evt.Data["input_tokens"])
		}
		if evt.Category != "llm" {
			t.Errorf("expected category 'llm', got %s", evt.Category)
		}
	})

	t.Run("nil_publisher_returns_error", func(t *testing.T) {
		var nilPublisher *LLMEventPublisher
		err := nilPublisher.PublishLLMRequest("session", "agent", "model", 100)
		if err == nil {
			t.Error("expected error for nil publisher")
		}
	})
}

func TestLLMEventPublisher_PublishLLMResponse(t *testing.T) {
	bus := createTestBus()
	defer bus.Close()

	sub := newTestSubscriber("test-sub", events.EventTypeLLMResponse)
	bus.Subscribe(sub)

	publisher := NewLLMEventPublisher(bus)

	t.Run("publishes_response_event", func(t *testing.T) {
		duration := 500 * time.Millisecond
		err := publisher.PublishLLMResponse("session-1", "agent-1", "claude-3-opus", 1000, 500, duration)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !sub.waitForEvents(1, 500*time.Millisecond) {
			t.Fatal("timeout waiting for event")
		}

		evts := sub.getEvents()
		if len(evts) == 0 {
			t.Fatal("expected at least one event")
		}

		evt := evts[0]
		if evt.EventType != events.EventTypeLLMResponse {
			t.Errorf("expected EventTypeLLMResponse, got %v", evt.EventType)
		}
		if evt.SessionID != "session-1" {
			t.Errorf("expected session-1, got %s", evt.SessionID)
		}
		if evt.AgentID != "agent-1" {
			t.Errorf("expected agent-1, got %s", evt.AgentID)
		}
		if evt.Outcome != events.OutcomeSuccess {
			t.Errorf("expected OutcomeSuccess, got %v", evt.Outcome)
		}
		if evt.Data["model"] != "claude-3-opus" {
			t.Errorf("expected claude-3-opus, got %v", evt.Data["model"])
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
		err := publisher.PublishLLMResponse("session", "agent", "model", 100, 50, 0)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("nil_publisher_returns_error", func(t *testing.T) {
		var nilPublisher *LLMEventPublisher
		err := nilPublisher.PublishLLMResponse("session", "agent", "model", 100, 50, time.Second)
		if err == nil {
			t.Error("expected error for nil publisher")
		}
	})
}

func TestLLMEventPublisher_PublishLLMError(t *testing.T) {
	bus := createTestBus()
	defer bus.Close()

	sub := newTestSubscriber("test-sub", events.EventTypeAgentError)
	bus.Subscribe(sub)

	publisher := NewLLMEventPublisher(bus)

	t.Run("publishes_error_event", func(t *testing.T) {
		testErr := errors.New("API rate limit exceeded")
		err := publisher.PublishLLMError("session-1", "agent-1", "gpt-4", testErr)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !sub.waitForEvents(1, 500*time.Millisecond) {
			t.Fatal("timeout waiting for event")
		}

		evts := sub.getEvents()
		if len(evts) == 0 {
			t.Fatal("expected at least one event")
		}

		evt := evts[0]
		if evt.EventType != events.EventTypeAgentError {
			t.Errorf("expected EventTypeAgentError, got %v", evt.EventType)
		}
		if evt.SessionID != "session-1" {
			t.Errorf("expected session-1, got %s", evt.SessionID)
		}
		if evt.AgentID != "agent-1" {
			t.Errorf("expected agent-1, got %s", evt.AgentID)
		}
		if evt.Outcome != events.OutcomeFailure {
			t.Errorf("expected OutcomeFailure, got %v", evt.Outcome)
		}
		if evt.Data["model"] != "gpt-4" {
			t.Errorf("expected gpt-4, got %v", evt.Data["model"])
		}
		if evt.Data["error"] != "API rate limit exceeded" {
			t.Errorf("expected error message, got %v", evt.Data["error"])
		}
		if evt.Data["error_type"] != "llm_api_error" {
			t.Errorf("expected error_type llm_api_error, got %v", evt.Data["error_type"])
		}
	})

	t.Run("nil_error_returns_error", func(t *testing.T) {
		err := publisher.PublishLLMError("session", "agent", "model", nil)
		if err == nil {
			t.Error("expected error for nil error parameter")
		}
	})

	t.Run("nil_publisher_returns_error", func(t *testing.T) {
		var nilPublisher *LLMEventPublisher
		err := nilPublisher.PublishLLMError("session", "agent", "model", errors.New("test"))
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
		bus := createTestBus()
		defer bus.Close()

		publisher := NewLLMEventPublisher(bus)
		hook := NewLLMEventPublisherHook(publisher)
		if hook == nil {
			t.Fatal("expected non-nil hook")
		}
		if hook.publisher != publisher {
			t.Error("expected hook to have the provided publisher")
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
	bus := createTestBus()
	defer bus.Close()

	sub := newTestSubscriber("test-sub", events.EventTypeLLMRequest)
	bus.Subscribe(sub)

	publisher := NewLLMEventPublisher(bus)
	hook := NewLLMEventPublisherHook(publisher)

	hook.OnRequest("session-1", "agent-1", "gpt-4", 1000)

	if !sub.waitForEvents(1, 500*time.Millisecond) {
		t.Fatal("timeout waiting for event")
	}

	evts := sub.getEvents()
	if len(evts) == 0 {
		t.Fatal("expected at least one event")
	}
	if evts[0].EventType != events.EventTypeLLMRequest {
		t.Errorf("expected EventTypeLLMRequest, got %v", evts[0].EventType)
	}
}

func TestLLMEventPublisherHook_OnResponse(t *testing.T) {
	bus := createTestBus()
	defer bus.Close()

	sub := newTestSubscriber("test-sub", events.EventTypeLLMResponse)
	bus.Subscribe(sub)

	publisher := NewLLMEventPublisher(bus)
	hook := NewLLMEventPublisherHook(publisher)

	hook.OnResponse("session-1", "agent-1", "claude-3", 1000, 500, 500*time.Millisecond)

	if !sub.waitForEvents(1, 500*time.Millisecond) {
		t.Fatal("timeout waiting for event")
	}

	evts := sub.getEvents()
	if len(evts) == 0 {
		t.Fatal("expected at least one event")
	}
	if evts[0].EventType != events.EventTypeLLMResponse {
		t.Errorf("expected EventTypeLLMResponse, got %v", evts[0].EventType)
	}
}

func TestLLMEventPublisherHook_OnError(t *testing.T) {
	bus := createTestBus()
	defer bus.Close()

	sub := newTestSubscriber("test-sub", events.EventTypeAgentError)
	bus.Subscribe(sub)

	publisher := NewLLMEventPublisher(bus)
	hook := NewLLMEventPublisherHook(publisher)

	hook.OnError("session-1", "agent-1", "gpt-4", errors.New("connection timeout"))

	if !sub.waitForEvents(1, 500*time.Millisecond) {
		t.Fatal("timeout waiting for event")
	}

	evts := sub.getEvents()
	if len(evts) == 0 {
		t.Fatal("expected at least one event")
	}
	if evts[0].EventType != events.EventTypeAgentError {
		t.Errorf("expected EventTypeAgentError, got %v", evts[0].EventType)
	}
}

func TestLLMEventPublisherHook_NilSafety(t *testing.T) {
	t.Run("nil_hook", func(t *testing.T) {
		var hook *LLMEventPublisherHook
		// Should not panic
		hook.OnRequest("session", "agent", "model", 100)
		hook.OnResponse("session", "agent", "model", 100, 50, time.Second)
		hook.OnError("session", "agent", "model", errors.New("test"))
	})

	t.Run("hook_with_nil_publisher", func(t *testing.T) {
		hook := &LLMEventPublisherHook{publisher: nil}
		// Should not panic
		hook.OnRequest("session", "agent", "model", 100)
		hook.OnResponse("session", "agent", "model", 100, 50, time.Second)
		hook.OnError("session", "agent", "model", errors.New("test"))
	})
}

// =============================================================================
// NoOpLLMEventHook Tests
// =============================================================================

func TestNoOpLLMEventHook(t *testing.T) {
	hook := &NoOpLLMEventHook{}

	// Should not panic, no state to verify
	hook.OnRequest("session", "agent", "model", 100)
	hook.OnResponse("session", "agent", "model", 100, 50, time.Second)
	hook.OnError("session", "agent", "model", errors.New("test"))
}

// =============================================================================
// Interface Compliance Tests
// =============================================================================

func TestLLMProviderEventHook_InterfaceCompliance(t *testing.T) {
	t.Run("LLMEventPublisherHook", func(t *testing.T) {
		bus := createTestBus()
		defer bus.Close()

		publisher := NewLLMEventPublisher(bus)
		hook := NewLLMEventPublisherHook(publisher)

		var _ LLMProviderEventHook = hook
	})

	t.Run("NoOpLLMEventHook", func(t *testing.T) {
		hook := &NoOpLLMEventHook{}
		var _ LLMProviderEventHook = hook
	})
}

// =============================================================================
// Concurrent Publishing Tests
// =============================================================================

func TestLLMEventPublisher_Concurrent(t *testing.T) {
	bus := createTestBus()
	defer bus.Close()

	sub := newTestSubscriber("test-sub") // wildcard
	bus.Subscribe(sub)

	publisher := NewLLMEventPublisher(bus)

	const numGoroutines = 50
	var wg sync.WaitGroup

	for i := 0; i < numGoroutines; i++ {
		wg.Add(3)

		go func(idx int) {
			defer wg.Done()
			_ = publisher.PublishLLMRequest("session", "agent", "model", idx*100)
		}(i)

		go func(idx int) {
			defer wg.Done()
			_ = publisher.PublishLLMResponse("session", "agent", "model", idx*100, idx*50, time.Duration(idx)*time.Millisecond)
		}(i)

		go func(idx int) {
			defer wg.Done()
			_ = publisher.PublishLLMError("session", "agent", "model", errors.New("test error"))
		}(i)
	}

	wg.Wait()

	// Give time for events to be processed
	time.Sleep(100 * time.Millisecond)

	// Note: Due to debouncing in ActivityEventBus, we may not receive all events
	// This test primarily verifies no race conditions or panics
	evts := sub.getEvents()
	if len(evts) == 0 {
		t.Error("expected at least some events to be received")
	}
}

// =============================================================================
// Integration Test
// =============================================================================

func TestLLMEventPublisher_FullWorkflow(t *testing.T) {
	bus := createTestBus()
	defer bus.Close()

	sub := newTestSubscriber("test-sub") // wildcard
	bus.Subscribe(sub)

	publisher := NewLLMEventPublisher(bus)
	hook := NewLLMEventPublisherHook(publisher)

	// Simulate a typical LLM request/response cycle
	sessionID := "session-123"
	agentID := "coder-agent"
	model := "claude-3-opus"

	// 1. Request initiated
	hook.OnRequest(sessionID, agentID, model, 1500)

	// 2. Wait for response
	time.Sleep(50 * time.Millisecond)

	// 3. Response received
	hook.OnResponse(sessionID, agentID, model, 1500, 750, 500*time.Millisecond)

	// Wait for events
	if !sub.waitForEvents(2, time.Second) {
		t.Fatal("timeout waiting for events")
	}

	evts := sub.getEvents()
	if len(evts) < 2 {
		t.Fatalf("expected at least 2 events, got %d", len(evts))
	}

	// Verify event sequence
	var hasRequest, hasResponse bool
	for _, evt := range evts {
		if evt.EventType == events.EventTypeLLMRequest {
			hasRequest = true
			if evt.SessionID != sessionID {
				t.Error("request event has wrong session ID")
			}
		}
		if evt.EventType == events.EventTypeLLMResponse {
			hasResponse = true
			if evt.SessionID != sessionID {
				t.Error("response event has wrong session ID")
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
	bus := createTestBus()
	defer bus.Close()

	sub := newTestSubscriber("test-sub") // wildcard
	bus.Subscribe(sub)

	publisher := NewLLMEventPublisher(bus)
	hook := NewLLMEventPublisherHook(publisher)

	sessionID := "session-456"
	agentID := "writer-agent"
	model := "gpt-4"

	// 1. Request initiated
	hook.OnRequest(sessionID, agentID, model, 2000)

	// 2. Wait, then error occurs
	time.Sleep(50 * time.Millisecond)

	// 3. Error received
	hook.OnError(sessionID, agentID, model, errors.New("context length exceeded"))

	// Wait for events
	if !sub.waitForEvents(2, time.Second) {
		t.Fatal("timeout waiting for events")
	}

	evts := sub.getEvents()

	// Verify error event
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
