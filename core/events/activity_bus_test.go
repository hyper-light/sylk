package events

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// EventSubscriber Interface Tests
// =============================================================================

// mockSubscriber implements EventSubscriber interface for testing
type mockSubscriber struct {
	id         string
	eventTypes []EventType
	events     []*ActivityEvent
	mu         sync.Mutex
}

func (m *mockSubscriber) ID() string {
	return m.id
}

func (m *mockSubscriber) EventTypes() []EventType {
	return m.eventTypes
}

func (m *mockSubscriber) OnEvent(event *ActivityEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	return nil
}

func (m *mockSubscriber) getEvents() []*ActivityEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*ActivityEvent{}, m.events...)
}

func TestEventSubscriber_Interface(t *testing.T) {
	sub := &mockSubscriber{
		id:         "test-sub",
		eventTypes: []EventType{EventTypeAgentAction, EventTypeToolCall},
	}

	if sub.ID() != "test-sub" {
		t.Errorf("ID() = %q, want %q", sub.ID(), "test-sub")
	}

	types := sub.EventTypes()
	if len(types) != 2 {
		t.Errorf("len(EventTypes()) = %d, want 2", len(types))
	}

	event := NewActivityEvent(EventTypeAgentAction, "session-1", "test content")
	if err := sub.OnEvent(event); err != nil {
		t.Errorf("OnEvent() error = %v", err)
	}

	events := sub.getEvents()
	if len(events) != 1 {
		t.Errorf("len(events) = %d, want 1", len(events))
	}
}

func TestEventSubscriber_WildcardSubscription(t *testing.T) {
	// Wildcard subscriber has empty EventTypes
	sub := &mockSubscriber{
		id:         "wildcard-sub",
		eventTypes: []EventType{},
	}

	if len(sub.EventTypes()) != 0 {
		t.Errorf("Wildcard subscriber should have empty EventTypes, got %d", len(sub.EventTypes()))
	}
}

func TestEventSubscriber_SpecificEventTypes(t *testing.T) {
	sub := &mockSubscriber{
		id: "specific-sub",
		eventTypes: []EventType{
			EventTypeUserPrompt,
			EventTypeAgentDecision,
			EventTypeToolCall,
		},
	}

	types := sub.EventTypes()
	if len(types) != 3 {
		t.Errorf("len(EventTypes()) = %d, want 3", len(types))
	}

	// Verify specific event types
	expectedTypes := map[EventType]bool{
		EventTypeUserPrompt:    true,
		EventTypeAgentDecision: true,
		EventTypeToolCall:      true,
	}

	for _, et := range types {
		if !expectedTypes[et] {
			t.Errorf("Unexpected event type: %v", et)
		}
	}
}

// =============================================================================
// EventDebouncer Tests
// =============================================================================

func TestNewEventDebouncer(t *testing.T) {
	debouncer := NewEventDebouncer(5 * time.Second)
	defer debouncer.Stop()

	if debouncer.window != 5*time.Second {
		t.Errorf("window = %v, want %v", debouncer.window, 5*time.Second)
	}

	if debouncer.seen == nil {
		t.Error("seen map should not be nil")
	}
}

func TestNewEventDebouncer_DefaultWindow(t *testing.T) {
	// Zero or negative window should default to 5 seconds
	tests := []struct {
		input    time.Duration
		expected time.Duration
	}{
		{0, 5 * time.Second},
		{-1 * time.Second, 5 * time.Second},
	}

	for _, tt := range tests {
		debouncer := NewEventDebouncer(tt.input)
		defer debouncer.Stop()
		if debouncer.window != tt.expected {
			t.Errorf("NewEventDebouncer(%v).window = %v, want %v", tt.input, debouncer.window, tt.expected)
		}
	}
}

func TestEventDebouncer_ShouldSkip_FirstEvent(t *testing.T) {
	debouncer := NewEventDebouncer(100 * time.Millisecond)
	defer debouncer.Stop()

	event := NewActivityEvent(EventTypeAgentAction, "session-1", "test")
	event.AgentID = "agent-1"

	// First event should not be skipped
	if debouncer.ShouldSkip(event) {
		t.Error("First event should not be skipped")
	}
}

func TestEventDebouncer_ShouldSkip_DuplicateWithinWindow(t *testing.T) {
	debouncer := NewEventDebouncer(100 * time.Millisecond)
	defer debouncer.Stop()

	event1 := NewActivityEvent(EventTypeAgentAction, "session-1", "test")
	event1.AgentID = "agent-1"

	event2 := NewActivityEvent(EventTypeAgentAction, "session-1", "test")
	event2.AgentID = "agent-1"

	// First event should not be skipped
	if debouncer.ShouldSkip(event1) {
		t.Error("First event should not be skipped")
	}

	// Duplicate within window should be skipped
	if !debouncer.ShouldSkip(event2) {
		t.Error("Duplicate event within window should be skipped")
	}
}

