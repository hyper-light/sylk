package scribe

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/agents/guide"
	"github.com/adalundhe/sylk/agents/shared"
	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/providers"
)

// --- test helpers ---

type mockProvider struct {
	mu        sync.Mutex
	calls     int
	response  string
	err       error
	lastReq   *providers.Request
}

func (p *mockProvider) Complete(_ context.Context, req *providers.Request) (*providers.Response, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	p.lastReq = req
	if p.err != nil {
		return nil, p.err
	}
	return &providers.Response{Content: p.response}, nil
}

func (p *mockProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

type mockBus struct {
	mu         sync.Mutex
	published  []*guide.Message
	topics     []string
	subs       map[string]guide.MessageHandler
}

func newMockBus() *mockBus {
	return &mockBus{subs: make(map[string]guide.MessageHandler)}
}

func (b *mockBus) Publish(topic string, msg *guide.Message) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.published = append(b.published, msg)
	b.topics = append(b.topics, topic)
	return nil
}

func (b *mockBus) Subscribe(topic string, handler guide.MessageHandler) (guide.Subscription, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subs[topic] = handler
	return &mockSub{}, nil
}

func (b *mockBus) SubscribeAsync(topic string, handler guide.MessageHandler) (guide.Subscription, error) {
	return b.Subscribe(topic, handler)
}

func (b *mockBus) Close() error { return nil }

func (b *mockBus) publishCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.published)
}

func (b *mockBus) lastTopic() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.topics) == 0 {
		return ""
	}
	return b.topics[len(b.topics)-1]
}

type mockSub struct{ unsubscribed atomic.Bool }

func (s *mockSub) Unsubscribe() { s.unsubscribed.Store(true) }

// --- Scribe tests ---

func newTestScope(t *testing.T) *concurrency.GoroutineScope {
	t.Helper()
	scope := concurrency.NewGoroutineScope("test-scribe", context.Background(), nil, 0)
	t.Cleanup(func() { _ = scope.Shutdown(context.Background()) })
	return scope
}

func TestScribe_New(t *testing.T) {
	bus := newMockBus()
	scope := newTestScope(t)

	s := New(Config{
		ParentAgentType: "engineer",
		Provider:        &mockProvider{response: `{"summary":"test"}`},
		Model:           "test-model",
		Bus:             bus,
		Scope:           scope,
	})

	if s.id == "" {
		t.Fatal("expected non-empty ID")
	}
	if s.parentAgentType != "engineer" {
		t.Errorf("expected parent type engineer, got %s", s.parentAgentType)
	}
	if s.maxMessages != defaultMaxMessages {
		t.Errorf("expected max messages %d, got %d", defaultMaxMessages, s.maxMessages)
	}
}

func TestScribe_StartStop(t *testing.T) {
	bus := newMockBus()
	scope := newTestScope(t)

	s := New(Config{
		ParentAgentType: "architect",
		Provider:        &mockProvider{response: `{}`},
		Model:           "test-model",
		Bus:             bus,
		Scope:           scope,
	})

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !s.running.Load() {
		t.Error("expected running=true after Start")
	}

	// Double start is a no-op.
	if err := s.Start(); err != nil {
		t.Fatalf("double Start: %v", err)
	}

	if err := s.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if s.running.Load() {
		t.Error("expected running=false after Stop")
	}

	// Double stop is a no-op.
	if err := s.Stop(); err != nil {
		t.Fatalf("double Stop: %v", err)
	}
}

func TestScribe_Feed_ProcessesAndForwards(t *testing.T) {
	bus := newMockBus()
	scope := newTestScope(t)
	prov := &mockProvider{response: `{"summary":"engineer built feature X"}`}

	s := New(Config{
		ParentAgentType: "engineer",
		Provider:        prov,
		Model:           "test-model",
		Bus:             bus,
		Scope:           scope,
	})

	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	s.Feed(shared.ScribeFeed{
		UserRequest:   "build feature X",
		AgentResponse: "built feature X",
		CorrelationID: "corr-1",
		Timestamp:     time.Now(),
	})

	// Wait for async feed processing.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if prov.callCount() > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if prov.callCount() != 1 {
		t.Fatalf("expected 1 LLM call, got %d", prov.callCount())
	}

	if bus.publishCount() != 1 {
		t.Fatalf("expected 1 bus publish, got %d", bus.publishCount())
	}

	expectedTopic := guide.TopicRequests("archivalist", "archivalist")
	if got := bus.lastTopic(); got != expectedTopic {
		t.Errorf("expected topic %q, got %q", expectedTopic, got)
	}

	if err := s.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

func TestScribe_AppendMessage_RingBuffer(t *testing.T) {
	bus := newMockBus()
	scope := newTestScope(t)

	s := New(Config{
		ParentAgentType: "test",
		Provider:        &mockProvider{response: "{}"},
		Model:           "test-model",
		Bus:             bus,
		Scope:           scope,
		MaxMessages:     3, // 3 pairs max = 6 messages
	})

	// Add 4 pairs (8 messages) — should evict oldest pair.
	for i := range 4 {
		s.appendMessage(providers.RoleUser, "u"+string(rune('0'+i)))
		s.appendMessage(providers.RoleAssistant, "a"+string(rune('0'+i)))
	}

	s.messagesMu.RLock()
	got := len(s.messages)
	s.messagesMu.RUnlock()

	if got != 6 {
		t.Errorf("expected 6 messages (3 pairs), got %d", got)
	}
}

func TestScribe_HandoffableAgent(t *testing.T) {
	bus := newMockBus()
	scope := newTestScope(t)

	s := New(Config{
		ParentAgentType: "designer",
		Provider:        &mockProvider{response: "{}"},
		Model:           "gemini-3-flash-preview",
		Bus:             bus,
		Scope:           scope,
	})

	if s.AgentType() != "scribe-designer" {
		t.Errorf("expected agent type scribe-designer, got %s", s.AgentType())
	}

	desc := s.Descriptor()
	if desc.AgentType != "scribe-designer" {
		t.Errorf("expected descriptor type scribe-designer, got %s", desc.AgentType)
	}
	if desc.ModelID != "gemini-3-flash-preview" {
		t.Errorf("expected model gemini-3-flash-preview, got %s", desc.ModelID)
	}
	if desc.ContextWindow != 1_000_000 {
		t.Errorf("expected context window 1000000, got %d", desc.ContextWindow)
	}

	state := s.ExtractArchivableState()
	if state.AgentType != "scribe-designer" {
		t.Errorf("expected archivable type scribe-designer, got %s", state.AgentType)
	}
}

func TestScribe_Feed_DropsWhenBufferFull(t *testing.T) {
	bus := newMockBus()
	scope := newTestScope(t)

	// Create a Scribe but don't start it — feedCh will fill up.
	s := New(Config{
		ParentAgentType: "test",
		Provider:        &mockProvider{response: "{}"},
		Model:           "test-model",
		Bus:             bus,
		Scope:           scope,
	})

	// Fill the buffer (feedBufferSize = 32).
	for range feedBufferSize + 5 {
		s.Feed(shared.ScribeFeed{
			UserRequest:   "req",
			AgentResponse: "resp",
			CorrelationID: "c",
			Timestamp:     time.Now(),
		})
	}

	// Should not deadlock — Feed is non-blocking.
	if len(s.feedCh) > feedBufferSize {
		t.Errorf("feedCh exceeded buffer size: %d", len(s.feedCh))
	}
}
