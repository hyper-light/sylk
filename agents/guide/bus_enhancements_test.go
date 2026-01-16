package guide

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/messaging"
)

// =============================================================================
// Topic Router Tests
// =============================================================================

func TestTopicRouter_ExactMatch(t *testing.T) {
	router := NewTopicRouter()

	received := make(chan string, 10)
	handler := func(msg *Message) error {
		received <- msg.ID
		return nil
	}

	// Subscribe to exact topic
	sub := router.Subscribe("agent.requests", handler)
	defer router.Unsubscribe(sub)

	// Match should find it
	matches := router.Match("agent.requests")
	if len(matches) != 1 {
		t.Errorf("expected 1 match, got %d", len(matches))
	}

	// Non-matching topic should not find it
	matches = router.Match("agent.responses")
	if len(matches) != 0 {
		t.Errorf("expected 0 matches, got %d", len(matches))
	}
}

func TestTopicRouter_SingleWildcard(t *testing.T) {
	router := NewTopicRouter()

	handler := func(msg *Message) error { return nil }

	// Subscribe to wildcard pattern
	sub := router.Subscribe("session.*.requests", handler)
	defer router.Unsubscribe(sub)

	// Should match any session
	matches := router.Match("session.abc123.requests")
	if len(matches) != 1 {
		t.Errorf("expected 1 match for session.abc123.requests, got %d", len(matches))
	}

	matches = router.Match("session.xyz789.requests")
	if len(matches) != 1 {
		t.Errorf("expected 1 match for session.xyz789.requests, got %d", len(matches))
	}

	// Should not match wrong channel
	matches = router.Match("session.abc123.responses")
	if len(matches) != 0 {
		t.Errorf("expected 0 matches for session.abc123.responses, got %d", len(matches))
	}

	// Should not match missing segment
	matches = router.Match("session.requests")
	if len(matches) != 0 {
		t.Errorf("expected 0 matches for session.requests, got %d", len(matches))
	}
}

func TestTopicRouter_MultiWildcard(t *testing.T) {
	router := NewTopicRouter()

	handler := func(msg *Message) error { return nil }

	// Subscribe to multi-segment wildcard
	sub := router.Subscribe("session.**", handler)
	defer router.Unsubscribe(sub)

	// Should match any depth under session
	testCases := []struct {
		topic   string
		matches int
	}{
		{"session.abc", 1},
		{"session.abc.requests", 1},
		{"session.abc.def.requests", 1},
		{"agent.requests", 0}, // Different prefix
	}

	for _, tc := range testCases {
		matches := router.Match(tc.topic)
		if len(matches) != tc.matches {
			t.Errorf("topic %q: expected %d matches, got %d", tc.topic, tc.matches, len(matches))
		}
	}
}

func TestTopicRouter_MixedWildcards(t *testing.T) {
	router := NewTopicRouter()

	handler := func(msg *Message) error { return nil }

	// Subscribe to pattern with mixed wildcards: session.*.agent.**
	sub := router.Subscribe("session.*.agent.**", handler)
	defer router.Unsubscribe(sub)

	testCases := []struct {
		topic   string
		matches int
	}{
		{"session.abc.agent.requests", 1},
		{"session.xyz.agent.sub.topic", 1},
		{"session.abc.other.requests", 0}, // "other" instead of "agent"
		{"session.agent.requests", 0},     // Missing session ID segment
	}

	for _, tc := range testCases {
		matches := router.Match(tc.topic)
		if len(matches) != tc.matches {
			t.Errorf("topic %q: expected %d matches, got %d", tc.topic, tc.matches, len(matches))
		}
	}
}

func TestTopicRouter_MultipleSubscriptions(t *testing.T) {
	router := NewTopicRouter()

	handler := func(msg *Message) error { return nil }

	// Multiple subscriptions to different patterns
	sub1 := router.Subscribe("agent.requests", handler)
	sub2 := router.Subscribe("*.requests", handler)
	sub3 := router.Subscribe("agent.*", handler)
	defer router.Unsubscribe(sub1)
	defer router.Unsubscribe(sub2)
	defer router.Unsubscribe(sub3)

	// Should match all three
	matches := router.Match("agent.requests")
	if len(matches) != 3 {
		t.Errorf("expected 3 matches, got %d", len(matches))
	}
}