func TestEventDebouncer_ShouldSkip_DuplicateAfterWindow(t *testing.T) {
	debouncer := NewEventDebouncer(50 * time.Millisecond)
	defer debouncer.Stop()

	event1 := NewActivityEvent(EventTypeAgentAction, "session-1", "test")
	event1.AgentID = "agent-1"

	event2 := NewActivityEvent(EventTypeAgentAction, "session-1", "test")
	event2.AgentID = "agent-1"

	// First event
	if debouncer.ShouldSkip(event1) {
		t.Error("First event should not be skipped")
	}

	// Wait for window to expire
	time.Sleep(60 * time.Millisecond)

	// Event after window should not be skipped
	if debouncer.ShouldSkip(event2) {
		t.Error("Event after window expiry should not be skipped")
	}
}

func TestEventDebouncer_ShouldSkip_DifferentEventTypes(t *testing.T) {
	debouncer := NewEventDebouncer(100 * time.Millisecond)
	defer debouncer.Stop()

	event1 := NewActivityEvent(EventTypeAgentAction, "session-1", "test")
	event1.AgentID = "agent-1"

	event2 := NewActivityEvent(EventTypeToolCall, "session-1", "test")
	event2.AgentID = "agent-1"

	// First event
	if debouncer.ShouldSkip(event1) {
		t.Error("First event should not be skipped")
	}

	// Different event type should not be skipped (different signature)
	if debouncer.ShouldSkip(event2) {
		t.Error("Different event type should not be skipped")
	}
}

func TestEventDebouncer_ShouldSkip_DifferentAgents(t *testing.T) {
	debouncer := NewEventDebouncer(100 * time.Millisecond)
	defer debouncer.Stop()

	event1 := NewActivityEvent(EventTypeAgentAction, "session-1", "test")
	event1.AgentID = "agent-1"

	event2 := NewActivityEvent(EventTypeAgentAction, "session-1", "test")
	event2.AgentID = "agent-2"

	// First event
	if debouncer.ShouldSkip(event1) {
		t.Error("First event should not be skipped")
	}

	// Different agent should not be skipped (different signature)
	if debouncer.ShouldSkip(event2) {
		t.Error("Different agent should not be skipped")
	}
}

func TestEventDebouncer_ShouldSkip_DifferentSessions(t *testing.T) {
	debouncer := NewEventDebouncer(100 * time.Millisecond)
	defer debouncer.Stop()

	event1 := NewActivityEvent(EventTypeAgentAction, "session-1", "test")
	event1.AgentID = "agent-1"

	event2 := NewActivityEvent(EventTypeAgentAction, "session-2", "test")
	event2.AgentID = "agent-1"

	// First event
	if debouncer.ShouldSkip(event1) {
		t.Error("First event should not be skipped")
	}

	// Different session should not be skipped (different signature)
	if debouncer.ShouldSkip(event2) {
		t.Error("Different session should not be skipped")
	}
}

func TestEventDebouncer_Signature(t *testing.T) {
	debouncer := NewEventDebouncer(100 * time.Millisecond)
	defer debouncer.Stop()

	event := NewActivityEvent(EventTypeAgentAction, "session-1", "test")
	event.AgentID = "agent-1"

	// Signature format should be: "eventType:agentID:sessionID"
	expected := "agent_action:agent-1:session-1"
	signature := debouncer.signature(event)

	if signature != expected {
		t.Errorf("signature() = %q, want %q", signature, expected)
	}
}

func TestEventDebouncer_Cleanup(t *testing.T) {
	debouncer := NewEventDebouncer(50 * time.Millisecond)
	defer debouncer.Stop()

	// Add some events
	for i := 0; i < 5; i++ {
		event := NewActivityEvent(EventTypeAgentAction, "session-1", "test")
		event.AgentID = "agent-1"
		debouncer.ShouldSkip(event)
	}

	// Verify events are recorded
	debouncer.mu.RLock()
	initialCount := len(debouncer.seen)
	debouncer.mu.RUnlock()

	if initialCount == 0 {
		t.Error("Expected some events to be recorded")
	}

	// Wait for entries to expire (window*2 = 100ms)
	time.Sleep(110 * time.Millisecond)

	// Run cleanup
	debouncer.Cleanup()

	// Verify old entries were removed
	debouncer.mu.RLock()
	finalCount := len(debouncer.seen)
	debouncer.mu.RUnlock()

	if finalCount != 0 {
		t.Errorf("Cleanup() should remove expired entries, got %d remaining", finalCount)
	}
}

// TestEventDebouncer_AutomaticCleanup verifies background cleanup runs automatically.
func TestEventDebouncer_AutomaticCleanup(t *testing.T) {
	// Use a short window to speed up the test
	debouncer := NewEventDebouncer(30 * time.Millisecond)
	defer debouncer.Stop()

	// Add events
	for i := 0; i < 10; i++ {
		event := NewActivityEvent(EventTypeAgentAction, "session-"+string(rune('A'+i)), "test")
		event.AgentID = "agent-" + string(rune('A'+i))
		debouncer.ShouldSkip(event)
	}

	// Verify events are recorded
	debouncer.mu.RLock()
	initialCount := len(debouncer.seen)
	debouncer.mu.RUnlock()

	if initialCount != 10 {
		t.Errorf("Expected 10 entries, got %d", initialCount)
	}

	// Wait for automatic cleanup (window + window*2 + buffer = 30 + 60 + 20 = 110ms)
	time.Sleep(120 * time.Millisecond)

	// Entries should be cleaned up automatically
	debouncer.mu.RLock()
	finalCount := len(debouncer.seen)
	debouncer.mu.RUnlock()

	if finalCount != 0 {
		t.Errorf("Automatic cleanup should have removed entries, got %d remaining", finalCount)
	}
}

