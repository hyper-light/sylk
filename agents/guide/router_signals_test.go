package guide

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockSignalEmitter implements RouterSignalEmitter for testing.
type mockSignalEmitter struct {
	mu      sync.Mutex
	signals []agentRequestSignal
}

type agentRequestSignal struct {
	FromAgentID string
	SessionID   string
	ToAgentID   string
}

func newMockSignalEmitter() *mockSignalEmitter {
	return &mockSignalEmitter{
		signals: make([]agentRequestSignal, 0),
	}
}

func (m *mockSignalEmitter) EmitAgentRequest(fromAgentID, sessionID, toAgentID string) {
	m.mu.Lock()
	m.signals = append(m.signals, agentRequestSignal{
		FromAgentID: fromAgentID,
		SessionID:   sessionID,
		ToAgentID:   toAgentID,
	})
	m.mu.Unlock()
}

func (m *mockSignalEmitter) getSignals() []agentRequestSignal {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]agentRequestSignal, len(m.signals))
	copy(result, m.signals)
	return result
}

func (m *mockSignalEmitter) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.signals)
}

func (m *mockSignalEmitter) reset() {
	m.mu.Lock()
	m.signals = m.signals[:0]
	m.mu.Unlock()
}

// =============================================================================
// routerSignals Unit Tests
// =============================================================================

func TestRouterSignals_NewRouterSignals(t *testing.T) {
	rs := newRouterSignals()
	if rs == nil {
		t.Fatal("expected non-nil routerSignals")
	}
}

func TestRouterSignals_SetSignalEmitter(t *testing.T) {
	rs := newRouterSignals()
	emitter := newMockSignalEmitter()

	rs.SetSignalEmitter(emitter)

	// Emit a signal - should work
	rs.SetSessionID("test-session")
	rs.emitAgentRequest("agent-1", "agent-2")

	if emitter.count() != 1 {
		t.Errorf("expected 1 signal, got %d", emitter.count())
	}
}

func TestRouterSignals_NilEmitter_NoOp(t *testing.T) {
	rs := newRouterSignals()

	// Should not panic with nil emitter
	rs.emitAgentRequest("agent-1", "agent-2")
}

func TestRouterSignals_SetSessionID(t *testing.T) {
	rs := newRouterSignals()
	emitter := newMockSignalEmitter()

	rs.SetSignalEmitter(emitter)
	rs.SetSessionID("session-123")
	rs.emitAgentRequest("agent-1", "agent-2")

	signals := emitter.getSignals()
	if len(signals) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(signals))
	}
	if signals[0].SessionID != "session-123" {
		t.Errorf("SessionID = %q, want %q", signals[0].SessionID, "session-123")
	}
}

func TestRouterSignals_EmitAgentRequest(t *testing.T) {
	rs := newRouterSignals()
	emitter := newMockSignalEmitter()

	rs.SetSignalEmitter(emitter)
	rs.SetSessionID("sess-1")
	rs.emitAgentRequest("from-agent", "to-agent")

	signals := emitter.getSignals()
	if len(signals) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(signals))
	}

	sig := signals[0]
	if sig.FromAgentID != "from-agent" {
		t.Errorf("FromAgentID = %q, want %q", sig.FromAgentID, "from-agent")
	}
	if sig.ToAgentID != "to-agent" {
		t.Errorf("ToAgentID = %q, want %q", sig.ToAgentID, "to-agent")
	}
	if sig.SessionID != "sess-1" {
		t.Errorf("SessionID = %q, want %q", sig.SessionID, "sess-1")
	}
}

// =============================================================================
// TieredRouter Signal Integration Tests
// =============================================================================

func createTestRouterWithSignals(t *testing.T) (*TieredRouter, *ChannelBus, *mockSignalEmitter) {
	t.Helper()

	bus := NewChannelBus(DefaultChannelBusConfig())
	router, err := NewTieredRouter(TieredRouterConfig{
		AgentID:   "test-agent",
		AgentName: "Test Agent",
		Bus:       bus,
	})
	if err != nil {
		t.Fatalf("failed to create router: %v", err)
	}

	emitter := newMockSignalEmitter()
	router.SetSignalEmitter(emitter)
	router.SetSignalSessionID("test-session")

	return router, bus, emitter
}

