package tools

import (
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
)

// =============================================================================
// Mock Subscriber for Testing
// =============================================================================

// testToolSubscriber implements events.EventSubscriber for testing
type testToolSubscriber struct {
	id         string
	eventTypes []events.EventType
	events     []*events.ActivityEvent
	mu         sync.Mutex
}

func (s *testToolSubscriber) ID() string {
	return s.id
}

func (s *testToolSubscriber) EventTypes() []events.EventType {
	return s.eventTypes
}

func (s *testToolSubscriber) OnEvent(event *events.ActivityEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *testToolSubscriber) getEvents() []*events.ActivityEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*events.ActivityEvent{}, s.events...)
}

// =============================================================================
// NewToolEventPublisher Tests
// =============================================================================

func TestNewToolEventPublisher(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	defer bus.Close()

	publisher := NewToolEventPublisher(bus)

	if publisher == nil {
		t.Fatal("Expected non-nil publisher")
	}

	if publisher.bus != bus {
		t.Error("Expected bus to be set")
	}
}

func TestToolEventPublisher_Bus(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	defer bus.Close()

	publisher := NewToolEventPublisher(bus)

	if publisher.Bus() != bus {
		t.Error("Expected Bus() to return the underlying bus")
	}
}

// =============================================================================
// PublishToolCall Tests
// =============================================================================

func TestToolEventPublisher_PublishToolCall(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &testToolSubscriber{
		id:         "test-sub",
		eventTypes: []events.EventType{events.EventTypeToolCall},
	}
	bus.Subscribe(sub)

	publisher := NewToolEventPublisher(bus)

	params := map[string]any{
		"path":    "/tmp/test.txt",
		"content": "Hello, world!",
	}

	err := publisher.PublishToolCall("session-123", "agent-1", "write_file", params)
	if err != nil {
		t.Fatalf("PublishToolCall failed: %v", err)
	}

	// Wait for event delivery
	time.Sleep(150 * time.Millisecond)

	receivedEvents := sub.getEvents()
	if len(receivedEvents) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(receivedEvents))
	}

	event := receivedEvents[0]
	if event.EventType != events.EventTypeToolCall {
		t.Errorf("Expected EventTypeToolCall, got %v", event.EventType)
	}
	if event.SessionID != "session-123" {
		t.Errorf("Expected SessionID 'session-123', got %q", event.SessionID)
	}
	if event.AgentID != "agent-1" {
		t.Errorf("Expected AgentID 'agent-1', got %q", event.AgentID)
	}
	if event.Content != "Tool call: write_file" {
		t.Errorf("Expected Content 'Tool call: write_file', got %q", event.Content)
	}
	if event.Outcome != events.OutcomePending {
		t.Errorf("Expected OutcomePending, got %v", event.Outcome)
	}
	if event.Data == nil {
		t.Fatal("Expected Data to be non-nil")
	}
	if event.Data["tool_name"] != "write_file" {
		t.Errorf("Expected tool_name 'write_file', got %v", event.Data["tool_name"])
	}
	if paramsData, ok := event.Data["params"].(map[string]any); !ok {
		t.Errorf("Expected params to be map[string]any, got %T", event.Data["params"])
	} else {
		if paramsData["path"] != "/tmp/test.txt" {
			t.Errorf("Expected params.path '/tmp/test.txt', got %v", paramsData["path"])
		}
	}
}

func TestToolEventPublisher_PublishToolCall_NilBus(t *testing.T) {
	publisher := &ToolEventPublisher{
		bus: nil,
	}

	err := publisher.PublishToolCall("session", "agent", "tool", nil)
	if err == nil {
		t.Error("Expected error when bus is nil")
	}
}