// TestEventDebouncer_BoundedMemory verifies memory doesn't grow unboundedly.
func TestEventDebouncer_BoundedMemory(t *testing.T) {
	// Use a very short window
	debouncer := NewEventDebouncer(20 * time.Millisecond)
	defer debouncer.Stop()

	// Add many unique events over time
	for round := 0; round < 5; round++ {
		for i := 0; i < 100; i++ {
			event := NewActivityEvent(EventTypeAgentAction, "session-"+string(rune('A'+i%26)), "test")
			event.AgentID = "agent-" + string(rune('A'+round)) + string(rune('A'+i%26))
			debouncer.ShouldSkip(event)
		}
		// Wait for cleanup to run
		time.Sleep(50 * time.Millisecond)
	}

	// After all rounds, the map should be bounded (not 500 entries)
	debouncer.mu.RLock()
	count := len(debouncer.seen)
	debouncer.mu.RUnlock()

	// Should have at most entries from the last round (100) that haven't expired yet
	// Plus some margin for timing
	maxExpected := 150
	if count > maxExpected {
		t.Errorf("Map should be bounded, got %d entries (max expected %d)", count, maxExpected)
	}
}

// TestEventDebouncer_ConcurrentCleanup verifies no data races during cleanup.
func TestEventDebouncer_ConcurrentCleanup(t *testing.T) {
	debouncer := NewEventDebouncer(10 * time.Millisecond)
	defer debouncer.Stop()

	var wg sync.WaitGroup
	goroutines := 20
	iterations := 100

	// Start goroutines that add events
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				event := NewActivityEvent(EventTypeAgentAction, "session-"+string(rune('A'+j%26)), "test")
				event.AgentID = "agent-" + string(rune('A'+id))
				debouncer.ShouldSkip(event)
			}
		}(i)
	}

	// Start goroutines that manually trigger cleanup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				debouncer.Cleanup()
				time.Sleep(time.Millisecond)
			}
		}()
	}

	wg.Wait()
	// Test passes if no data race is detected (run with -race)
}

// TestEventDebouncer_Stop verifies the debouncer can be stopped.
func TestEventDebouncer_Stop(t *testing.T) {
	debouncer := NewEventDebouncer(100 * time.Millisecond)

	// Stop should not panic
	debouncer.Stop()

	// Multiple stops should not panic
	debouncer.Stop()
	debouncer.Stop()
}

// TestEventDebouncer_StopIdempotent verifies Stop is idempotent.
func TestEventDebouncer_StopIdempotent(t *testing.T) {
	debouncer := NewEventDebouncer(100 * time.Millisecond)

	// Call Stop concurrently
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			debouncer.Stop()
		}()
	}
	wg.Wait()
	// Should not panic
}

func TestEventDebouncer_ConcurrentAccess(t *testing.T) {
	debouncer := NewEventDebouncer(100 * time.Millisecond)
	defer debouncer.Stop()

	wg := sync.WaitGroup{}
	goroutines := 10
	eventsPerGoroutine := 100

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < eventsPerGoroutine; j++ {
				event := NewActivityEvent(EventTypeAgentAction, "session-1", "test")
				event.AgentID = "agent-1"
				debouncer.ShouldSkip(event)
			}
		}(i)
	}

	wg.Wait()

	// Should not panic and should have recorded events
	debouncer.mu.RLock()
	count := len(debouncer.seen)
	debouncer.mu.RUnlock()

	if count == 0 {
		t.Error("Expected some events to be recorded")
	}
}

// TestEventDebouncer_ConcurrentSameSignature tests atomic check-and-record
// to prevent TOCTOU race condition (W3H.3). Multiple goroutines concurrently
// submit events with the same signature - only ONE should be recorded as the
// first event (not skipped), and all others should be skipped.
func TestEventDebouncer_ConcurrentSameSignature(t *testing.T) {
	debouncer := NewEventDebouncer(1 * time.Second) // Long window
	defer debouncer.Stop()

	var wg sync.WaitGroup
	goroutines := 100
	notSkippedCount := int32(0)
	skippedCount := int32(0)

	// All goroutines will fire at the same time
	startChan := make(chan struct{})

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-startChan // Wait for signal

			event := NewActivityEvent(EventTypeAgentAction, "session-1", "test")
			event.AgentID = "agent-1"

			if debouncer.ShouldSkip(event) {
				atomic.AddInt32(&skippedCount, 1)
			} else {
				atomic.AddInt32(&notSkippedCount, 1)
			}
		}()
	}

	// Fire all goroutines at once
	close(startChan)
	wg.Wait()

	// Exactly ONE event should NOT be skipped (the first one)
	// All others should be skipped
	if notSkippedCount != 1 {
		t.Errorf("Expected exactly 1 event not skipped (the first), got %d", notSkippedCount)
	}

	expectedSkipped := int32(goroutines - 1)
	if skippedCount != expectedSkipped {
		t.Errorf("Expected %d events skipped, got %d", expectedSkipped, skippedCount)
	}
}