func TestTieredRouter_SetSignalEmitter(t *testing.T) {
	router, bus, _ := createTestRouterWithSignals(t)
	defer bus.Close()

	// Replace with new emitter
	newEmitter := newMockSignalEmitter()
	router.SetSignalEmitter(newEmitter)

	// Verify by checking the signals field directly
	if router.signals == nil {
		t.Error("expected signals to be initialized")
	}
}

func TestTieredRouter_SetSignalSessionID(t *testing.T) {
	router, bus, emitter := createTestRouterWithSignals(t)
	defer bus.Close()

	router.SetSignalSessionID("custom-session")

	// Trigger a signal emission via emitDirectRouteSignal
	router.emitDirectRouteSignal("target-agent")

	signals := emitter.getSignals()
	if len(signals) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(signals))
	}
	if signals[0].SessionID != "custom-session" {
		t.Errorf("SessionID = %q, want %q", signals[0].SessionID, "custom-session")
	}
}

func TestTieredRouter_DirectRouting_EmitsSignal(t *testing.T) {
	router, bus, emitter := createTestRouterWithSignals(t)
	defer bus.Close()

	// Start the router
	if err := router.Start(func(msg *Message) error { return nil }); err != nil {
		t.Fatalf("failed to start router: %v", err)
	}
	defer router.Stop()

	// Register a target agent as known and ready
	router.knownAgents.Set("target-agent", &AgentAnnouncement{
		AgentID:   "target-agent",
		AgentName: "Target",
	})
	router.readyAgents.Set("target-agent", true)

	// Route directly to the agent
	_, err := router.RouteToAgent(context.Background(), "target-agent", "test input")
	if err != nil {
		t.Fatalf("RouteToAgent failed: %v", err)
	}

	signals := emitter.getSignals()
	if len(signals) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(signals))
	}

	sig := signals[0]
	if sig.FromAgentID != "test-agent" {
		t.Errorf("FromAgentID = %q, want %q", sig.FromAgentID, "test-agent")
	}
	if sig.ToAgentID != "target-agent" {
		t.Errorf("ToAgentID = %q, want %q", sig.ToAgentID, "target-agent")
	}
}

func TestTieredRouter_GuideRouting_EmitsSignal(t *testing.T) {
	router, bus, emitter := createTestRouterWithSignals(t)
	defer bus.Close()

	// Start the router
	if err := router.Start(func(msg *Message) error { return nil }); err != nil {
		t.Fatalf("failed to start router: %v", err)
	}
	defer router.Stop()

	// Route without any known agents - should fall back to Guide
	_, err := router.Route(context.Background(), "test input")
	if err != nil {
		t.Fatalf("Route failed: %v", err)
	}

	signals := emitter.getSignals()
	if len(signals) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(signals))
	}

	sig := signals[0]
	if sig.FromAgentID != "test-agent" {
		t.Errorf("FromAgentID = %q, want %q", sig.FromAgentID, "test-agent")
	}
	if sig.ToAgentID != "guide" {
		t.Errorf("ToAgentID = %q, want %q", sig.ToAgentID, "guide")
	}
}

func TestTieredRouter_TriggerAction_EmitsSignal(t *testing.T) {
	router, bus, emitter := createTestRouterWithSignals(t)
	defer bus.Close()

	// Start the router
	if err := router.Start(func(msg *Message) error { return nil }); err != nil {
		t.Fatalf("failed to start router: %v", err)
	}
	defer router.Stop()

	// Trigger an action
	_, err := router.TriggerAction(context.Background(), "archivalist", "preserve", map[string]string{"key": "value"})
	if err != nil {
		t.Fatalf("TriggerAction failed: %v", err)
	}

	signals := emitter.getSignals()
	if len(signals) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(signals))
	}

	sig := signals[0]
	if sig.FromAgentID != "test-agent" {
		t.Errorf("FromAgentID = %q, want %q", sig.FromAgentID, "test-agent")
	}
	if sig.ToAgentID != "archivalist" {
		t.Errorf("ToAgentID = %q, want %q", sig.ToAgentID, "archivalist")
	}
}

