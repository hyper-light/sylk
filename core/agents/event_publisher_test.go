package agents

import (
	"errors"
	"testing"

	"github.com/adalundhe/sylk/core/events"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// =============================================================================
// AgentEventPublisher Tests
// =============================================================================

func TestNewAgentEventPublisher(t *testing.T) {
	collector := events.NewTestActivityCollector()

	publisher := NewAgentEventPublisher(collector, "agent-123")

	assert.NotNil(t, publisher)
	assert.Equal(t, "agent-123", publisher.AgentID())
	assert.Equal(t, collector, publisher.Bus())
}

func TestNewAgentEventPublisher_NilBus(t *testing.T) {
	publisher := NewAgentEventPublisher(nil, "agent-123")

	assert.NotNil(t, publisher)
	assert.Equal(t, "agent-123", publisher.AgentID())
	assert.Nil(t, publisher.Bus())
}

func TestAgentEventPublisher_PublishAgentAction(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewAgentEventPublisher(collector, "agent-123")

	err := publisher.PublishAgentAction("session-456", "file_read", "Reading config.yaml")
	require.NoError(t, err)

	evts := collector.Events()
	require.Len(t, evts, 1)

	event := evts[0]
	assert.Equal(t, events.EventTypeAgentAction, event.EventType)
	assert.Equal(t, "session-456", event.SessionID)
	assert.Equal(t, "agent-123", event.AgentID)
	assert.Equal(t, "file_read", event.Summary)
	assert.Contains(t, event.Content, "Action: file_read")
	assert.Contains(t, event.Content, "Reading config.yaml")
	assert.Equal(t, "file_read", event.Data["action"])
	assert.Equal(t, "Reading config.yaml", event.Data["details"])
}

func TestAgentEventPublisher_PublishAgentAction_NoDetails(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewAgentEventPublisher(collector, "agent-123")

	err := publisher.PublishAgentAction("session-456", "ping", "")
	require.NoError(t, err)

	evts := collector.Events()
	require.Len(t, evts, 1)

	event := evts[0]
	assert.Equal(t, "Action: ping", event.Content)
}

func TestAgentEventPublisher_PublishAgentAction_NilBus(t *testing.T) {
	publisher := NewAgentEventPublisher(nil, "agent-123")

	err := publisher.PublishAgentAction("session-456", "action", "details")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "event bus is nil")
}

func TestAgentEventPublisher_PublishAgentDecision(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewAgentEventPublisher(collector, "agent-123")

	err := publisher.PublishAgentDecision("session-456", "use_cache", "File hasn't changed since last read")
	require.NoError(t, err)

	evts := collector.Events()
	require.Len(t, evts, 1)

	event := evts[0]
	assert.Equal(t, events.EventTypeAgentDecision, event.EventType)
	assert.Equal(t, "session-456", event.SessionID)
	assert.Equal(t, "agent-123", event.AgentID)
	assert.Equal(t, "use_cache", event.Summary)
	assert.Contains(t, event.Content, "Decision: use_cache")
	assert.Contains(t, event.Content, "Rationale:")
	assert.Equal(t, "use_cache", event.Data["decision"])
	assert.Equal(t, "File hasn't changed since last read", event.Data["rationale"])
}

func TestAgentEventPublisher_PublishAgentDecision_NoRationale(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewAgentEventPublisher(collector, "agent-123")

	err := publisher.PublishAgentDecision("session-456", "proceed", "")
	require.NoError(t, err)

	evts := collector.Events()
	require.Len(t, evts, 1)

	event := evts[0]
	assert.Equal(t, "Decision: proceed", event.Content)
	assert.NotContains(t, event.Content, "Rationale:")
}

func TestAgentEventPublisher_PublishAgentDecision_NilBus(t *testing.T) {
	publisher := NewAgentEventPublisher(nil, "agent-123")

	err := publisher.PublishAgentDecision("session-456", "decision", "rationale")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "event bus is nil")
}