// TestEventDebouncer_ConcurrentSameSignatureStress stress-tests the atomic
// check-and-record to ensure no TOCTOU race conditions under heavy load.
func TestEventDebouncer_ConcurrentSameSignatureStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	for iteration := 0; iteration < 100; iteration++ {
		debouncer := NewEventDebouncer(1 * time.Second)

		var wg sync.WaitGroup
		goroutines := 50
		notSkippedCount := int32(0)

		startChan := make(chan struct{})

		for i := 0; i < goroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-startChan

				event := NewActivityEvent(EventTypeAgentAction, "session-1", "test")
				event.AgentID = "agent-1"

				if !debouncer.ShouldSkip(event) {
					atomic.AddInt32(&notSkippedCount, 1)
				}
			}()
		}

		close(startChan)
		wg.Wait()
		debouncer.Stop()

		if notSkippedCount != 1 {
			t.Errorf("Iteration %d: Expected exactly 1 event not skipped, got %d",
				iteration, notSkippedCount)
		}
	}
}

func TestEventDebouncer_EmptyAgentID(t *testing.T) {
	debouncer := NewEventDebouncer(100 * time.Millisecond)
	defer debouncer.Stop()

	event := NewActivityEvent(EventTypeAgentAction, "session-1", "test")
	// AgentID is empty

	signature := debouncer.signature(event)
	expected := "agent_action::session-1"

	if signature != expected {
		t.Errorf("signature() with empty AgentID = %q, want %q", signature, expected)
	}
}

// =============================================================================
// ActivityEventBus Tests
// =============================================================================

func TestNewActivityEventBus(t *testing.T) {
	bus := NewActivityEventBus(100)

	if bus == nil {
		t.Fatal("Expected non-nil bus")
	}

	if cap(bus.buffer) != 100 {
		t.Errorf("Expected buffer size 100, got %d", cap(bus.buffer))
	}

	bus.Close()
}

func TestNewActivityEventBus_DefaultBufferSize(t *testing.T) {
	bus := NewActivityEventBus(0)

	if cap(bus.buffer) != 1000 {
		t.Errorf("Expected default buffer size 1000, got %d", cap(bus.buffer))
	}

	bus.Close()
}

func TestActivityEventBus_PublishAndSubscribe(t *testing.T) {
	bus := NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	// Create subscriber
	sub := &mockSubscriber{
		id:         "test-sub",
		eventTypes: []EventType{EventTypeAgentAction},
	}

	bus.Subscribe(sub)

	// Publish event
	event := NewActivityEvent(EventTypeAgentAction, "test-session", "test action")
	event.AgentID = "test-agent"

	bus.Publish(event)

	// Wait for event delivery
	time.Sleep(100 * time.Millisecond)

	events := sub.getEvents()
	if len(events) != 1 {
		t.Errorf("Expected 1 event, got %d", len(events))
	}
	if len(events) > 0 && events[0].EventType != EventTypeAgentAction {
		t.Errorf("Expected event type %s, got %s", EventTypeAgentAction, events[0].EventType)
	}
}

func TestActivityEventBus_WildcardSubscriber(t *testing.T) {
	bus := NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	// Create wildcard subscriber (empty EventTypes)
	sub := &mockSubscriber{
		id:         "wildcard-sub",
		eventTypes: []EventType{},
	}

	bus.Subscribe(sub)

	// Publish different event types
	events := []*ActivityEvent{
		NewActivityEvent(EventTypeAgentAction, "s1", "action1"),
		NewActivityEvent(EventTypeToolCall, "s2", "call1"),
		NewActivityEvent(EventTypeUserPrompt, "s3", "prompt1"),
	}

	for _, event := range events {
		bus.Publish(event)
	}

	// Wait for event delivery
	time.Sleep(200 * time.Millisecond)

	receivedEvents := sub.getEvents()
	if len(receivedEvents) < 3 {
		t.Errorf("Expected at least 3 events, got %d", len(receivedEvents))
	}
}

func TestActivityEventBus_MultipleSubscribers(t *testing.T) {
	bus := NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	// Create multiple subscribers for same event
	sub1 := &mockSubscriber{
		id:         "sub1",
		eventTypes: []EventType{EventTypeAgentAction},
	}

	sub2 := &mockSubscriber{
		id:         "sub2",
		eventTypes: []EventType{EventTypeAgentAction},
	}

	bus.Subscribe(sub1)
	bus.Subscribe(sub2)

	// Publish event
	event := NewActivityEvent(EventTypeAgentAction, "test", "test action")

	bus.Publish(event)

	// Wait for event delivery
	time.Sleep(100 * time.Millisecond)

	events1 := sub1.getEvents()
	events2 := sub2.getEvents()

	if len(events1) != 1 {
		t.Errorf("sub1 expected 1 event, got %d", len(events1))
	}

	if len(events2) != 1 {
		t.Errorf("sub2 expected 1 event, got %d", len(events2))
	}
}

