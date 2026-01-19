package guide

import (
	"sync"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/events"
)

// =============================================================================
// Mock Subscriber for Testing
// =============================================================================

// testSubscriber implements events.EventSubscriber for testing
type testSubscriber struct {
	id         string
	eventTypes []events.EventType
	events     []*events.ActivityEvent
	mu         sync.Mutex
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

func (s *testSubscriber) getEvents() []*events.ActivityEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*events.ActivityEvent{}, s.events...)
}

// =============================================================================
// NewGuideEventPublisher Tests
// =============================================================================

func TestNewGuideEventPublisher(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	defer bus.Close()

	publisher := NewGuideEventPublisher(bus, "test-session")

	if publisher == nil {
		t.Fatal("Expected non-nil publisher")
	}

	if publisher.bus != bus {
		t.Error("Expected bus to be set")
	}

	if publisher.sessionID != "test-session" {
		t.Errorf("Expected sessionID 'test-session', got %q", publisher.sessionID)
	}
}

func TestGuideEventPublisher_SessionID(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	defer bus.Close()

	publisher := NewGuideEventPublisher(bus, "my-session")

	if publisher.SessionID() != "my-session" {
		t.Errorf("Expected SessionID() to return 'my-session', got %q", publisher.SessionID())
	}
}

func TestGuideEventPublisher_Bus(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	defer bus.Close()

	publisher := NewGuideEventPublisher(bus, "test-session")

	if publisher.Bus() != bus {
		t.Error("Expected Bus() to return the underlying bus")
	}
}

// =============================================================================
// PublishUserPrompt Tests
// =============================================================================

func TestGuideEventPublisher_PublishUserPrompt(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &testSubscriber{
		id:         "test-sub",
		eventTypes: []events.EventType{events.EventTypeUserPrompt},
	}
	bus.Subscribe(sub)

	publisher := NewGuideEventPublisher(bus, "default-session")

	err := publisher.PublishUserPrompt("session-123", "Hello, world!", "guide")
	if err != nil {
		t.Fatalf("PublishUserPrompt failed: %v", err)
	}

	// Wait for event delivery
	time.Sleep(150 * time.Millisecond)

	receivedEvents := sub.getEvents()
	if len(receivedEvents) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(receivedEvents))
	}

	event := receivedEvents[0]
	if event.EventType != events.EventTypeUserPrompt {
		t.Errorf("Expected EventTypeUserPrompt, got %v", event.EventType)
	}
	if event.SessionID != "session-123" {
		t.Errorf("Expected SessionID 'session-123', got %q", event.SessionID)
	}
	if event.AgentID != "guide" {
		t.Errorf("Expected AgentID 'guide', got %q", event.AgentID)
	}
	if event.Content != "Hello, world!" {
		t.Errorf("Expected Content 'Hello, world!', got %q", event.Content)
	}
	if event.Outcome != events.OutcomePending {
		t.Errorf("Expected OutcomePending, got %v", event.Outcome)
	}
	if event.Data == nil {
		t.Error("Expected Data to be non-nil")
	}
	if length, ok := event.Data["prompt_length"].(int); !ok || length != 13 {
		t.Errorf("Expected prompt_length 13, got %v", event.Data["prompt_length"])
	}
}

func TestGuideEventPublisher_PublishUserPrompt_NilBus(t *testing.T) {
	publisher := &GuideEventPublisher{
		bus:       nil,
		sessionID: "test",
	}

	err := publisher.PublishUserPrompt("session", "content", "agent")
	if err == nil {
		t.Error("Expected error when bus is nil")
	}
}

func TestGuideEventPublisher_PublishUserPrompt_EmptyContent(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &testSubscriber{
		id:         "test-sub",
		eventTypes: []events.EventType{events.EventTypeUserPrompt},
	}
	bus.Subscribe(sub)

	publisher := NewGuideEventPublisher(bus, "default-session")

	err := publisher.PublishUserPrompt("session-123", "", "guide")
	if err != nil {
		t.Fatalf("PublishUserPrompt should accept empty content: %v", err)
	}

	// Wait for event delivery
	time.Sleep(150 * time.Millisecond)

	receivedEvents := sub.getEvents()
	if len(receivedEvents) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(receivedEvents))
	}

	if length, ok := receivedEvents[0].Data["prompt_length"].(int); !ok || length != 0 {
		t.Errorf("Expected prompt_length 0, got %v", receivedEvents[0].Data["prompt_length"])
	}
}

