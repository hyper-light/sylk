package agents

import (
	"errors"
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

// testSubscriber captures events for testing.
type testSubscriber struct {
	id         string
	eventTypes []events.EventType
	events     []*events.ActivityEvent
	mu         sync.Mutex
}

func newTestSubscriber(id string, eventTypes ...events.EventType) *testSubscriber {
	return &testSubscriber{
		id:         id,
		eventTypes: eventTypes,
		events:     make([]*events.ActivityEvent, 0),
	}
}

func (s *testSubscriber) ID() string {
	return s.id
}

func (s *testSubscriber) EventTypes() []events.EventType {
	return s.eventTypes
}

func (s *testSubscriber) OnEvent(event *events.ActivityEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return nil
}

func (s *testSubscriber) Events() []*events.ActivityEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]*events.ActivityEvent, len(s.events))
	copy(result, s.events)
	return result
}

func (s *testSubscriber) LastEvent() *events.ActivityEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.events) == 0 {
		return nil
	}
	return s.events[len(s.events)-1]
}

func (s *testSubscriber) WaitForEvent(timeout time.Duration) bool {
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

func (s *testSubscriber) WaitForEventCount(count int, timeout time.Duration) bool {
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

// createTestBus creates a test event bus with a wildcard subscriber.
func createTestBus() (*events.ActivityEventBus, *testSubscriber) {
	bus := events.NewActivityEventBus(100)
	sub := newTestSubscriber("test-sub")
	bus.Subscribe(sub)
	bus.Start()
	return bus, sub
}

// =============================================================================
// AgentEventPublisher Tests
// =============================================================================

func TestNewAgentEventPublisher(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	defer bus.Close()

	publisher := NewAgentEventPublisher(bus, "agent-123")

	assert.NotNil(t, publisher)
	assert.Equal(t, "agent-123", publisher.AgentID())
	assert.Equal(t, bus, publisher.Bus())
}

func TestNewAgentEventPublisher_NilBus(t *testing.T) {
	publisher := NewAgentEventPublisher(nil, "agent-123")

	assert.NotNil(t, publisher)
	assert.Equal(t, "agent-123", publisher.AgentID())
	assert.Nil(t, publisher.Bus())
}

func TestAgentEventPublisher_PublishAgentAction(t *testing.T) {
	bus, sub := createTestBus()
	defer bus.Close()

	publisher := NewAgentEventPublisher(bus, "agent-123")

	err := publisher.PublishAgentAction("session-456", "file_read", "Reading config.yaml")
	require.NoError(t, err)

	// Wait for event to be processed
	require.True(t, sub.WaitForEvent(time.Second))

	event := sub.LastEvent()
	require.NotNil(t, event)
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
	bus, sub := createTestBus()
	defer bus.Close()

	publisher := NewAgentEventPublisher(bus, "agent-123")

	err := publisher.PublishAgentAction("session-456", "ping", "")
	require.NoError(t, err)

	require.True(t, sub.WaitForEvent(time.Second))

	event := sub.LastEvent()
	require.NotNil(t, event)
	assert.Equal(t, "Action: ping", event.Content)
}

func TestAgentEventPublisher_PublishAgentAction_NilBus(t *testing.T) {
	publisher := NewAgentEventPublisher(nil, "agent-123")

	err := publisher.PublishAgentAction("session-456", "action", "details")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "event bus is nil")
}

func TestAgentEventPublisher_PublishAgentDecision(t *testing.T) {
	bus, sub := createTestBus()
	defer bus.Close()

	publisher := NewAgentEventPublisher(bus, "agent-123")

	err := publisher.PublishAgentDecision("session-456", "use_cache", "File hasn't changed since last read")
	require.NoError(t, err)

	require.True(t, sub.WaitForEvent(time.Second))

	event := sub.LastEvent()
	require.NotNil(t, event)
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
	bus, sub := createTestBus()
	defer bus.Close()

	publisher := NewAgentEventPublisher(bus, "agent-123")

	err := publisher.PublishAgentDecision("session-456", "proceed", "")
	require.NoError(t, err)

	require.True(t, sub.WaitForEvent(time.Second))

	event := sub.LastEvent()
	require.NotNil(t, event)
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
	bus, sub := createTestBus()
	defer bus.Close()

	publisher := NewAgentEventPublisher(bus, "agent-123")

	testErr := errors.New("file not found")
	err := publisher.PublishAgentError("session-456", testErr, "trying to read config.yaml")
	require.NoError(t, err)

	require.True(t, sub.WaitForEvent(time.Second))

	event := sub.LastEvent()
	require.NotNil(t, event)
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
	bus, sub := createTestBus()
	defer bus.Close()

	publisher := NewAgentEventPublisher(bus, "agent-123")

	err := publisher.PublishAgentError("session-456", nil, "some context")
	require.NoError(t, err)

	require.True(t, sub.WaitForEvent(time.Second))

	event := sub.LastEvent()
	require.NotNil(t, event)
	assert.Equal(t, "Error: ", event.Content[:7])
	assert.Equal(t, "", event.Data["error"])
}

func TestAgentEventPublisher_PublishAgentError_NoContext(t *testing.T) {
	bus, sub := createTestBus()
	defer bus.Close()

	publisher := NewAgentEventPublisher(bus, "agent-123")

	testErr := errors.New("timeout")
	err := publisher.PublishAgentError("session-456", testErr, "")
	require.NoError(t, err)

	require.True(t, sub.WaitForEvent(time.Second))

	event := sub.LastEvent()
	require.NotNil(t, event)
	assert.Equal(t, "Error: timeout", event.Content)
}

func TestAgentEventPublisher_PublishAgentError_NilBus(t *testing.T) {
	publisher := NewAgentEventPublisher(nil, "agent-123")

	err := publisher.PublishAgentError("session-456", errors.New("test"), "context")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "event bus is nil")
}