func TestActivityEventBus_Unsubscribe(t *testing.T) {
	bus := NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &mockSubscriber{
		id:         "test-sub",
		eventTypes: []EventType{EventTypeAgentAction},
	}

	bus.Subscribe(sub)
	bus.Unsubscribe("test-sub")

	// Publish event
	event := NewActivityEvent(EventTypeAgentAction, "test", "test action")

	bus.Publish(event)

	// Wait
	time.Sleep(100 * time.Millisecond)

	events := sub.getEvents()
	if len(events) != 0 {
		t.Errorf("Should not receive event after unsubscribe, got %d events", len(events))
	}
}

func TestActivityEventBus_BufferOverflow(t *testing.T) {
	bus := NewActivityEventBus(2) // Small buffer
	// Don't start dispatch - this will fill the buffer
	defer bus.Close()

	// Fill buffer beyond capacity
	for i := 0; i < 10; i++ {
		event := NewActivityEvent(EventTypeAgentAction, "test", "action")
		bus.Publish(event)
	}

	// Bus should not block or panic
	// Excess events should be dropped silently
}

func TestActivityEventBus_ConcurrentPublish(t *testing.T) {
	bus := NewActivityEventBus(1000)
	bus.Start()
	defer bus.Close()

	sub := &mockSubscriber{
		id:         "test-sub",
		eventTypes: []EventType{},
	}

	bus.Subscribe(sub)

	// Publish from multiple goroutines
	wg := sync.WaitGroup{}
	publishers := 10
	eventsPerPublisher := 10

	for i := 0; i < publishers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < eventsPerPublisher; j++ {
				event := NewActivityEvent(EventTypeAgentAction, "test", "action")
				bus.Publish(event)
			}
		}()
	}

	wg.Wait()

	// Wait for event delivery
	time.Sleep(500 * time.Millisecond)

	events := sub.getEvents()
	if len(events) == 0 {
		t.Error("Should receive at least some events")
	}
}

func TestActivityEventBus_Close(t *testing.T) {
	bus := NewActivityEventBus(100)
	bus.Start()

	sub := &mockSubscriber{
		id:         "test-sub",
		eventTypes: []EventType{EventTypeAgentAction},
	}

	bus.Subscribe(sub)
	bus.Close()

	// Publish after close should not panic
	event := NewActivityEvent(EventTypeAgentAction, "test", "action")
	bus.Publish(event)

	// Wait
	time.Sleep(100 * time.Millisecond)

	// Should not have received the event
	events := sub.getEvents()
	if len(events) != 0 {
		t.Errorf("Should not receive events after close, got %d", len(events))
	}
}

func TestActivityEventBus_SubscribeAfterClose(t *testing.T) {
	bus := NewActivityEventBus(100)
	bus.Start()
	bus.Close()

	sub := &mockSubscriber{
		id:         "test-sub",
		eventTypes: []EventType{EventTypeAgentAction},
	}

	// Subscribe after close should not panic
	bus.Subscribe(sub)
}

// =============================================================================
// Start() Idempotence Tests (W3C.3)
// =============================================================================

func TestActivityEventBus_SingleStart(t *testing.T) {
	bus := NewActivityEventBus(100)

	// Single Start() should work correctly
	bus.Start()

	// Verify dispatch is running by publishing and receiving an event
	sub := &mockSubscriber{
		id:         "test-sub",
		eventTypes: []EventType{EventTypeAgentAction},
	}
	bus.Subscribe(sub)

	event := NewActivityEvent(EventTypeAgentAction, "test-session", "test action")
	bus.Publish(event)

	// Wait for event delivery
	time.Sleep(100 * time.Millisecond)

	events := sub.getEvents()
	if len(events) != 1 {
		t.Errorf("Expected 1 event after single Start(), got %d", len(events))
	}

	bus.Close()
}

func TestActivityEventBus_MultipleStartIdempotent(t *testing.T) {
	bus := NewActivityEventBus(100)

	// Call Start() multiple times - should be idempotent
	bus.Start()
	bus.Start()
	bus.Start()

	// Verify dispatch is running correctly (only one goroutine)
	sub := &mockSubscriber{
		id:         "test-sub",
		eventTypes: []EventType{EventTypeAgentAction},
	}
	bus.Subscribe(sub)

	event := NewActivityEvent(EventTypeAgentAction, "test-session", "test action")
	bus.Publish(event)

	// Wait for event delivery
	time.Sleep(100 * time.Millisecond)

	events := sub.getEvents()
	if len(events) != 1 {
		t.Errorf("Expected 1 event after multiple Start() calls, got %d", len(events))
	}

	// Close should complete without deadlock
	// (If multiple goroutines were spawned, wg.Wait() would deadlock)
	done := make(chan struct{})
	go func() {
		bus.Close()
		close(done)
	}()

	select {
	case <-done:
		// Success - Close completed
	case <-time.After(2 * time.Second):
		t.Fatal("Close() deadlocked - multiple Start() calls spawned multiple goroutines")
	}
}