func TestTieredRouter_NilSignalEmitter_NoOp(t *testing.T) {
	bus := NewChannelBus(DefaultChannelBusConfig())
	defer bus.Close()

	router, err := NewTieredRouter(TieredRouterConfig{
		AgentID:   "test-agent",
		AgentName: "Test Agent",
		Bus:       bus,
	})
	if err != nil {
		t.Fatalf("failed to create router: %v", err)
	}

	// Do NOT set a signal emitter - should be nil by default

	// Start the router
	if err := router.Start(func(msg *Message) error { return nil }); err != nil {
		t.Fatalf("failed to start router: %v", err)
	}
	defer router.Stop()

	// These should not panic with nil emitter
	_, err = router.Route(context.Background(), "test input")
	if err != nil {
		t.Fatalf("Route failed: %v", err)
	}

	_, err = router.TriggerAction(context.Background(), "target", "action", nil)
	if err != nil {
		t.Fatalf("TriggerAction failed: %v", err)
	}
}

func TestTieredRouter_DirectRoute_FallsBackToGuide_EmitsGuideSignal(t *testing.T) {
	router, bus, emitter := createTestRouterWithSignals(t)
	defer bus.Close()

	// Start the router
	if err := router.Start(func(msg *Message) error { return nil }); err != nil {
		t.Fatalf("failed to start router: %v", err)
	}
	defer router.Stop()

	// Try to route to unknown agent - should fall back to Guide
	_, err := router.RouteToAgent(context.Background(), "unknown-agent", "test input")
	if err != nil {
		t.Fatalf("RouteToAgent failed: %v", err)
	}

	signals := emitter.getSignals()
	if len(signals) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(signals))
	}

	// Should emit Guide signal since unknown agent falls back to Guide
	if signals[0].ToAgentID != "guide" {
		t.Errorf("ToAgentID = %q, want %q", signals[0].ToAgentID, "guide")
	}
}

func TestTieredRouter_MultipleRoutes_MultipleSignals(t *testing.T) {
	router, bus, emitter := createTestRouterWithSignals(t)
	defer bus.Close()

	// Start the router
	if err := router.Start(func(msg *Message) error { return nil }); err != nil {
		t.Fatalf("failed to start router: %v", err)
	}
	defer router.Stop()

	// Register multiple target agents
	for _, agentID := range []string{"agent-1", "agent-2", "agent-3"} {
		router.knownAgents.Set(agentID, &AgentAnnouncement{
			AgentID:   agentID,
			AgentName: agentID,
		})
		router.readyAgents.Set(agentID, true)
	}

	// Route to multiple agents
	for _, agentID := range []string{"agent-1", "agent-2", "agent-3"} {
		_, err := router.RouteToAgent(context.Background(), agentID, "test input")
		if err != nil {
			t.Fatalf("RouteToAgent to %s failed: %v", agentID, err)
		}
	}

	signals := emitter.getSignals()
	if len(signals) != 3 {
		t.Fatalf("expected 3 signals, got %d", len(signals))
	}

	// Verify each signal
	expectedTargets := map[string]bool{"agent-1": false, "agent-2": false, "agent-3": false}
	for _, sig := range signals {
		if sig.FromAgentID != "test-agent" {
			t.Errorf("FromAgentID = %q, want %q", sig.FromAgentID, "test-agent")
		}
		expectedTargets[sig.ToAgentID] = true
	}
	for target, seen := range expectedTargets {
		if !seen {
			t.Errorf("expected signal to %s not found", target)
		}
	}
}

// =============================================================================
// Race Condition Tests
// =============================================================================

func TestRouterSignals_ConcurrentEmit(t *testing.T) {
	rs := newRouterSignals()
	emitter := newMockSignalEmitter()

	rs.SetSignalEmitter(emitter)
	rs.SetSessionID("concurrent-session")

	const goroutines = 100
	const signalsPerGoroutine = 10

	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < signalsPerGoroutine; j++ {
				rs.emitAgentRequest("from-agent", "to-agent")
			}
		}(i)
	}

	wg.Wait()

	expected := goroutines * signalsPerGoroutine
	if emitter.count() != expected {
		t.Errorf("expected %d signals, got %d", expected, emitter.count())
	}
}

func TestRouterSignals_ConcurrentSetEmitter(t *testing.T) {
	rs := newRouterSignals()

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	// Half the goroutines set emitters
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			emitter := newMockSignalEmitter()
			rs.SetSignalEmitter(emitter)
		}()
	}

	// Half the goroutines emit signals
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			rs.emitAgentRequest("from", "to")
		}()
	}

	wg.Wait()
	// No panic means success - we're testing for race conditions
}