func TestToolEventPublisher_PublishToolCall_NilParams(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &testToolSubscriber{
		id:         "test-sub",
		eventTypes: []events.EventType{events.EventTypeToolCall},
	}
	bus.Subscribe(sub)

	publisher := NewToolEventPublisher(bus)

	err := publisher.PublishToolCall("session", "agent", "tool", nil)
	if err != nil {
		t.Fatalf("PublishToolCall should accept nil params: %v", err)
	}

	// Wait for event delivery
	time.Sleep(150 * time.Millisecond)

	receivedEvents := sub.getEvents()
	if len(receivedEvents) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(receivedEvents))
	}

	// Params is stored as provided (nil becomes nil in the map)
	params := receivedEvents[0].Data["params"]
	if params != nil {
		// If it's a map, it should be empty or nil
		if m, ok := params.(map[string]any); ok && len(m) > 0 {
			t.Errorf("Expected params to be nil or empty, got %v", params)
		}
	}
}

func TestToolEventPublisher_PublishToolCall_EmptyParams(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &testToolSubscriber{
		id:         "test-sub",
		eventTypes: []events.EventType{events.EventTypeToolCall},
	}
	bus.Subscribe(sub)

	publisher := NewToolEventPublisher(bus)

	err := publisher.PublishToolCall("session", "agent", "tool", map[string]any{})
	if err != nil {
		t.Fatalf("PublishToolCall should accept empty params: %v", err)
	}

	// Wait for event delivery
	time.Sleep(150 * time.Millisecond)

	receivedEvents := sub.getEvents()
	if len(receivedEvents) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(receivedEvents))
	}

	if paramsData, ok := receivedEvents[0].Data["params"].(map[string]any); !ok {
		t.Errorf("Expected params to be map[string]any, got %T", receivedEvents[0].Data["params"])
	} else if len(paramsData) != 0 {
		t.Errorf("Expected empty params, got %d entries", len(paramsData))
	}
}

// =============================================================================
// PublishToolResult Tests
// =============================================================================

func TestToolEventPublisher_PublishToolResult_Success(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &testToolSubscriber{
		id:         "test-sub",
		eventTypes: []events.EventType{events.EventTypeToolResult},
	}
	bus.Subscribe(sub)

	publisher := NewToolEventPublisher(bus)

	result := map[string]any{
		"bytes_written": 1024,
		"path":          "/tmp/test.txt",
	}

	err := publisher.PublishToolResult("session-456", "agent-2", "write_file", result, events.OutcomeSuccess)
	if err != nil {
		t.Fatalf("PublishToolResult failed: %v", err)
	}

	// Wait for event delivery
	time.Sleep(150 * time.Millisecond)

	receivedEvents := sub.getEvents()
	if len(receivedEvents) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(receivedEvents))
	}

	event := receivedEvents[0]
	if event.EventType != events.EventTypeToolResult {
		t.Errorf("Expected EventTypeToolResult, got %v", event.EventType)
	}
	if event.SessionID != "session-456" {
		t.Errorf("Expected SessionID 'session-456', got %q", event.SessionID)
	}
	if event.AgentID != "agent-2" {
		t.Errorf("Expected AgentID 'agent-2', got %q", event.AgentID)
	}
	if event.Content != "Tool result: write_file" {
		t.Errorf("Expected Content 'Tool result: write_file', got %q", event.Content)
	}
	if event.Outcome != events.OutcomeSuccess {
		t.Errorf("Expected OutcomeSuccess, got %v", event.Outcome)
	}
	if event.Data == nil {
		t.Fatal("Expected Data to be non-nil")
	}
	if event.Data["tool_name"] != "write_file" {
		t.Errorf("Expected tool_name 'write_file', got %v", event.Data["tool_name"])
	}
}

func TestToolEventPublisher_PublishToolResult_Failure(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &testToolSubscriber{
		id:         "test-sub",
		eventTypes: []events.EventType{events.EventTypeToolResult},
	}
	bus.Subscribe(sub)

	publisher := NewToolEventPublisher(bus)

	result := "permission denied"

	err := publisher.PublishToolResult("session", "agent", "write_file", result, events.OutcomeFailure)
	if err != nil {
		t.Fatalf("PublishToolResult failed: %v", err)
	}

	// Wait for event delivery
	time.Sleep(150 * time.Millisecond)

	receivedEvents := sub.getEvents()
	if len(receivedEvents) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(receivedEvents))
	}

	if receivedEvents[0].Outcome != events.OutcomeFailure {
		t.Errorf("Expected OutcomeFailure, got %v", receivedEvents[0].Outcome)
	}
	if receivedEvents[0].Data["result"] != "permission denied" {
		t.Errorf("Expected result 'permission denied', got %v", receivedEvents[0].Data["result"])
	}
}

