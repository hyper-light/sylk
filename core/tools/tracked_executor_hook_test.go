package tools

import (
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// Test Helpers
// =============================================================================

// testEventSubscriber captures events for testing.
type testEventSubscriber struct {
	id         string
	eventTypes []events.EventType
	events     []*events.ActivityEvent
	mu         sync.Mutex
}

func newTestEventSubscriber(id string, eventTypes ...events.EventType) *testEventSubscriber {
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

func (s *testEventSubscriber) Events() []*events.ActivityEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]*events.ActivityEvent, len(s.events))
	copy(result, s.events)
	return result
}

func (s *testEventSubscriber) LastEvent() *events.ActivityEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) == 0 {
		return nil
	}
	return s.events[len(s.events)-1]
}

func (s *testEventSubscriber) WaitForEvent(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		count := len(s.events)
		s.mu.Unlock()
		if count > 0 {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

func (s *testEventSubscriber) WaitForEventCount(count int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		currentCount := len(s.events)
		s.mu.Unlock()
		if currentCount >= count {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// createTestEventBus creates a test event bus with a wildcard subscriber.
func createTestEventBus() (*events.ActivityEventBus, *testEventSubscriber) {
	bus := events.NewActivityEventBus(100)
	sub := newTestEventSubscriber("test-sub")
	bus.Subscribe(sub)
	bus.Start()
	return bus, sub
}

// =============================================================================
// ToolEventPublisherHook Tests
// =============================================================================

func TestNewToolEventPublisherHook(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	defer bus.Close()

	hook := NewToolEventPublisherHook(bus)

	assert.NotNil(t, hook)
	assert.Equal(t, bus, hook.Bus())
}

func TestNewToolEventPublisherHook_NilBus(t *testing.T) {
	hook := NewToolEventPublisherHook(nil)

	assert.NotNil(t, hook)
	assert.Nil(t, hook.Bus())
}

func TestToolEventPublisherHook_OnToolStart(t *testing.T) {
	bus, sub := createTestEventBus()
	defer bus.Close()

	hook := NewToolEventPublisherHook(bus)

	params := map[string]any{
		"path":    "/etc/config.yaml",
		"timeout": 30,
	}

	hook.OnToolStart("session-123", "agent-456", "file_read", params)

	require.True(t, sub.WaitForEvent(time.Second))

	event := sub.LastEvent()
	require.NotNil(t, event)
	assert.Equal(t, events.EventTypeToolCall, event.EventType)
	assert.Equal(t, "session-123", event.SessionID)
	assert.Equal(t, "agent-456", event.AgentID)
	assert.Contains(t, event.Content, "Tool started: file_read")
	assert.Contains(t, event.Summary, "Executing tool: file_read")
	assert.Equal(t, "file_read", event.Data["tool_name"])
	assert.Equal(t, params, event.Data["params"])
}

func TestToolEventPublisherHook_OnToolStart_NilBus(t *testing.T) {
	hook := NewToolEventPublisherHook(nil)

	// Should not panic
	hook.OnToolStart("session-123", "agent-456", "file_read", nil)
}

func TestToolEventPublisherHook_OnToolStart_NilParams(t *testing.T) {
	bus, sub := createTestEventBus()
	defer bus.Close()

	hook := NewToolEventPublisherHook(bus)

	hook.OnToolStart("session-123", "agent-456", "file_read", nil)

	require.True(t, sub.WaitForEvent(time.Second))

	event := sub.LastEvent()
	require.NotNil(t, event)
	assert.Nil(t, event.Data["params"])
}

func TestToolEventPublisherHook_OnToolComplete_Success(t *testing.T) {
	bus, sub := createTestEventBus()
	defer bus.Close()

	hook := NewToolEventPublisherHook(bus)

	result := map[string]any{
		"content": "file contents here",
		"size":    1234,
	}

	hook.OnToolComplete("session-123", "agent-456", "file_read", result, events.OutcomeSuccess)

	require.True(t, sub.WaitForEvent(time.Second))

	event := sub.LastEvent()
	require.NotNil(t, event)
	assert.Equal(t, events.EventTypeToolResult, event.EventType)
	assert.Equal(t, "session-123", event.SessionID)
	assert.Equal(t, "agent-456", event.AgentID)
	assert.Contains(t, event.Content, "Tool completed: file_read")
	assert.Equal(t, events.OutcomeSuccess, event.Outcome)
	assert.Equal(t, "file_read", event.Data["tool_name"])
	assert.Equal(t, result, event.Data["result"])
}

func TestToolEventPublisherHook_OnToolComplete_Failure(t *testing.T) {
	bus, sub := createTestEventBus()
	defer bus.Close()

	hook := NewToolEventPublisherHook(bus)

	hook.OnToolComplete("session-123", "agent-456", "file_write", nil, events.OutcomeFailure)

	require.True(t, sub.WaitForEvent(time.Second))

	event := sub.LastEvent()
	require.NotNil(t, event)
	assert.Equal(t, events.EventTypeToolResult, event.EventType)
	assert.Contains(t, event.Content, "Tool failed: file_write")
	assert.Equal(t, events.OutcomeFailure, event.Outcome)
}

func TestToolEventPublisherHook_OnToolComplete_NilBus(t *testing.T) {
	hook := NewToolEventPublisherHook(nil)

	// Should not panic
	hook.OnToolComplete("session-123", "agent-456", "file_read", nil, events.OutcomeSuccess)
}

func TestToolEventPublisherHook_OnToolTimeout(t *testing.T) {
	bus, sub := createTestEventBus()
	defer bus.Close()

	hook := NewToolEventPublisherHook(bus)

	timeout := 30 * time.Second

	hook.OnToolTimeout("session-123", "agent-456", "slow_tool", timeout)

	require.True(t, sub.WaitForEvent(time.Second))

	event := sub.LastEvent()
	require.NotNil(t, event)
	assert.Equal(t, events.EventTypeToolTimeout, event.EventType)
	assert.Equal(t, "session-123", event.SessionID)
	assert.Equal(t, "agent-456", event.AgentID)
	assert.Contains(t, event.Content, "Tool timed out: slow_tool")
	assert.Contains(t, event.Content, "30s")
	assert.Equal(t, events.OutcomeFailure, event.Outcome)
	assert.Equal(t, "slow_tool", event.Data["tool_name"])
	assert.Equal(t, "30s", event.Data["timeout"])
	assert.Equal(t, int64(30000), event.Data["timeout_ms"])
}

func TestToolEventPublisherHook_OnToolTimeout_NilBus(t *testing.T) {
	hook := NewToolEventPublisherHook(nil)

	// Should not panic
	hook.OnToolTimeout("session-123", "agent-456", "slow_tool", 30*time.Second)
}

// =============================================================================
// CompositeToolEventHook Tests
// =============================================================================

func TestNewCompositeToolEventHook(t *testing.T) {
	hook1 := NewNoOpToolEventHook()
	hook2 := NewNoOpToolEventHook()

	composite := NewCompositeToolEventHook(hook1, hook2)

	assert.NotNil(t, composite)
	assert.Len(t, composite.Hooks(), 2)
}

func TestCompositeToolEventHook_AddHook(t *testing.T) {
	composite := NewCompositeToolEventHook()

	assert.Len(t, composite.Hooks(), 0)

	hook := NewNoOpToolEventHook()
	composite.AddHook(hook)

	assert.Len(t, composite.Hooks(), 1)
}

func TestCompositeToolEventHook_OnToolStart(t *testing.T) {
	bus1, sub1 := createTestEventBus()
	defer bus1.Close()

	bus2, sub2 := createTestEventBus()
	defer bus2.Close()

	hook1 := NewToolEventPublisherHook(bus1)
	hook2 := NewToolEventPublisherHook(bus2)

	composite := NewCompositeToolEventHook(hook1, hook2)

	params := map[string]any{"key": "value"}
	composite.OnToolStart("session-123", "agent-456", "test_tool", params)

	require.True(t, sub1.WaitForEvent(time.Second))
	require.True(t, sub2.WaitForEvent(time.Second))

	event1 := sub1.LastEvent()
	event2 := sub2.LastEvent()

	assert.NotNil(t, event1)
	assert.NotNil(t, event2)
	assert.Equal(t, events.EventTypeToolCall, event1.EventType)
	assert.Equal(t, events.EventTypeToolCall, event2.EventType)
}

func TestCompositeToolEventHook_OnToolComplete(t *testing.T) {
	bus1, sub1 := createTestEventBus()
	defer bus1.Close()

	bus2, sub2 := createTestEventBus()
	defer bus2.Close()

	hook1 := NewToolEventPublisherHook(bus1)
	hook2 := NewToolEventPublisherHook(bus2)

	composite := NewCompositeToolEventHook(hook1, hook2)

	composite.OnToolComplete("session-123", "agent-456", "test_tool", "result", events.OutcomeSuccess)

	require.True(t, sub1.WaitForEvent(time.Second))
	require.True(t, sub2.WaitForEvent(time.Second))

	event1 := sub1.LastEvent()
	event2 := sub2.LastEvent()

	assert.NotNil(t, event1)
	assert.NotNil(t, event2)
	assert.Equal(t, events.EventTypeToolResult, event1.EventType)
	assert.Equal(t, events.EventTypeToolResult, event2.EventType)
}

func TestCompositeToolEventHook_OnToolTimeout(t *testing.T) {
	bus1, sub1 := createTestEventBus()
	defer bus1.Close()

	bus2, sub2 := createTestEventBus()
	defer bus2.Close()

	hook1 := NewToolEventPublisherHook(bus1)
	hook2 := NewToolEventPublisherHook(bus2)

	composite := NewCompositeToolEventHook(hook1, hook2)

	composite.OnToolTimeout("session-123", "agent-456", "test_tool", 10*time.Second)

	require.True(t, sub1.WaitForEvent(time.Second))
	require.True(t, sub2.WaitForEvent(time.Second))

	event1 := sub1.LastEvent()
	event2 := sub2.LastEvent()

	assert.NotNil(t, event1)
	assert.NotNil(t, event2)
	assert.Equal(t, events.EventTypeToolTimeout, event1.EventType)
	assert.Equal(t, events.EventTypeToolTimeout, event2.EventType)
}

// =============================================================================
// NoOpToolEventHook Tests
// =============================================================================

func TestNewNoOpToolEventHook(t *testing.T) {
	hook := NewNoOpToolEventHook()
	assert.NotNil(t, hook)
}

func TestNoOpToolEventHook_OnToolStart(t *testing.T) {
	hook := NewNoOpToolEventHook()
	// Should not panic
	hook.OnToolStart("session", "agent", "tool", nil)
}

func TestNoOpToolEventHook_OnToolComplete(t *testing.T) {
	hook := NewNoOpToolEventHook()
	// Should not panic
	hook.OnToolComplete("session", "agent", "tool", nil, events.OutcomeSuccess)
}

func TestNoOpToolEventHook_OnToolTimeout(t *testing.T) {
	hook := NewNoOpToolEventHook()
	// Should not panic
	hook.OnToolTimeout("session", "agent", "tool", time.Second)
}

// =============================================================================
// Interface Compliance Tests
// =============================================================================

func TestToolEventPublisherHook_ImplementsInterface(t *testing.T) {
	var _ TrackedExecutorEventHook = (*ToolEventPublisherHook)(nil)
}

func TestCompositeToolEventHook_ImplementsInterface(t *testing.T) {
	var _ TrackedExecutorEventHook = (*CompositeToolEventHook)(nil)
}

func TestNoOpToolEventHook_ImplementsInterface(t *testing.T) {
	var _ TrackedExecutorEventHook = (*NoOpToolEventHook)(nil)
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestToolEventPublisherHook_FullToolLifecycle(t *testing.T) {
	bus, sub := createTestEventBus()
	defer bus.Close()

	hook := NewToolEventPublisherHook(bus)

	// Simulate tool execution lifecycle
	params := map[string]any{"file": "test.txt"}

	// 1. Tool starts
	hook.OnToolStart("session-123", "agent-456", "file_read", params)

	// Wait to avoid debouncing
	time.Sleep(150 * time.Millisecond)

	// 2. Tool completes successfully
	result := map[string]any{"content": "file content"}
	hook.OnToolComplete("session-123", "agent-456", "file_read", result, events.OutcomeSuccess)

	// Wait for both events
	require.True(t, sub.WaitForEventCount(2, 2*time.Second))

	capturedEvents := sub.Events()
	require.Len(t, capturedEvents, 2)

	// Verify event sequence
	assert.Equal(t, events.EventTypeToolCall, capturedEvents[0].EventType)
	assert.Equal(t, events.EventTypeToolResult, capturedEvents[1].EventType)
}

func TestToolEventPublisherHook_ToolWithTimeout(t *testing.T) {
	bus, sub := createTestEventBus()
	defer bus.Close()

	hook := NewToolEventPublisherHook(bus)

	// 1. Tool starts
	hook.OnToolStart("session-123", "agent-456", "slow_tool", nil)

	// Wait to avoid debouncing
	time.Sleep(150 * time.Millisecond)

	// 2. Tool times out
	hook.OnToolTimeout("session-123", "agent-456", "slow_tool", 30*time.Second)

	// Wait for both events
	require.True(t, sub.WaitForEventCount(2, 2*time.Second))

	capturedEvents := sub.Events()
	require.Len(t, capturedEvents, 2)

	// Verify event sequence
	assert.Equal(t, events.EventTypeToolCall, capturedEvents[0].EventType)
	assert.Equal(t, events.EventTypeToolTimeout, capturedEvents[1].EventType)
}