func TestRouterSignals_ConcurrentSetSessionID(t *testing.T) {
	rs := newRouterSignals()
	emitter := newMockSignalEmitter()
	rs.SetSignalEmitter(emitter)

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	// Half set session IDs
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()
			rs.SetSessionID("session-" + string(rune('a'+id%26)))
		}(i)
	}

	// Half emit signals
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			rs.emitAgentRequest("from", "to")
		}()
	}

	wg.Wait()
	// No panic means success
}

func TestTieredRouter_ConcurrentRouting_Race(t *testing.T) {
	router, bus, emitter := createTestRouterWithSignals(t)
	defer bus.Close()

	// Start the router
	if err := router.Start(func(msg *Message) error { return nil }); err != nil {
		t.Fatalf("failed to start router: %v", err)
	}
	defer router.Stop()

	// Register target agents
	for i := 0; i < 10; i++ {
		agentID := "agent-" + string(rune('a'+i))
		router.knownAgents.Set(agentID, &AgentAnnouncement{
			AgentID:   agentID,
			AgentName: agentID,
		})
		router.readyAgents.Set(agentID, true)
	}

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	var successCount int64

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()

			agentID := "agent-" + string(rune('a'+id%10))
			_, err := router.RouteToAgent(context.Background(), agentID, "test input")
			if err == nil {
				atomic.AddInt64(&successCount, 1)
			}
		}(i)
	}

	wg.Wait()

	// All routes should succeed
	if successCount != goroutines {
		t.Errorf("expected %d successful routes, got %d", goroutines, successCount)
	}

	// Should have signals for each successful route
	if emitter.count() != int(successCount) {
		t.Errorf("expected %d signals, got %d", successCount, emitter.count())
	}
}

func TestTieredRouter_ConcurrentTriggerAction_Race(t *testing.T) {
	router, bus, emitter := createTestRouterWithSignals(t)
	defer bus.Close()

	// Start the router
	if err := router.Start(func(msg *Message) error { return nil }); err != nil {
		t.Fatalf("failed to start router: %v", err)
	}
	defer router.Stop()

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	var successCount int64

	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer wg.Done()

			targetID := "target-" + string(rune('a'+id%10))
			_, err := router.TriggerAction(context.Background(), targetID, "test-action", nil)
			if err == nil {
				atomic.AddInt64(&successCount, 1)
			}
		}(i)
	}

	wg.Wait()

	// All actions should succeed
	if successCount != goroutines {
		t.Errorf("expected %d successful actions, got %d", goroutines, successCount)
	}

	// Should have signals for each successful action
	if emitter.count() != int(successCount) {
		t.Errorf("expected %d signals, got %d", successCount, emitter.count())
	}
}

func TestTieredRouter_ConcurrentSetEmitterAndRoute_Race(t *testing.T) {
	router, bus, _ := createTestRouterWithSignals(t)
	defer bus.Close()

	// Start the router
	if err := router.Start(func(msg *Message) error { return nil }); err != nil {
		t.Fatalf("failed to start router: %v", err)
	}
	defer router.Stop()

	const goroutines = 30
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)

	// Half set emitters
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			emitter := newMockSignalEmitter()
			router.SetSignalEmitter(emitter)
		}()
	}

	// Half route
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			router.Route(context.Background(), "test input")
		}()
	}

	wg.Wait()
	// No panic means success
}

// =============================================================================
// Edge Cases
// =============================================================================

func TestRouterSignals_EmptySessionID(t *testing.T) {
	rs := newRouterSignals()
	emitter := newMockSignalEmitter()

	rs.SetSignalEmitter(emitter)
	// Don't set session ID - should remain empty

	rs.emitAgentRequest("from", "to")

	signals := emitter.getSignals()
	if len(signals) != 1 {
		t.Fatalf("expected 1 signal, got %d", len(signals))
	}
	if signals[0].SessionID != "" {
		t.Errorf("SessionID = %q, want empty string", signals[0].SessionID)
	}
}