func TestAgentEventPublisher_PublishSuccess(t *testing.T) {
	bus, sub := createTestBus()
	defer bus.Close()

	publisher := NewAgentEventPublisher(bus, "agent-123")

	err := publisher.PublishSuccess("session-456", "Task completed successfully")
	require.NoError(t, err)

	require.True(t, sub.WaitForEvent(time.Second))

	event := sub.LastEvent()
	require.NotNil(t, event)
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
	bus, sub := createTestBus()
	defer bus.Close()

	publisher := NewAgentEventPublisher(bus, "agent-123")

	testErr := errors.New("permission denied")
	err := publisher.PublishFailure("session-456", "Failed to write file", testErr)
	require.NoError(t, err)

	require.True(t, sub.WaitForEvent(time.Second))

	event := sub.LastEvent()
	require.NotNil(t, event)
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
	bus, sub := createTestBus()
	defer bus.Close()

	publisher := NewAgentEventPublisher(bus, "agent-123")

	err := publisher.PublishFailure("session-456", "Task failed", nil)
	require.NoError(t, err)

	require.True(t, sub.WaitForEvent(time.Second))

	event := sub.LastEvent()
	require.NotNil(t, event)
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
	bus := events.NewActivityEventBus(100)
	defer bus.Close()

	publisher := NewAgentEventPublisher(bus, "agent-123")
	sessionPub := publisher.WithSessionID("session-456")

	assert.NotNil(t, sessionPub)
	assert.Equal(t, "session-456", sessionPub.SessionID())
	assert.Equal(t, "agent-123", sessionPub.AgentID())
}

func TestSessionAgentEventPublisher_PublishAction(t *testing.T) {
	bus, sub := createTestBus()
	defer bus.Close()

	publisher := NewAgentEventPublisher(bus, "agent-123")
	sessionPub := publisher.WithSessionID("session-456")

	err := sessionPub.PublishAction("test_action", "test details")
	require.NoError(t, err)

	require.True(t, sub.WaitForEvent(time.Second))

	event := sub.LastEvent()
	require.NotNil(t, event)
	assert.Equal(t, events.EventTypeAgentAction, event.EventType)
	assert.Equal(t, "session-456", event.SessionID)
	assert.Equal(t, "agent-123", event.AgentID)
}