// =============================================================================
// PublishRoutingDecision Tests
// =============================================================================

func TestGuideEventPublisher_PublishRoutingDecision(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &testSubscriber{
		id:         "test-sub",
		eventTypes: []events.EventType{events.EventTypeAgentDecision},
	}
	bus.Subscribe(sub)

	publisher := NewGuideEventPublisher(bus, "default-session")

	err := publisher.PublishRoutingDecision("session-456", "agent-a", "agent-b", "Best match for query")
	if err != nil {
		t.Fatalf("PublishRoutingDecision failed: %v", err)
	}

	// Wait for event delivery
	time.Sleep(150 * time.Millisecond)

	receivedEvents := sub.getEvents()
	if len(receivedEvents) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(receivedEvents))
	}

	event := receivedEvents[0]
	if event.EventType != events.EventTypeAgentDecision {
		t.Errorf("Expected EventTypeAgentDecision, got %v", event.EventType)
	}
	if event.SessionID != "session-456" {
		t.Errorf("Expected SessionID 'session-456', got %q", event.SessionID)
	}
	if event.AgentID != "guide" {
		t.Errorf("Expected AgentID 'guide', got %q", event.AgentID)
	}
	if event.Content != "Routing decision: agent-a -> agent-b" {
		t.Errorf("Unexpected Content: %q", event.Content)
	}
	if event.Outcome != events.OutcomeSuccess {
		t.Errorf("Expected OutcomeSuccess, got %v", event.Outcome)
	}
	if event.Data == nil {
		t.Fatal("Expected Data to be non-nil")
	}
	if event.Data["decision_type"] != "routing" {
		t.Errorf("Expected decision_type 'routing', got %v", event.Data["decision_type"])
	}
	if event.Data["from_agent"] != "agent-a" {
		t.Errorf("Expected from_agent 'agent-a', got %v", event.Data["from_agent"])
	}
	if event.Data["to_agent"] != "agent-b" {
		t.Errorf("Expected to_agent 'agent-b', got %v", event.Data["to_agent"])
	}
	if event.Data["reason"] != "Best match for query" {
		t.Errorf("Expected reason 'Best match for query', got %v", event.Data["reason"])
	}
}

func TestGuideEventPublisher_PublishRoutingDecision_NilBus(t *testing.T) {
	publisher := &GuideEventPublisher{
		bus:       nil,
		sessionID: "test",
	}

	err := publisher.PublishRoutingDecision("session", "from", "to", "reason")
	if err == nil {
		t.Error("Expected error when bus is nil")
	}
}

func TestGuideEventPublisher_PublishRoutingDecision_EmptyAgents(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &testSubscriber{
		id:         "test-sub",
		eventTypes: []events.EventType{events.EventTypeAgentDecision},
	}
	bus.Subscribe(sub)

	publisher := NewGuideEventPublisher(bus, "default-session")

	err := publisher.PublishRoutingDecision("session", "", "", "")
	if err != nil {
		t.Fatalf("PublishRoutingDecision should accept empty agents: %v", err)
	}

	// Wait for event delivery
	time.Sleep(150 * time.Millisecond)

	receivedEvents := sub.getEvents()
	if len(receivedEvents) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(receivedEvents))
	}

	if receivedEvents[0].Content != "Routing decision:  -> " {
		t.Errorf("Unexpected Content for empty agents: %q", receivedEvents[0].Content)
	}
}

// =============================================================================
// PublishClarificationRequest Tests
// =============================================================================