func TestAgentEventPublisher_PublishAgentError(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewAgentEventPublisher(collector, "agent-123")

	testErr := errors.New("file not found")
	err := publisher.PublishAgentError("session-456", testErr, "trying to read config.yaml")
	require.NoError(t, err)

	evts := collector.Events()
	require.Len(t, evts, 1)

	event := evts[0]
	assert.Equal(t, events.EventTypeAgentError, event.EventType)
	assert.Equal(t, "session-456", event.SessionID)
	assert.Equal(t, "agent-123", event.AgentID)
	assert.Equal(t, "file not found", event.Summary)
	assert.Equal(t, events.OutcomeFailure, event.Outcome)
	assert.Contains(t, event.Content, "Error: file not found")
	assert.Contains(t, event.Content, "Context:")
	assert.Equal(t, "file not found", event.Data["error"])
	assert.Equal(t, "trying to read config.yaml", event.Data["context"])
}

func TestAgentEventPublisher_PublishAgentError_NilError(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewAgentEventPublisher(collector, "agent-123")

	err := publisher.PublishAgentError("session-456", nil, "some context")
	require.NoError(t, err)

	evts := collector.Events()
	require.Len(t, evts, 1)

	event := evts[0]
	assert.Equal(t, "Error: ", event.Content[:7])
	assert.Equal(t, "", event.Data["error"])
}

func TestAgentEventPublisher_PublishAgentError_NoContext(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewAgentEventPublisher(collector, "agent-123")

	testErr := errors.New("timeout")
	err := publisher.PublishAgentError("session-456", testErr, "")
	require.NoError(t, err)

	evts := collector.Events()
	require.Len(t, evts, 1)

	event := evts[0]
	assert.Equal(t, "Error: timeout", event.Content)
}

func TestAgentEventPublisher_PublishAgentError_NilBus(t *testing.T) {
	publisher := NewAgentEventPublisher(nil, "agent-123")

	err := publisher.PublishAgentError("session-456", errors.New("test"), "context")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "event bus is nil")
}

func TestAgentEventPublisher_PublishSuccess(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewAgentEventPublisher(collector, "agent-123")

	err := publisher.PublishSuccess("session-456", "Task completed successfully")
	require.NoError(t, err)

	evts := collector.Events()
	require.Len(t, evts, 1)

	event := evts[0]
	assert.Equal(t, events.EventTypeSuccess, event.EventType)
	assert.Equal(t, "session-456", event.SessionID)
	assert.Equal(t, "agent-123", event.AgentID)
	assert.Equal(t, "Task completed successfully", event.Summary)
	assert.Equal(t, "Task completed successfully", event.Content)
	assert.Equal(t, events.OutcomeSuccess, event.Outcome)
	assert.Equal(t, "Task completed successfully", event.Data["description"])
}

func TestAgentEventPublisher_PublishSuccess_NilBus(t *testing.T) {
	publisher := NewAgentEventPublisher(nil, "agent-123")

	err := publisher.PublishSuccess("session-456", "success")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "event bus is nil")
}

func TestAgentEventPublisher_PublishFailure(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewAgentEventPublisher(collector, "agent-123")

	testErr := errors.New("permission denied")
	err := publisher.PublishFailure("session-456", "Failed to write file", testErr)
	require.NoError(t, err)

	evts := collector.Events()
	require.Len(t, evts, 1)

	event := evts[0]
	assert.Equal(t, events.EventTypeFailure, event.EventType)
	assert.Equal(t, "session-456", event.SessionID)
	assert.Equal(t, "agent-123", event.AgentID)
	assert.Equal(t, "Failed to write file", event.Summary)
	assert.Equal(t, events.OutcomeFailure, event.Outcome)
	assert.Contains(t, event.Content, "Failed to write file")
	assert.Contains(t, event.Content, "permission denied")
	assert.Equal(t, "Failed to write file", event.Data["description"])
	assert.Equal(t, "permission denied", event.Data["error"])
}

func TestAgentEventPublisher_PublishFailure_NilError(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewAgentEventPublisher(collector, "agent-123")

	err := publisher.PublishFailure("session-456", "Task failed", nil)
	require.NoError(t, err)

	evts := collector.Events()
	require.Len(t, evts, 1)

	event := evts[0]
	assert.Equal(t, "Task failed", event.Content)
	assert.Equal(t, "", event.Data["error"])
}

func TestAgentEventPublisher_PublishFailure_NilBus(t *testing.T) {
	publisher := NewAgentEventPublisher(nil, "agent-123")

	err := publisher.PublishFailure("session-456", "failed", errors.New("test"))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "event bus is nil")
}

