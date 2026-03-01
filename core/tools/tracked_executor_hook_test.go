package tools

import (
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// ToolEventPublisherHook Tests
// =============================================================================

func TestNewToolEventPublisherHook(t *testing.T) {
	collector := events.NewTestActivityCollector()

	hook := NewToolEventPublisherHook(collector)

	assert.NotNil(t, hook)
	assert.Equal(t, collector, hook.Bus())
}

func TestNewToolEventPublisherHook_NilBus(t *testing.T) {
	hook := NewToolEventPublisherHook(nil)

	assert.NotNil(t, hook)
	assert.Nil(t, hook.Bus())
}

func TestToolEventPublisherHook_OnToolStart(t *testing.T) {
	collector := events.NewTestActivityCollector()

	hook := NewToolEventPublisherHook(collector)

	params := map[string]any{
		"path":    "/etc/config.yaml",
		"timeout": 30,
	}

	hook.OnToolStart("session-123", "agent-456", "file_read", params)

	receivedEvents := collector.Events()
	require.Len(t, receivedEvents, 1)

	event := receivedEvents[0]
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
	collector := events.NewTestActivityCollector()

	hook := NewToolEventPublisherHook(collector)

	hook.OnToolStart("session-123", "agent-456", "file_read", nil)

	receivedEvents := collector.Events()
	require.Len(t, receivedEvents, 1)

	event := receivedEvents[0]
	assert.Nil(t, event.Data["params"])
}

func TestToolEventPublisherHook_OnToolComplete_Success(t *testing.T) {
	collector := events.NewTestActivityCollector()

	hook := NewToolEventPublisherHook(collector)

	result := map[string]any{
		"content": "file contents here",
		"size":    1234,
	}

	hook.OnToolComplete("session-123", "agent-456", "file_read", result, events.OutcomeSuccess)

	receivedEvents := collector.Events()
	require.Len(t, receivedEvents, 1)

	event := receivedEvents[0]
	assert.Equal(t, events.EventTypeToolResult, event.EventType)
	assert.Equal(t, "session-123", event.SessionID)
	assert.Equal(t, "agent-456", event.AgentID)
	assert.Contains(t, event.Content, "Tool completed: file_read")
	assert.Equal(t, events.OutcomeSuccess, event.Outcome)
	assert.Equal(t, "file_read", event.Data["tool_name"])
	assert.Equal(t, result, event.Data["result"])
}

func TestToolEventPublisherHook_OnToolComplete_Failure(t *testing.T) {
	collector := events.NewTestActivityCollector()

	hook := NewToolEventPublisherHook(collector)

	hook.OnToolComplete("session-123", "agent-456", "file_write", nil, events.OutcomeFailure)

	receivedEvents := collector.Events()
	require.Len(t, receivedEvents, 1)

	event := receivedEvents[0]
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
	collector := events.NewTestActivityCollector()

	hook := NewToolEventPublisherHook(collector)

	timeout := 30 * time.Second

	hook.OnToolTimeout("session-123", "agent-456", "slow_tool", timeout)

	receivedEvents := collector.Events()
	require.Len(t, receivedEvents, 1)

	event := receivedEvents[0]
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
	collector1 := events.NewTestActivityCollector()
	collector2 := events.NewTestActivityCollector()

	hook1 := NewToolEventPublisherHook(collector1)
	hook2 := NewToolEventPublisherHook(collector2)

	composite := NewCompositeToolEventHook(hook1, hook2)

	params := map[string]any{"key": "value"}
	composite.OnToolStart("session-123", "agent-456", "test_tool", params)

	events1 := collector1.Events()
	events2 := collector2.Events()

	require.Len(t, events1, 1)
	require.Len(t, events2, 1)

	assert.Equal(t, events.EventTypeToolCall, events1[0].EventType)
	assert.Equal(t, events.EventTypeToolCall, events2[0].EventType)
}

func TestCompositeToolEventHook_OnToolComplete(t *testing.T) {
	collector1 := events.NewTestActivityCollector()
	collector2 := events.NewTestActivityCollector()

	hook1 := NewToolEventPublisherHook(collector1)
	hook2 := NewToolEventPublisherHook(collector2)

	composite := NewCompositeToolEventHook(hook1, hook2)

	composite.OnToolComplete("session-123", "agent-456", "test_tool", "result", events.OutcomeSuccess)

	events1 := collector1.Events()
	events2 := collector2.Events()

	require.Len(t, events1, 1)
	require.Len(t, events2, 1)

	assert.Equal(t, events.EventTypeToolResult, events1[0].EventType)
	assert.Equal(t, events.EventTypeToolResult, events2[0].EventType)
}

func TestCompositeToolEventHook_OnToolTimeout(t *testing.T) {
	collector1 := events.NewTestActivityCollector()
	collector2 := events.NewTestActivityCollector()

	hook1 := NewToolEventPublisherHook(collector1)
	hook2 := NewToolEventPublisherHook(collector2)

	composite := NewCompositeToolEventHook(hook1, hook2)

	composite.OnToolTimeout("session-123", "agent-456", "test_tool", 10*time.Second)

	events1 := collector1.Events()
	events2 := collector2.Events()

	require.Len(t, events1, 1)
	require.Len(t, events2, 1)

	assert.Equal(t, events.EventTypeToolTimeout, events1[0].EventType)
	assert.Equal(t, events.EventTypeToolTimeout, events2[0].EventType)
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
	collector := events.NewTestActivityCollector()

	hook := NewToolEventPublisherHook(collector)

	// Simulate tool execution lifecycle
	params := map[string]any{"file": "test.txt"}

	// 1. Tool starts
	hook.OnToolStart("session-123", "agent-456", "file_read", params)

	// 2. Tool completes successfully
	result := map[string]any{"content": "file content"}
	hook.OnToolComplete("session-123", "agent-456", "file_read", result, events.OutcomeSuccess)

	capturedEvents := collector.Events()
	require.Len(t, capturedEvents, 2)

	// Verify event sequence
	assert.Equal(t, events.EventTypeToolCall, capturedEvents[0].EventType)
	assert.Equal(t, events.EventTypeToolResult, capturedEvents[1].EventType)
}

func TestToolEventPublisherHook_ToolWithTimeout(t *testing.T) {
	collector := events.NewTestActivityCollector()

	hook := NewToolEventPublisherHook(collector)

	// 1. Tool starts
	hook.OnToolStart("session-123", "agent-456", "slow_tool", nil)

	// 2. Tool times out
	hook.OnToolTimeout("session-123", "agent-456", "slow_tool", 30*time.Second)

	capturedEvents := collector.Events()
	require.Len(t, capturedEvents, 2)

	// Verify event sequence
	assert.Equal(t, events.EventTypeToolCall, capturedEvents[0].EventType)
	assert.Equal(t, events.EventTypeToolTimeout, capturedEvents[1].EventType)
}