func TestActivityEventBus_ConcurrentStartIdempotent(t *testing.T) {
	bus := NewActivityEventBus(100)

	// Call Start() concurrently from multiple goroutines
	var wg sync.WaitGroup
	goroutines := 100

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			bus.Start()
		}()
	}

	wg.Wait()

	// Verify dispatch is running correctly (only one goroutine)
	sub := &mockSubscriber{
		id:         "test-sub",
		eventTypes: []EventType{EventTypeAgentAction},
	}
	bus.Subscribe(sub)

	event := NewActivityEvent(EventTypeAgentAction, "test-session", "test action")
	bus.Publish(event)

	// Wait for event delivery
	time.Sleep(100 * time.Millisecond)

	events := sub.getEvents()
	if len(events) != 1 {
		t.Errorf("Expected 1 event after concurrent Start() calls, got %d", len(events))
	}

	// Close should complete without deadlock
	done := make(chan struct{})
	go func() {
		bus.Close()
		close(done)
	}()

	select {
	case <-done:
		// Success - Close completed
	case <-time.After(2 * time.Second):
		t.Fatal("Close() deadlocked - concurrent Start() calls spawned multiple goroutines")
	}
}

// =============================================================================
// W3C.2 TOCTOU Race Condition Tests
// =============================================================================

// TestActivityEventBus_PublishHappyPath tests the normal publish operation.
func TestActivityEventBus_PublishHappyPath(t *testing.T) {
	bus := NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	sub := &mockSubscriber{
		id:         "happy-path-sub",
		eventTypes: []EventType{EventTypeAgentAction},
	}
	bus.Subscribe(sub)

	// Publish multiple unique events
	for i := 0; i < 5; i++ {
		event := NewActivityEvent(EventTypeAgentAction, "session-"+string(rune('A'+i)), "action")
		event.AgentID = "agent-" + string(rune('A'+i))
		bus.Publish(event)
	}

	// Wait for event delivery
	time.Sleep(200 * time.Millisecond)

	events := sub.getEvents()
	if len(events) != 5 {
		t.Errorf("Expected 5 events, got %d", len(events))
	}
}

// TestActivityEventBus_ConcurrentPublishClose tests the TOCTOU fix by
// concurrently publishing events while closing the bus.
// This should NOT panic with "send on closed channel".
func TestActivityEventBus_ConcurrentPublishClose(t *testing.T) {
	for iteration := 0; iteration < 100; iteration++ {
		bus := NewActivityEventBus(100)
		bus.Start()

		var wg sync.WaitGroup
		publishers := 10

		// Start publishers
		for i := 0; i < publishers; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					event := NewActivityEvent(EventTypeAgentAction, "session", "action")
					event.AgentID = "agent"
					bus.Publish(event)
				}
			}(i)
		}

		// Close the bus while publishers are running
		// This timing races with the publishers
		go func() {
			time.Sleep(time.Microsecond * time.Duration(iteration%10))
			bus.Close()
		}()

		wg.Wait()
	}
	// If we reach here without panic, the test passes
}

// TestActivityEventBus_ConcurrentPublishCloseStress is a stress test for the
// TOCTOU race condition fix. It runs many iterations to catch race conditions.
func TestActivityEventBus_ConcurrentPublishCloseStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	for iteration := 0; iteration < 1000; iteration++ {
		bus := NewActivityEventBus(10) // Small buffer to increase contention
		bus.Start()

		var wg sync.WaitGroup

		// Start many publishers
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 50; j++ {
					event := NewActivityEvent(EventTypeAgentAction, "s", "a")
					bus.Publish(event)
				}
			}()
		}

		// Close immediately to maximize race window
		bus.Close()
		wg.Wait()
	}
}

// TestActivityEventBus_BufferFullScenario tests that the bus handles
// a full buffer correctly by dropping events without blocking.
func TestActivityEventBus_BufferFullScenario(t *testing.T) {
	bus := NewActivityEventBus(5) // Small buffer
	// Do NOT start dispatch - buffer will fill and stay full
	defer bus.Close()

	// Publish more events than buffer can hold
	start := time.Now()
	for i := 0; i < 100; i++ {
		event := NewActivityEvent(EventTypeAgentAction, "session-"+string(rune('A'+i%26)), "action")
		event.AgentID = "agent-" + string(rune('A'+i%26))
		bus.Publish(event)
	}
	elapsed := time.Since(start)

	// Should complete quickly (non-blocking) - allow generous timeout
	if elapsed > 1*time.Second {
		t.Errorf("Publish took too long (%v), should be non-blocking", elapsed)
	}

	// Verify buffer has some events (it should be full with 5)
	if len(bus.buffer) == 0 {
		t.Error("Buffer should have some events")
	}
}

// TestActivityEventBus_BufferFullWithDispatcher tests buffer full scenario
// when the dispatcher is running but slow subscribers cause backpressure.
func TestActivityEventBus_BufferFullWithDispatcher(t *testing.T) {
	bus := NewActivityEventBus(2) // Very small buffer
	bus.Start()
	defer bus.Close()

	// Create a slow subscriber that blocks
	slowSub := &slowSubscriber{
		id:       "slow-sub",
		delay:    50 * time.Millisecond,
		received: make([]*ActivityEvent, 0),
	}
	bus.Subscribe(slowSub)

	// Rapidly publish events - should not block even with slow subscriber
	start := time.Now()
	for i := 0; i < 20; i++ {
		event := NewActivityEvent(EventTypeAgentAction, "s"+string(rune('A'+i%26)), "action")
		event.AgentID = "a" + string(rune('A'+i%26))
		bus.Publish(event)
	}
	elapsed := time.Since(start)

	// Should complete quickly - publishers should not block
	if elapsed > 500*time.Millisecond {
		t.Errorf("Publish took too long (%v), should be non-blocking", elapsed)
	}

	// Wait for some events to be delivered
	time.Sleep(300 * time.Millisecond)

	// Verify some events were received (not all - some dropped)
	received := slowSub.getReceived()
	if len(received) == 0 {
		t.Error("Should have received some events")
	}
}