func TestToolEventPublisher_PublishToolResult_NilBus(t *testing.T) {
	publisher := &ToolEventPublisher{
		bus: nil,
	}

	err := publisher.PublishToolResult("session", "agent", "tool", nil, events.OutcomeSuccess)
	if err == nil {
		t.Error("Expected error when bus is nil")
	}
}

func TestToolEventPublisher_PublishToolResult_NilResult(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &testToolSubscriber{
		id:         "test-sub",
		eventTypes: []events.EventType{events.EventTypeToolResult},
	}
	bus.Subscribe(sub)

	publisher := NewToolEventPublisher(bus)

	err := publisher.PublishToolResult("session", "agent", "tool", nil, events.OutcomeSuccess)
	if err != nil {
		t.Fatalf("PublishToolResult should accept nil result: %v", err)
	}

	// Wait for event delivery
	time.Sleep(150 * time.Millisecond)

	receivedEvents := sub.getEvents()
	if len(receivedEvents) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(receivedEvents))
	}

	if receivedEvents[0].Data["result"] != nil {
		t.Errorf("Expected result to be nil, got %v", receivedEvents[0].Data["result"])
	}
}

func TestToolEventPublisher_PublishToolResult_VariousResultTypes(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &testToolSubscriber{
		id:         "test-sub",
		eventTypes: []events.EventType{events.EventTypeToolResult},
	}
	bus.Subscribe(sub)

	publisher := NewToolEventPublisher(bus)

	testCases := []struct {
		name   string
		result any
	}{
		{"string", "hello"},
		{"int", 42},
		{"float", 3.14},
		{"bool", true},
		{"slice", []string{"a", "b", "c"}},
		{"map", map[string]int{"x": 1, "y": 2}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := publisher.PublishToolResult("session", "agent", "tool", tc.result, events.OutcomeSuccess)
			if err != nil {
				t.Fatalf("PublishToolResult failed for %s: %v", tc.name, err)
			}
		})
	}
}

// =============================================================================
// PublishToolTimeout Tests
// =============================================================================

func TestToolEventPublisher_PublishToolTimeout(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &testToolSubscriber{
		id:         "test-sub",
		eventTypes: []events.EventType{events.EventTypeToolTimeout},
	}
	bus.Subscribe(sub)

	publisher := NewToolEventPublisher(bus)

	timeout := 30 * time.Second

	err := publisher.PublishToolTimeout("session-789", "agent-3", "slow_operation", timeout)
	if err != nil {
		t.Fatalf("PublishToolTimeout failed: %v", err)
	}

	// Wait for event delivery
	time.Sleep(150 * time.Millisecond)

	receivedEvents := sub.getEvents()
	if len(receivedEvents) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(receivedEvents))
	}

	event := receivedEvents[0]
	if event.EventType != events.EventTypeToolTimeout {
		t.Errorf("Expected EventTypeToolTimeout, got %v", event.EventType)
	}
	if event.SessionID != "session-789" {
		t.Errorf("Expected SessionID 'session-789', got %q", event.SessionID)
	}
	if event.AgentID != "agent-3" {
		t.Errorf("Expected AgentID 'agent-3', got %q", event.AgentID)
	}
	if event.Content != "Tool timeout: slow_operation after 30s" {
		t.Errorf("Unexpected Content: %q", event.Content)
	}
	if event.Outcome != events.OutcomeFailure {
		t.Errorf("Expected OutcomeFailure, got %v", event.Outcome)
	}
	if event.Data == nil {
		t.Fatal("Expected Data to be non-nil")
	}
	if event.Data["tool_name"] != "slow_operation" {
		t.Errorf("Expected tool_name 'slow_operation', got %v", event.Data["tool_name"])
	}
	if ms, ok := event.Data["timeout_ms"].(int64); !ok || ms != 30000 {
		t.Errorf("Expected timeout_ms 30000, got %v", event.Data["timeout_ms"])
	}
	if secs, ok := event.Data["timeout_seconds"].(float64); !ok || secs != 30.0 {
		t.Errorf("Expected timeout_seconds 30.0, got %v", event.Data["timeout_seconds"])
	}
}