func TestTopicRouter_Unsubscribe(t *testing.T) {
	router := NewTopicRouter()

	handler := func(msg *Message) error { return nil }

	sub := router.Subscribe("agent.requests", handler)

	// Should have 1 match
	matches := router.Match("agent.requests")
	if len(matches) != 1 {
		t.Errorf("expected 1 match, got %d", len(matches))
	}

	// Unsubscribe
	router.Unsubscribe(sub)

	// Should have 0 matches
	matches = router.Match("agent.requests")
	if len(matches) != 0 {
		t.Errorf("expected 0 matches after unsubscribe, got %d", len(matches))
	}
}

func TestTopicRouter_Stats(t *testing.T) {
	router := NewTopicRouter()

	handler := func(msg *Message) error { return nil }

	router.Subscribe("exact.topic", handler)
	router.Subscribe("wildcard.*", handler)

	// Perform some lookups
	router.Match("exact.topic")
	router.Match("wildcard.foo")
	router.Match("no.match")

	stats := router.Stats()
	if stats.Lookups != 3 {
		t.Errorf("expected 3 lookups, got %d", stats.Lookups)
	}
	if stats.TotalPatterns != 2 {
		t.Errorf("expected 2 total patterns, got %d", stats.TotalPatterns)
	}
}

// =============================================================================
// Topic Pattern Builder Tests
// =============================================================================

func TestTopicPatternBuilder(t *testing.T) {
	testCases := []struct {
		name     string
		builder  func() string
		expected string
	}{
		{
			name: "session topic",
			builder: func() string {
				return NewTopicPattern().Session().SessionID("abc").Agent("guide").Channel(ChannelTypeRequests).Build()
			},
			expected: "session.abc.guide.requests",
		},
		{
			name: "any session pattern",
			builder: func() string {
				return NewTopicPattern().Session().AnySession().AnyAgent().Channel(ChannelTypeRequests).Build()
			},
			expected: "session.*.*.requests",
		},
		{
			name: "all remaining pattern",
			builder: func() string {
				return NewTopicPattern().Session().SessionID("abc").AnyRemaining().Build()
			},
			expected: "session.abc.**",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.builder()
			if result != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, result)
			}
		})
	}
}

func TestBuildSessionTopic(t *testing.T) {
	topic := BuildSessionTopic("sess123", "agent1", ChannelTypeRequests)
	expected := "session.sess123.agent1.requests"
	if topic != expected {
		t.Errorf("expected %q, got %q", expected, topic)
	}
}

func TestParseSessionTopic(t *testing.T) {
	sessionID, agentID, channel, ok := ParseSessionTopic("session.abc123.guide.requests")
	if !ok {
		t.Error("expected parse to succeed")
	}
	if sessionID != "abc123" {
		t.Errorf("expected sessionID 'abc123', got %q", sessionID)
	}
	if agentID != "guide" {
		t.Errorf("expected agentID 'guide', got %q", agentID)
	}
	if channel != "requests" {
		t.Errorf("expected channel 'requests', got %q", channel)
	}

	// Invalid topic
	_, _, _, ok = ParseSessionTopic("invalid.topic")
	if ok {
		t.Error("expected parse to fail for invalid topic")
	}
}

// =============================================================================
// Message Filter Tests
// =============================================================================

func TestMessageFilter_SessionID(t *testing.T) {
	filter := &MessageFilter{SessionID: "sess123"}

	msg := &Message{
		ID:       "msg1",
		Metadata: map[string]any{"session_id": "sess123"},
	}
	if !filter.Matches(msg) {
		t.Error("expected filter to match message with correct session_id")
	}

	msg.Metadata["session_id"] = "other"
	if filter.Matches(msg) {
		t.Error("expected filter to reject message with different session_id")
	}
}