// =============================================================================
// SessionAgentEventPublisher Tests
// =============================================================================

func TestAgentEventPublisher_WithSessionID(t *testing.T) {
	collector := events.NewTestActivityCollector()

	publisher := NewAgentEventPublisher(collector, "agent-123")
	sessionPub := publisher.WithSessionID("session-456")

	assert.NotNil(t, sessionPub)
	assert.Equal(t, "session-456", sessionPub.SessionID())
	assert.Equal(t, "agent-123", sessionPub.AgentID())
}

func TestSessionAgentEventPublisher_PublishAction(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewAgentEventPublisher(collector, "agent-123")
	sessionPub := publisher.WithSessionID("session-456")

	err := sessionPub.PublishAction("test_action", "test details")
	require.NoError(t, err)

	evts := collector.Events()
	require.Len(t, evts, 1)

	event := evts[0]
	assert.Equal(t, events.EventTypeAgentAction, event.EventType)
	assert.Equal(t, "session-456", event.SessionID)
	assert.Equal(t, "agent-123", event.AgentID)
}

func TestSessionAgentEventPublisher_PublishDecision(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewAgentEventPublisher(collector, "agent-123")
	sessionPub := publisher.WithSessionID("session-456")

	err := sessionPub.PublishDecision("test_decision", "test rationale")
	require.NoError(t, err)

	evts := collector.Events()
	require.Len(t, evts, 1)

	event := evts[0]
	assert.Equal(t, events.EventTypeAgentDecision, event.EventType)
	assert.Equal(t, "session-456", event.SessionID)
}

func TestSessionAgentEventPublisher_PublishError(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewAgentEventPublisher(collector, "agent-123")
	sessionPub := publisher.WithSessionID("session-456")

	err := sessionPub.PublishError(errors.New("test error"), "test context")
	require.NoError(t, err)

	evts := collector.Events()
	require.Len(t, evts, 1)

	event := evts[0]
	assert.Equal(t, events.EventTypeAgentError, event.EventType)
	assert.Equal(t, "session-456", event.SessionID)
}

func TestSessionAgentEventPublisher_PublishSuccess(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewAgentEventPublisher(collector, "agent-123")
	sessionPub := publisher.WithSessionID("session-456")

	err := sessionPub.PublishSuccess("test success")
	require.NoError(t, err)

	evts := collector.Events()
	require.Len(t, evts, 1)

	event := evts[0]
	assert.Equal(t, events.EventTypeSuccess, event.EventType)
	assert.Equal(t, "session-456", event.SessionID)
}

func TestSessionAgentEventPublisher_PublishFailure(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewAgentEventPublisher(collector, "agent-123")
	sessionPub := publisher.WithSessionID("session-456")

	err := sessionPub.PublishFailure("test failure", errors.New("test error"))
	require.NoError(t, err)

	evts := collector.Events()
	require.Len(t, evts, 1)

	event := evts[0]
	assert.Equal(t, events.EventTypeFailure, event.EventType)
	assert.Equal(t, "session-456", event.SessionID)
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestAgentEventPublisher_MultipleEvents(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewAgentEventPublisher(collector, "agent-123")
	sessionPub := publisher.WithSessionID("session-456")

	// Publish multiple events
	require.NoError(t, sessionPub.PublishAction("start", "starting task"))
	require.NoError(t, sessionPub.PublishDecision("approach_a", "better performance"))
	require.NoError(t, sessionPub.PublishSuccess("completed"))

	evts := collector.Events()
	require.Len(t, evts, 3)

	// Verify event types in order
	assert.Equal(t, "agent_action", evts[0].EventType.String())
	assert.Equal(t, "agent_decision", evts[1].EventType.String())
	assert.Equal(t, "success", evts[2].EventType.String())
}

func TestAgentEventPublisher_ChainedCalls(t *testing.T) {
	collector := events.NewTestActivityCollector()
	publisher := NewAgentEventPublisher(collector, "agent-123")

	// Test chained call pattern
	err := publisher.WithSessionID("session-456").PublishAction("chained", "details")
	require.NoError(t, err)

	evts := collector.Events()
	require.Len(t, evts, 1)

	event := evts[0]
	assert.Equal(t, "session-456", event.SessionID)
	assert.Equal(t, "agent-123", event.AgentID)
}