func TestGuideEventPublisher_PublishClarificationRequest(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &testSubscriber{
		id:         "test-sub",
		eventTypes: []events.EventType{events.EventTypeUserClarification},
	}
	bus.Subscribe(sub)

	publisher := NewGuideEventPublisher(bus, "default-session")

	err := publisher.PublishClarificationRequest("session-789", "Could you please clarify your request?")
	if err != nil {
		t.Fatalf("PublishClarificationRequest failed: %v", err)
	}

	// Wait for event delivery
	time.Sleep(150 * time.Millisecond)

	receivedEvents := sub.getEvents()
	if len(receivedEvents) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(receivedEvents))
	}

	event := receivedEvents[0]
	if event.EventType != events.EventTypeUserClarification {
		t.Errorf("Expected EventTypeUserClarification, got %v", event.EventType)
	}
	if event.SessionID != "session-789" {
		t.Errorf("Expected SessionID 'session-789', got %q", event.SessionID)
	}
	if event.AgentID != "guide" {
		t.Errorf("Expected AgentID 'guide', got %q", event.AgentID)
	}
	if event.Content != "Could you please clarify your request?" {
		t.Errorf("Unexpected Content: %q", event.Content)
	}
	if event.Outcome != events.OutcomePending {
		t.Errorf("Expected OutcomePending, got %v", event.Outcome)
	}
	if event.Data == nil {
		t.Fatal("Expected Data to be non-nil")
	}
	if length, ok := event.Data["question_length"].(int); !ok || length != 38 {
		t.Errorf("Expected question_length 38, got %v", event.Data["question_length"])
	}
}

func TestGuideEventPublisher_PublishClarificationRequest_NilBus(t *testing.T) {
	publisher := &GuideEventPublisher{
		bus:       nil,
		sessionID: "test",
	}

	err := publisher.PublishClarificationRequest("session", "question?")
	if err == nil {
		t.Error("Expected error when bus is nil")
	}
}

func TestGuideEventPublisher_PublishClarificationRequest_EmptyQuestion(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &testSubscriber{
		id:         "test-sub",
		eventTypes: []events.EventType{events.EventTypeUserClarification},
	}
	bus.Subscribe(sub)

	publisher := NewGuideEventPublisher(bus, "default-session")

	err := publisher.PublishClarificationRequest("session", "")
	if err != nil {
		t.Fatalf("PublishClarificationRequest should accept empty question: %v", err)
	}

	// Wait for event delivery
	time.Sleep(150 * time.Millisecond)

	receivedEvents := sub.getEvents()
	if len(receivedEvents) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(receivedEvents))
	}

	if length, ok := receivedEvents[0].Data["question_length"].(int); !ok || length != 0 {
		t.Errorf("Expected question_length 0, got %v", receivedEvents[0].Data["question_length"])
	}
}

// =============================================================================
// Event ID Uniqueness Tests
// =============================================================================

func TestGuideEventPublisher_EventIDsAreUnique(t *testing.T) {
	bus := events.NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &testSubscriber{
		id:         "test-sub",
		eventTypes: []events.EventType{},
	}
	bus.Subscribe(sub)

	publisher := NewGuideEventPublisher(bus, "test-session")

	// Publish multiple events with different sessions/agents to avoid debouncing
	// (debouncer keys on event_type:agent_id:session_id)
	_ = publisher.PublishUserPrompt("session-1", "prompt1", "guide")
	_ = publisher.PublishUserPrompt("session-2", "prompt2", "guide")
	_ = publisher.PublishRoutingDecision("session-3", "a", "b", "reason")
	_ = publisher.PublishClarificationRequest("session-4", "question?")

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

func TestGuideEventPublisher_ConcurrentPublishing(t *testing.T) {
	bus := events.NewActivityEventBus(1000)
	bus.Start()
	defer bus.Close()

	sub := &testSubscriber{
		id:         "test-sub",
		eventTypes: []events.EventType{},
	}
	bus.Subscribe(sub)

	publisher := NewGuideEventPublisher(bus, "test-session")

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
					_ = publisher.PublishUserPrompt("session", "prompt", "guide")
				case 1:
					_ = publisher.PublishRoutingDecision("session", "a", "b", "reason")
				case 2:
					_ = publisher.PublishClarificationRequest("session", "question?")
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