func TestMessageFilter_MessageTypes(t *testing.T) {
	filter := &MessageFilter{
		MessageTypes: []MessageType{MessageTypeRequest, MessageTypeResponse},
	}

	msg := &Message{ID: "msg1", Type: MessageTypeRequest}
	if !filter.Matches(msg) {
		t.Error("expected filter to match request type")
	}

	msg.Type = MessageTypeError
	if filter.Matches(msg) {
		t.Error("expected filter to reject error type")
	}
}

func TestMessageFilter_Priority(t *testing.T) {
	minPriority := messaging.PriorityNormal
	filter := &MessageFilter{MinPriority: &minPriority}

	msg := &Message{ID: "msg1", Priority: messaging.PriorityHigh}
	if !filter.Matches(msg) {
		t.Error("expected filter to match high priority")
	}

	msg.Priority = messaging.PriorityLow
	if filter.Matches(msg) {
		t.Error("expected filter to reject low priority")
	}
}

func TestFilteredHandler(t *testing.T) {
	var received []string
	handler := func(msg *Message) error {
		received = append(received, msg.ID)
		return nil
	}

	filter := &MessageFilter{
		MessageTypes: []MessageType{MessageTypeRequest},
	}

	filtered := FilteredHandler(filter, handler)

	// Should process request
	_ = filtered(&Message{ID: "msg1", Type: MessageTypeRequest})
	if len(received) != 1 || received[0] != "msg1" {
		t.Error("expected to receive msg1")
	}

	// Should filter out response
	_ = filtered(&Message{ID: "msg2", Type: MessageTypeResponse})
	if len(received) != 1 {
		t.Error("expected msg2 to be filtered out")
	}
}

// =============================================================================
// Session Bus Tests
// =============================================================================

func TestSessionBus_Publish(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer bus.Close()

	sb, err := NewSessionBus(bus, SessionBusConfig{
		SessionID:       "test-session",
		EnableWildcards: true,
	})
	if err != nil {
		t.Fatalf("failed to create session bus: %v", err)
	}
	defer sb.Close()

	received := make(chan *Message, 10)

	// Subscribe to session topic
	_, err = sb.Subscribe("guide.requests", func(msg *Message) error {
		received <- msg
		return nil
	})
	if err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}

	// Allow subscription to be registered
	time.Sleep(10 * time.Millisecond)

	// Publish message
	msg := &Message{
		ID:   "test-msg",
		Type: MessageTypeRequest,
	}
	if err := sb.Publish("guide.requests", msg); err != nil {
		t.Fatalf("failed to publish: %v", err)
	}

	// Wait for message
	select {
	case rcv := <-received:
		if rcv.ID != "test-msg" {
			t.Errorf("expected message ID 'test-msg', got %q", rcv.ID)
		}
		// Check session_id was added to metadata
		if sessID, ok := rcv.Metadata["session_id"].(string); !ok || sessID != "test-session" {
			t.Error("expected session_id in metadata")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for message")
	}
}

func TestSessionBus_SubscribePattern(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer bus.Close()

	sb, err := NewSessionBus(bus, SessionBusConfig{
		SessionID:       "test-session",
		EnableWildcards: true,
	})
	if err != nil {
		t.Fatalf("failed to create session bus: %v", err)
	}
	defer sb.Close()

	var receivedCount int64

	// Subscribe to wildcard pattern that matches session topics
	// Pattern: session.*.*.requests matches session.{sessionID}.{agent}.requests
	pattern := "session.*.*.requests"
	_, err = sb.SubscribePattern(pattern, func(msg *Message) error {
		atomic.AddInt64(&receivedCount, 1)
		return nil
	})
	if err != nil {
		t.Fatalf("failed to subscribe pattern: %v", err)
	}

	// Verify the router has the pattern
	if sb.router == nil {
		t.Fatal("router is nil")
	}
	routerStats := sb.router.Stats()
	if routerStats.TotalPatterns != 1 {
		t.Errorf("expected 1 pattern in router, got %d", routerStats.TotalPatterns)
	}

	// Use a non-global topic (myagent is not in the global prefix list)
	topic := "myagent.requests"
	fullTopic := sb.resolveTopic(topic)
	expectedTopic := "session.test-session.myagent.requests"
	if fullTopic != expectedTopic {
		t.Errorf("expected resolved topic %q, got %q", expectedTopic, fullTopic)
	}

	// Check if pattern matches the topic directly using the router
	matches := sb.router.Match(fullTopic)
	if len(matches) == 0 {
		t.Errorf("expected pattern %q to match topic %q, but got 0 matches", pattern, fullTopic)
	}

	// Publish to matching topic (will be session-prefixed)
	msg := &Message{ID: "test-msg"}
	if err := sb.Publish(topic, msg); err != nil {
		t.Fatalf("failed to publish: %v", err)
	}

	// Allow async processing
	time.Sleep(100 * time.Millisecond)

	// Check stats
	stats := sb.Stats()
	t.Logf("Session bus stats: published=%d, received=%d, wildcardMatches=%d",
		stats.MessagesPublished, stats.MessagesReceived, stats.WildcardMatches)

	// Check wildcard subscription received the message
	if atomic.LoadInt64(&receivedCount) == 0 {
		t.Error("expected wildcard subscription to receive message")
	}
}