func TestRouterSignals_ReplaceEmitter(t *testing.T) {
	rs := newRouterSignals()

	emitter1 := newMockSignalEmitter()
	emitter2 := newMockSignalEmitter()

	rs.SetSignalEmitter(emitter1)
	rs.SetSessionID("session-1")
	rs.emitAgentRequest("from", "to")

	// Replace emitter
	rs.SetSignalEmitter(emitter2)
	rs.emitAgentRequest("from2", "to2")

	if emitter1.count() != 1 {
		t.Errorf("emitter1 count = %d, want 1", emitter1.count())
	}
	if emitter2.count() != 1 {
		t.Errorf("emitter2 count = %d, want 1", emitter2.count())
	}

	// Verify signals went to correct emitters
	if emitter1.getSignals()[0].FromAgentID != "from" {
		t.Error("emitter1 received wrong signal")
	}
	if emitter2.getSignals()[0].FromAgentID != "from2" {
		t.Error("emitter2 received wrong signal")
	}
}

func TestRouterSignals_DisableBySettingNil(t *testing.T) {
	rs := newRouterSignals()

	emitter := newMockSignalEmitter()
	rs.SetSignalEmitter(emitter)
	rs.emitAgentRequest("from", "to")

	// Disable by setting nil
	rs.SetSignalEmitter(nil)
	rs.emitAgentRequest("from2", "to2")

	// Should only have 1 signal (from before disabling)
	if emitter.count() != 1 {
		t.Errorf("expected 1 signal, got %d", emitter.count())
	}
}

// =============================================================================
// Benchmark
// =============================================================================

func BenchmarkRouterSignals_EmitAgentRequest(b *testing.B) {
	rs := newRouterSignals()
	emitter := newMockSignalEmitter()
	rs.SetSignalEmitter(emitter)
	rs.SetSessionID("bench-session")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rs.emitAgentRequest("from-agent", "to-agent")
	}
}

func BenchmarkRouterSignals_EmitAgentRequest_Parallel(b *testing.B) {
	rs := newRouterSignals()
	emitter := newMockSignalEmitter()
	rs.SetSignalEmitter(emitter)
	rs.SetSessionID("bench-session")

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			rs.emitAgentRequest("from-agent", "to-agent")
		}
	})
}

func BenchmarkRouterSignals_NilEmitter(b *testing.B) {
	rs := newRouterSignals()
	// No emitter set - should be fast no-op

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rs.emitAgentRequest("from-agent", "to-agent")
	}
}

// =============================================================================
// Signal Metrics Tests
// =============================================================================

func TestSignalMetrics_ZeroValue(t *testing.T) {
	var metrics SignalMetrics
	if metrics.DirectRouteSignals != 0 {
		t.Error("expected zero value for DirectRouteSignals")
	}
	if metrics.GuideRouteSignals != 0 {
		t.Error("expected zero value for GuideRouteSignals")
	}
	if metrics.ActionSignals != 0 {
		t.Error("expected zero value for ActionSignals")
	}
	if !metrics.LastSignalTime.IsZero() {
		t.Error("expected zero value for LastSignalTime")
	}
}

// =============================================================================
// Interface Compliance Tests
// =============================================================================

func TestRouterSignalEmitter_InterfaceCompliance(t *testing.T) {
	// Verify mockSignalEmitter implements RouterSignalEmitter
	var _ RouterSignalEmitter = (*mockSignalEmitter)(nil)
}

// slowEmitter introduces artificial delay for testing timeout scenarios.
type slowEmitter struct {
	delay time.Duration
	count int64
}

func (s *slowEmitter) EmitAgentRequest(fromAgentID, sessionID, toAgentID string) {
	time.Sleep(s.delay)
	atomic.AddInt64(&s.count, 1)
}

func TestRouterSignals_SlowEmitter_DoesNotBlock(t *testing.T) {
	rs := newRouterSignals()
	emitter := &slowEmitter{delay: 10 * time.Millisecond}
	rs.SetSignalEmitter(emitter)

	start := time.Now()

	// Emit a signal
	rs.emitAgentRequest("from", "to")

	elapsed := time.Since(start)

	// Should complete (including the delay)
	if elapsed < 10*time.Millisecond {
		t.Error("expected some delay from slow emitter")
	}

	if atomic.LoadInt64(&emitter.count) != 1 {
		t.Error("expected 1 signal emitted")
	}
}