func TestToolEventPublisher_PublishToolTimeout_NilBus(t *testing.T) {
	publisher := &ToolEventPublisher{
		bus: nil,
	}

	err := publisher.PublishToolTimeout("session", "agent", "tool", 5*time.Second)
	if err == nil {
		t.Error("Expected error when bus is nil")
	}
}

func TestToolEventPublisher_PublishToolTimeout_ZeroDuration(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &testToolSubscriber{
		id:         "test-sub",
		eventTypes: []events.EventType{events.EventTypeToolTimeout},
	}
	bus.Subscribe(sub)

	publisher := NewToolEventPublisher(bus)

	err := publisher.PublishToolTimeout("session", "agent", "tool", 0)
	if err != nil {
		t.Fatalf("PublishToolTimeout should accept zero duration: %v", err)
	}

	// Wait for event delivery
	time.Sleep(150 * time.Millisecond)

	receivedEvents := sub.getEvents()
	if len(receivedEvents) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(receivedEvents))
	}

	if ms, ok := receivedEvents[0].Data["timeout_ms"].(int64); !ok || ms != 0 {
		t.Errorf("Expected timeout_ms 0, got %v", receivedEvents[0].Data["timeout_ms"])
	}
}

func TestToolEventPublisher_PublishToolTimeout_SubMillisecond(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &testToolSubscriber{
		id:         "test-sub",
		eventTypes: []events.EventType{events.EventTypeToolTimeout},
	}
	bus.Subscribe(sub)

	publisher := NewToolEventPublisher(bus)

	// 500 microseconds
	err := publisher.PublishToolTimeout("session", "agent", "tool", 500*time.Microsecond)
	if err != nil {
		t.Fatalf("PublishToolTimeout failed: %v", err)
	}

	// Wait for event delivery
	time.Sleep(150 * time.Millisecond)

	receivedEvents := sub.getEvents()
	if len(receivedEvents) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(receivedEvents))
	}

	// Milliseconds should be 0 since 500us < 1ms
	if ms, ok := receivedEvents[0].Data["timeout_ms"].(int64); !ok || ms != 0 {
		t.Errorf("Expected timeout_ms 0, got %v", receivedEvents[0].Data["timeout_ms"])
	}

	// Seconds should be 0.0005
	if secs, ok := receivedEvents[0].Data["timeout_seconds"].(float64); !ok || secs != 0.0005 {
		t.Errorf("Expected timeout_seconds 0.0005, got %v", receivedEvents[0].Data["timeout_seconds"])
	}
}

// =============================================================================
// Event ID Uniqueness Tests
// =============================================================================

func TestToolEventPublisher_EventIDsAreUnique(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &testToolSubscriber{
		id:         "test-sub",
		eventTypes: []events.EventType{},
	}
	bus.Subscribe(sub)

	publisher := NewToolEventPublisher(bus)

	// Publish multiple events with different sessions to avoid debouncing
	// (debouncer keys on event_type:agent_id:session_id)
	_ = publisher.PublishToolCall("session-1", "agent-1", "tool1", nil)
	_ = publisher.PublishToolCall("session-2", "agent-2", "tool2", nil)
	_ = publisher.PublishToolResult("session-3", "agent-3", "tool1", "result", events.OutcomeSuccess)
	_ = publisher.PublishToolTimeout("session-4", "agent-4", "tool3", time.Second)

	// Wait for event delivery
	time.Sleep(200 * time.Millisecond)

	receivedEvents := sub.getEvents()
	if len(receivedEvents) < 4 {
		t.Fatalf("Expected at least 4 events, got %d", len(receivedEvents))
	}

	// Check all event IDs are unique
	seen := make(map[string]bool)
	for _, event := range receivedEvents {
		if event.ID == "" {
			t.Error("Event ID should not be empty")
		}
		if seen[event.ID] {
			t.Errorf("Duplicate event ID found: %s", event.ID)
		}
		seen[event.ID] = true
	}
}