func TestSessionAgentEventPublisher_PublishDecision(t *testing.T) {
	bus, sub := createTestBus()
	defer bus.Close()

	publisher := NewAgentEventPublisher(bus, "agent-123")
	sessionPub := publisher.WithSessionID("session-456")

	err := sessionPub.PublishDecision("test_decision", "test rationale")
	require.NoError(t, err)

	require.True(t, sub.WaitForEvent(time.Second))

	event := sub.LastEvent()
	require.NotNil(t, event)
	assert.Equal(t, events.EventTypeAgentDecision, event.EventType)
	assert.Equal(t, "session-456", event.SessionID)
}

func TestSessionAgentEventPublisher_PublishError(t *testing.T) {
	bus, sub := createTestBus()
	defer bus.Close()

	publisher := NewAgentEventPublisher(bus, "agent-123")
	sessionPub := publisher.WithSessionID("session-456")

	err := sessionPub.PublishError(errors.New("test error"), "test context")
	require.NoError(t, err)

	require.True(t, sub.WaitForEvent(time.Second))

	event := sub.LastEvent()
	require.NotNil(t, event)
	assert.Equal(t, events.EventTypeAgentError, event.EventType)
	assert.Equal(t, "session-456", event.SessionID)
}

func TestSessionAgentEventPublisher_PublishSuccess(t *testing.T) {
	bus, sub := createTestBus()
	defer bus.Close()

	publisher := NewAgentEventPublisher(bus, "agent-123")
	sessionPub := publisher.WithSessionID("session-456")

	err := sessionPub.PublishSuccess("test success")
	require.NoError(t, err)

	require.True(t, sub.WaitForEvent(time.Second))

	event := sub.LastEvent()
	require.NotNil(t, event)
	assert.Equal(t, events.EventTypeSuccess, event.EventType)
	assert.Equal(t, "session-456", event.SessionID)
}

func TestSessionAgentEventPublisher_PublishFailure(t *testing.T) {
	bus, sub := createTestBus()
	defer bus.Close()

	publisher := NewAgentEventPublisher(bus, "agent-123")
	sessionPub := publisher.WithSessionID("session-456")

	err := sessionPub.PublishFailure("test failure", errors.New("test error"))
	require.NoError(t, err)

	require.True(t, sub.WaitForEvent(time.Second))

	event := sub.LastEvent()
	require.NotNil(t, event)
	assert.Equal(t, events.EventTypeFailure, event.EventType)
	assert.Equal(t, "session-456", event.SessionID)
}

// =============================================================================
// Integration Tests
// =============================================================================

func TestAgentEventPublisher_MultipleEvents(t *testing.T) {
	bus, sub := createTestBus()
	defer bus.Close()

	publisher := NewAgentEventPublisher(bus, "agent-123")
	sessionPub := publisher.WithSessionID("session-456")

	// Publish multiple events
	require.NoError(t, sessionPub.PublishAction("start", "starting task"))

	// Add small delay to avoid debouncing
	time.Sleep(150 * time.Millisecond)

	require.NoError(t, sessionPub.PublishDecision("approach_a", "better performance"))

	time.Sleep(150 * time.Millisecond)

	require.NoError(t, sessionPub.PublishSuccess("completed"))

	// Wait for all events
	require.True(t, sub.WaitForEventCount(3, 2*time.Second))

	events := sub.Events()
	require.Len(t, events, 3)

	// Verify event types in order
	assert.Equal(t, "agent_action", events[0].EventType.String())
	assert.Equal(t, "agent_decision", events[1].EventType.String())
	assert.Equal(t, "success", events[2].EventType.String())
}

func TestAgentEventPublisher_ChainedCalls(t *testing.T) {
	bus, sub := createTestBus()
	defer bus.Close()

	publisher := NewAgentEventPublisher(bus, "agent-123")

	// Test chained call pattern
	err := publisher.WithSessionID("session-456").PublishAction("chained", "details")
	require.NoError(t, err)

	require.True(t, sub.WaitForEvent(time.Second))

	event := sub.LastEvent()
	require.NotNil(t, event)
	assert.Equal(t, "session-456", event.SessionID)
	assert.Equal(t, "agent-123", event.AgentID)
}