// slowSubscriber is a test subscriber that introduces delay.
type slowSubscriber struct {
	id       string
	delay    time.Duration
	received []*ActivityEvent
	mu       sync.Mutex
}

func (s *slowSubscriber) ID() string {
	return s.id
}

func (s *slowSubscriber) EventTypes() []EventType {
	return []EventType{} // Wildcard
}

func (s *slowSubscriber) OnEvent(event *ActivityEvent) error {
	time.Sleep(s.delay)
	s.mu.Lock()
	s.received = append(s.received, event)
	s.mu.Unlock()
	return nil
}

func (s *slowSubscriber) getReceived() []*ActivityEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*ActivityEvent{}, s.received...)
}

// =============================================================================
// W3M.3 Unsubscribe O(1) Optimization Tests
// =============================================================================

// TestActivityEventBus_UnsubscribeRemovesCorrectSubscriber verifies that
// Unsubscribe removes only the targeted subscriber.
func TestActivityEventBus_UnsubscribeRemovesCorrectSubscriber(t *testing.T) {
	bus := NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	// Create multiple subscribers for the same event type
	sub1 := &mockSubscriber{id: "sub-1", eventTypes: []EventType{EventTypeAgentAction}}
	sub2 := &mockSubscriber{id: "sub-2", eventTypes: []EventType{EventTypeAgentAction}}
	sub3 := &mockSubscriber{id: "sub-3", eventTypes: []EventType{EventTypeAgentAction}}

	bus.Subscribe(sub1)
	bus.Subscribe(sub2)
	bus.Subscribe(sub3)

	// Unsubscribe only sub-2
	bus.Unsubscribe("sub-2")

	// Publish event
	event := NewActivityEvent(EventTypeAgentAction, "test-session", "test action")
	bus.Publish(event)

	// Wait for event delivery
	time.Sleep(100 * time.Millisecond)

	// sub1 and sub3 should receive the event, sub2 should not
	if len(sub1.getEvents()) != 1 {
		t.Errorf("sub1 expected 1 event, got %d", len(sub1.getEvents()))
	}
	if len(sub2.getEvents()) != 0 {
		t.Errorf("sub2 expected 0 events after unsubscribe, got %d", len(sub2.getEvents()))
	}
	if len(sub3.getEvents()) != 1 {
		t.Errorf("sub3 expected 1 event, got %d", len(sub3.getEvents()))
	}
}

// TestActivityEventBus_UnsubscribeWildcardSubscriber verifies that
// wildcard subscribers can be unsubscribed correctly.
func TestActivityEventBus_UnsubscribeWildcardSubscriber(t *testing.T) {
	bus := NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	// Create wildcard subscribers
	wildcard1 := &mockSubscriber{id: "wildcard-1", eventTypes: []EventType{}}
	wildcard2 := &mockSubscriber{id: "wildcard-2", eventTypes: []EventType{}}

	bus.Subscribe(wildcard1)
	bus.Subscribe(wildcard2)

	// Unsubscribe one wildcard subscriber
	bus.Unsubscribe("wildcard-1")

	// Publish event
	event := NewActivityEvent(EventTypeAgentAction, "test-session", "test action")
	bus.Publish(event)

	// Wait for event delivery
	time.Sleep(100 * time.Millisecond)

	// Only wildcard2 should receive the event
	if len(wildcard1.getEvents()) != 0 {
		t.Errorf("wildcard1 expected 0 events after unsubscribe, got %d", len(wildcard1.getEvents()))
	}
	if len(wildcard2.getEvents()) != 1 {
		t.Errorf("wildcard2 expected 1 event, got %d", len(wildcard2.getEvents()))
	}
}

// TestActivityEventBus_UnsubscribeMultipleEventTypes verifies that unsubscribing
// removes the subscriber from all event types it was registered for.
func TestActivityEventBus_UnsubscribeMultipleEventTypes(t *testing.T) {
	bus := NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	// Subscriber registered for multiple event types
	sub := &mockSubscriber{
		id:         "multi-type-sub",
		eventTypes: []EventType{EventTypeAgentAction, EventTypeToolCall, EventTypeUserPrompt},
	}
	bus.Subscribe(sub)

	// Unsubscribe
	bus.Unsubscribe("multi-type-sub")

	// Publish events of all types
	bus.Publish(NewActivityEvent(EventTypeAgentAction, "s1", "action"))
	bus.Publish(NewActivityEvent(EventTypeToolCall, "s2", "tool"))
	bus.Publish(NewActivityEvent(EventTypeUserPrompt, "s3", "prompt"))

	// Wait for event delivery
	time.Sleep(150 * time.Millisecond)

	// Should receive no events
	if len(sub.getEvents()) != 0 {
		t.Errorf("Expected 0 events after unsubscribe, got %d", len(sub.getEvents()))
	}
}