func TestSessionBus_Close(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer bus.Close()

	sb, err := NewSessionBus(bus, SessionBusConfig{
		SessionID:       "test-session",
		EnableWildcards: false,
	})
	if err != nil {
		t.Fatalf("failed to create session bus: %v", err)
	}

	// Subscribe
	_, err = sb.Subscribe("test.topic", func(msg *Message) error { return nil })
	if err != nil {
		t.Fatalf("failed to subscribe: %v", err)
	}

	if sb.SubscriptionCount() != 1 {
		t.Errorf("expected 1 subscription, got %d", sb.SubscriptionCount())
	}

	// Close
	if err := sb.Close(); err != nil {
		t.Fatalf("failed to close: %v", err)
	}

	// Should not be able to publish after close
	if err := sb.Publish("test.topic", &Message{}); err != ErrSessionClosed {
		t.Errorf("expected ErrSessionClosed, got %v", err)
	}
}

func TestSessionBus_GlobalTopics(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer bus.Close()

	sb, err := NewSessionBus(bus, SessionBusConfig{
		SessionID:       "test-session",
		EnableWildcards: false,
	})
	if err != nil {
		t.Fatalf("failed to create session bus: %v", err)
	}
	defer sb.Close()

	received := make(chan *Message, 10)

	// Subscribe to global topic
	_, err = sb.SubscribeGlobal(TopicAgentRegistry, func(msg *Message) error {
		received <- msg
		return nil
	})
	if err != nil {
		t.Fatalf("failed to subscribe global: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	// Publish to global topic (through underlying bus)
	msg := &Message{ID: "global-msg", Type: MessageTypeAgentRegistered}
	if err := bus.Publish(TopicAgentRegistry, msg); err != nil {
		t.Fatalf("failed to publish: %v", err)
	}

	select {
	case rcv := <-received:
		if rcv.ID != "global-msg" {
			t.Errorf("expected message ID 'global-msg', got %q", rcv.ID)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("timeout waiting for global message")
	}
}

// =============================================================================
// Session Bus Manager Tests
// =============================================================================

func TestSessionBusManager_GetOrCreate(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer bus.Close()

	manager := NewSessionBusManager(bus, SessionBusManagerConfig{
		EnableWildcards: true,
	})

	// Create first session
	sb1, err := manager.GetOrCreate("session-1")
	if err != nil {
		t.Fatalf("failed to create session bus: %v", err)
	}
	if sb1.SessionID() != "session-1" {
		t.Errorf("expected session ID 'session-1', got %q", sb1.SessionID())
	}

	// Get same session
	sb2, err := manager.GetOrCreate("session-1")
	if err != nil {
		t.Fatalf("failed to get session bus: %v", err)
	}
	if sb1 != sb2 {
		t.Error("expected same session bus instance")
	}

	// Create another session
	sb3, err := manager.GetOrCreate("session-2")
	if err != nil {
		t.Fatalf("failed to create session bus: %v", err)
	}
	if sb3 == sb1 {
		t.Error("expected different session bus instance")
	}

	stats := manager.Stats()
	if stats.ActiveSessions != 2 {
		t.Errorf("expected 2 active sessions, got %d", stats.ActiveSessions)
	}
}

func TestSessionBusManager_Close(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer bus.Close()

	var closedSessions []string
	var mu sync.Mutex

	manager := NewSessionBusManager(bus, SessionBusManagerConfig{
		OnSessionClosed: func(sessionID string) {
			mu.Lock()
			closedSessions = append(closedSessions, sessionID)
			mu.Unlock()
		},
	})

	// Create sessions
	_, _ = manager.GetOrCreate("session-1")
	_, _ = manager.GetOrCreate("session-2")

	// Close one session
	if err := manager.Close("session-1"); err != nil {
		t.Fatalf("failed to close session: %v", err)
	}

	time.Sleep(10 * time.Millisecond)

	mu.Lock()
	if len(closedSessions) != 1 || closedSessions[0] != "session-1" {
		t.Errorf("expected session-1 to be closed, got %v", closedSessions)
	}
	mu.Unlock()

	stats := manager.Stats()
	if stats.ActiveSessions != 1 {
		t.Errorf("expected 1 active session, got %d", stats.ActiveSessions)
	}

	// Session should not be retrievable
	if sb := manager.Get("session-1"); sb != nil {
		t.Error("expected session-1 to not be retrievable after close")
	}
}

func TestSessionBusManager_CloseAll(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer bus.Close()

	manager := NewSessionBusManager(bus, SessionBusManagerConfig{})

	// Create sessions
	_, _ = manager.GetOrCreate("session-1")
	_, _ = manager.GetOrCreate("session-2")
	_, _ = manager.GetOrCreate("session-3")

	// Close all
	manager.CloseAll()

	stats := manager.Stats()
	if stats.ActiveSessions != 0 {
		t.Errorf("expected 0 active sessions, got %d", stats.ActiveSessions)
	}
	if stats.SessionsClosed != 3 {
		t.Errorf("expected 3 sessions closed, got %d", stats.SessionsClosed)
	}
}

func TestSessionBusManager_ActiveSessionIDs(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer bus.Close()

	manager := NewSessionBusManager(bus, SessionBusManagerConfig{})

	_, _ = manager.GetOrCreate("session-a")
	_, _ = manager.GetOrCreate("session-b")
	_, _ = manager.GetOrCreate("session-c")

	ids := manager.ActiveSessionIDs()
	if len(ids) != 3 {
		t.Errorf("expected 3 session IDs, got %d", len(ids))
	}

	// Check all IDs are present (order may vary)
	idMap := make(map[string]bool)
	for _, id := range ids {
		idMap[id] = true
	}
	for _, expected := range []string{"session-a", "session-b", "session-c"} {
		if !idMap[expected] {
			t.Errorf("expected %q in active session IDs", expected)
		}
	}
}

// =============================================================================
// Concurrent Access Tests
// =============================================================================

func TestTopicRouter_Concurrent(t *testing.T) {
	router := NewTopicRouter()

	var wg sync.WaitGroup
	handler := func(msg *Message) error { return nil }

	// Concurrent subscriptions and matches
	for i := 0; i < 100; i++ {
		wg.Add(2)

		go func(i int) {
			defer wg.Done()
			sub := router.Subscribe("topic."+string(rune('a'+i%26)), handler)
			time.Sleep(time.Millisecond)
			router.Unsubscribe(sub)
		}(i)

		go func() {
			defer wg.Done()
			router.Match("topic.a")
		}()
	}

	wg.Wait()
}

func TestSessionBusManager_Concurrent(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer bus.Close()

	manager := NewSessionBusManager(bus, SessionBusManagerConfig{})

	var wg sync.WaitGroup

	// Concurrent GetOrCreate
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sessionID := "session-" + string(rune('a'+i%10))
			_, _ = manager.GetOrCreate(sessionID)
		}(i)
	}

	wg.Wait()

	// Should have at most 10 unique sessions
	stats := manager.Stats()
	if stats.ActiveSessions > 10 {
		t.Errorf("expected at most 10 active sessions, got %d", stats.ActiveSessions)
	}

	manager.CloseAll()
}