// =============================================================================
// Concurrent Publishing Tests
// =============================================================================

func TestToolEventPublisher_ConcurrentPublishing(t *testing.T) {
	bus := events.NewActivityEventBus(1000)
	bus.Start()
	defer bus.Close()

	sub := &testToolSubscriber{
		id:         "test-sub",
		eventTypes: []events.EventType{},
	}
	bus.Subscribe(sub)

	publisher := NewToolEventPublisher(bus)

	wg := sync.WaitGroup{}
	goroutines := 10
	eventsPerGoroutine := 10

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < eventsPerGoroutine; j++ {
				switch j % 3 {
				case 0:
					_ = publisher.PublishToolCall("session", "agent", "tool", nil)
				case 1:
					_ = publisher.PublishToolResult("session", "agent", "tool", "result", events.OutcomeSuccess)
				case 2:
					_ = publisher.PublishToolTimeout("session", "agent", "tool", time.Second)
				}
			}
		}(i)
	}

	wg.Wait()

	// Wait for event delivery
	time.Sleep(500 * time.Millisecond)

	// Should not panic and should receive at least some events
	receivedEvents := sub.getEvents()
	if len(receivedEvents) == 0 {
		t.Error("Expected at least some events to be received")
	}
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestToolEventPublisher_FullToolLifecycle(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &testToolSubscriber{
		id:         "test-sub",
		eventTypes: []events.EventType{},
	}
	bus.Subscribe(sub)

	publisher := NewToolEventPublisher(bus)

	// Simulate a tool lifecycle: call -> result
	params := map[string]any{"path": "/tmp/test.txt"}
	err := publisher.PublishToolCall("session", "agent", "read_file", params)
	if err != nil {
		t.Fatalf("PublishToolCall failed: %v", err)
	}

	result := map[string]any{"content": "file contents", "size": 1024}
	err = publisher.PublishToolResult("session", "agent", "read_file", result, events.OutcomeSuccess)
	if err != nil {
		t.Fatalf("PublishToolResult failed: %v", err)
	}

	// Wait for event delivery
	time.Sleep(200 * time.Millisecond)

	receivedEvents := sub.getEvents()
	if len(receivedEvents) < 2 {
		t.Fatalf("Expected at least 2 events, got %d", len(receivedEvents))
	}

	// Verify order and types
	foundCall := false
	foundResult := false
	for _, event := range receivedEvents {
		if event.EventType == events.EventTypeToolCall {
			foundCall = true
		}
		if event.EventType == events.EventTypeToolResult {
			foundResult = true
		}
	}

	if !foundCall {
		t.Error("Expected to find EventTypeToolCall event")
	}
	if !foundResult {
		t.Error("Expected to find EventTypeToolResult event")
	}
}

func TestToolEventPublisher_ToolTimeoutLifecycle(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &testToolSubscriber{
		id:         "test-sub",
		eventTypes: []events.EventType{},
	}
	bus.Subscribe(sub)

	publisher := NewToolEventPublisher(bus)

	// Simulate a tool lifecycle: call -> timeout
	params := map[string]any{"url": "https://slow-server.example.com"}
	err := publisher.PublishToolCall("session", "agent", "http_request", params)
	if err != nil {
		t.Fatalf("PublishToolCall failed: %v", err)
	}

	err = publisher.PublishToolTimeout("session", "agent", "http_request", 30*time.Second)
	if err != nil {
		t.Fatalf("PublishToolTimeout failed: %v", err)
	}

	// Wait for event delivery
	time.Sleep(200 * time.Millisecond)

	receivedEvents := sub.getEvents()
	if len(receivedEvents) < 2 {
		t.Fatalf("Expected at least 2 events, got %d", len(receivedEvents))
	}

	// Verify timeout has failure outcome
	for _, event := range receivedEvents {
		if event.EventType == events.EventTypeToolTimeout {
			if event.Outcome != events.OutcomeFailure {
				t.Errorf("Timeout event should have OutcomeFailure, got %v", event.Outcome)
			}
		}
	}
}