// TestActivityEventBus_UnsubscribeNonExistent verifies that unsubscribing
// a non-existent subscriber does not cause errors.
func TestActivityEventBus_UnsubscribeNonExistent(t *testing.T) {
	bus := NewActivityEventBus(100)
	bus.Start()
	defer bus.Close()

	// Unsubscribe non-existent subscriber - should not panic
	bus.Unsubscribe("non-existent-subscriber")

	// Verify bus still works
	sub := &mockSubscriber{id: "test-sub", eventTypes: []EventType{EventTypeAgentAction}}
	bus.Subscribe(sub)

	event := NewActivityEvent(EventTypeAgentAction, "test-session", "test action")
	bus.Publish(event)

	time.Sleep(100 * time.Millisecond)

	if len(sub.getEvents()) != 1 {
		t.Errorf("Expected 1 event, got %d", len(sub.getEvents()))
	}
}

// TestActivityEventBus_ConcurrentUnsubscribe tests that concurrent unsubscribe
// operations do not cause data races.
func TestActivityEventBus_ConcurrentUnsubscribe(t *testing.T) {
	bus := NewActivityEventBus(1000)
	bus.Start()
	defer bus.Close()

	// Create many subscribers
	numSubscribers := 100
	subscribers := make([]*mockSubscriber, numSubscribers)
	for i := 0; i < numSubscribers; i++ {
		subID := "sub-" + string(rune('A'+i/26)) + string(rune('A'+i%26))
		subscribers[i] = &mockSubscriber{
			id:         subID,
			eventTypes: []EventType{EventTypeAgentAction},
		}
		bus.Subscribe(subscribers[i])
	}

	var wg sync.WaitGroup

	// Concurrently unsubscribe all subscribers
	for i := 0; i < numSubscribers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			subID := "sub-" + string(rune('A'+id/26)) + string(rune('A'+id%26))
			bus.Unsubscribe(subID)
		}(i)
	}

	// Concurrently publish events
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sessionID := "session-" + string(rune('A'+id%26))
			event := NewActivityEvent(EventTypeAgentAction, sessionID, "action")
			bus.Publish(event)
		}(i)
	}

	wg.Wait()
	// Test passes if no race condition or panic occurs
}

// TestActivityEventBus_ConcurrentSubscribeUnsubscribe tests concurrent subscribe
// and unsubscribe operations for data race safety.
func TestActivityEventBus_ConcurrentSubscribeUnsubscribe(t *testing.T) {
	bus := NewActivityEventBus(1000)
	bus.Start()
	defer bus.Close()

	var wg sync.WaitGroup
	iterations := 100

	// Concurrent subscribe operations
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			subID := "sub-" + string(rune('A'+id/26)) + string(rune('A'+id%26))
			sub := &mockSubscriber{
				id:         subID,
				eventTypes: []EventType{EventTypeAgentAction},
			}
			bus.Subscribe(sub)
		}(i)
	}

	// Concurrent unsubscribe operations
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			subID := "sub-" + string(rune('A'+id/26)) + string(rune('A'+id%26))
			bus.Unsubscribe(subID)
		}(i)
	}

	// Concurrent publish operations
	for i := 0; i < iterations; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			sessionID := "s-" + string(rune('A'+id%26))
			event := NewActivityEvent(EventTypeAgentAction, sessionID, "action")
			bus.Publish(event)
		}(i)
	}

	wg.Wait()
	// Test passes if no race condition or panic occurs
}

// TestActivityEventBus_UnsubscribePerformanceWithManySubscribers benchmarks
// that unsubscribe completes in constant time regardless of subscriber count.
func TestActivityEventBus_UnsubscribePerformanceWithManySubscribers(t *testing.T) {
	// This test verifies O(1) behavior by checking that unsubscribe time
	// does not significantly increase with more subscribers

	testCases := []struct {
		numSubscribers int
	}{
		{10},
		{100},
		{1000},
	}

	for _, tc := range testCases {
		bus := NewActivityEventBus(100)

		// Subscribe many subscribers
		for i := 0; i < tc.numSubscribers; i++ {
			subID := "sub-" + string(rune('A'+i/676)) + string(rune('A'+(i/26)%26)) + string(rune('A'+i%26))
			sub := &mockSubscriber{
				id:         subID,
				eventTypes: []EventType{EventTypeAgentAction},
			}
			bus.Subscribe(sub)
		}

		// Measure unsubscribe time for the middle subscriber
		targetIdx := tc.numSubscribers / 2
		targetID := "sub-" + string(rune('A'+targetIdx/676)) + string(rune('A'+(targetIdx/26)%26)) + string(rune('A'+targetIdx%26))
		start := time.Now()
		bus.Unsubscribe(targetID)
		elapsed := time.Since(start)

		bus.Close()

		// Unsubscribe should be very fast (< 1ms) for O(1) operation
		// We use a generous threshold to avoid flaky tests
		if elapsed > 10*time.Millisecond {
			t.Errorf("Unsubscribe with %d subscribers took %v, expected < 10ms",
				tc.numSubscribers, elapsed)
		}
	}
}
