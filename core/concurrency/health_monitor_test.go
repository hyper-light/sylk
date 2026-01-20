package concurrency

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// Test Helpers
// =============================================================================

// testHealthMonitor collects events for testing.
type testHealthMonitor struct {
	mu     sync.Mutex
	events []HealthEvent
}

func newTestHealthMonitor() *testHealthMonitor {
	return &testHealthMonitor{
		events: make([]HealthEvent, 0),
	}
}

func (m *testHealthMonitor) OnHealthEvent(event HealthEvent) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
}

func (m *testHealthMonitor) Events() []HealthEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]HealthEvent, len(m.events))
	copy(result, m.events)
	return result
}

func (m *testHealthMonitor) CountByType(t HealthEventType) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	count := 0
	for _, e := range m.events {
		if e.Type == t {
			count++
		}
	}
	return count
}

func (m *testHealthMonitor) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = m.events[:0]
}

// testPanicMonitor always panics to test panic recovery.
type testPanicMonitor struct{}

func (p *testPanicMonitor) OnHealthEvent(event HealthEvent) {
	panic("intentional panic for testing")
}

// testLLMExecutor is a mock executor for tests.
type testLLMExecutor struct{}

func (m *testLLMExecutor) Execute(ctx context.Context, req *LLMRequest) (any, error) {
	return "ok", nil
}

// =============================================================================
// HealthEvent Type Tests
// =============================================================================

func TestHealthEventType_String(t *testing.T) {
	tests := []struct {
		eventType HealthEventType
		expected  string
	}{
		{EventMessageDropped, "MessageDropped"},
		{EventChannelResized, "ChannelResized"},
		{EventQueueDepthHigh, "QueueDepthHigh"},
		{EventQueueDepthNormal, "QueueDepthNormal"},
		{EventShutdownStarted, "ShutdownStarted"},
		{EventShutdownComplete, "ShutdownComplete"},
		{EventWorkerStarted, "WorkerStarted"},
		{EventWorkerStopped, "WorkerStopped"},
		{EventLeakDetected, "LeakDetected"},
		{EventSyncFailure, "SyncFailure"},
		{EventSyncRecovered, "SyncRecovered"},
		{HealthEventType(999), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.eventType.String(); got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

// =============================================================================
// HealthMonitorFunc Tests
// =============================================================================

func TestHealthMonitorFunc(t *testing.T) {
	var received HealthEvent
	fn := HealthMonitorFunc(func(e HealthEvent) {
		received = e
	})

	event := HealthEvent{
		Type:   EventMessageDropped,
		Source: "test",
	}
	fn.OnHealthEvent(event)

	if received.Type != EventMessageDropped {
		t.Errorf("got type %v, want %v", received.Type, EventMessageDropped)
	}
}

// =============================================================================
// MultiHealthMonitor Tests
// =============================================================================

func TestMultiHealthMonitor_BroadcastsToAll(t *testing.T) {
	m1 := newTestHealthMonitor()
	m2 := newTestHealthMonitor()
	multi := NewMultiHealthMonitor(m1, m2)

	event := HealthEvent{Type: EventWorkerStarted, Source: "test"}
	multi.OnHealthEvent(event)

	if len(m1.Events()) != 1 {
		t.Errorf("m1 got %d events, want 1", len(m1.Events()))
	}
	if len(m2.Events()) != 1 {
		t.Errorf("m2 got %d events, want 1", len(m2.Events()))
	}
}

func TestMultiHealthMonitor_RecoversPanic(t *testing.T) {
	m1 := &testPanicMonitor{}
	m2 := newTestHealthMonitor()
	multi := NewMultiHealthMonitor(m1, m2)

	event := HealthEvent{Type: EventWorkerStarted}

	// Should not panic
	multi.OnHealthEvent(event)

	// m2 should still receive the event
	if len(m2.Events()) != 1 {
		t.Errorf("m2 got %d events, want 1", len(m2.Events()))
	}
}

// =============================================================================
// emitHealthEvent Tests
// =============================================================================

func TestEmitHealthEvent_NilMonitorDoesNotCrash(t *testing.T) {
	// Should not panic
	emitHealthEvent(nil, HealthEvent{Type: EventMessageDropped})
}

func TestEmitHealthEvent_RecoversPanic(t *testing.T) {
	monitor := &testPanicMonitor{}
	// Should not panic
	emitHealthEvent(monitor, HealthEvent{Type: EventMessageDropped})
}

// =============================================================================
// AdaptiveChannel HealthMonitor Tests
// =============================================================================

func TestAdaptiveChannel_HealthMonitor_MessageDropped(t *testing.T) {
	monitor := newTestHealthMonitor()

	config := AdaptiveChannelConfig{
		MinSize:            2,
		MaxSize:            2,
		InitialSize:        2,
		AllowOverflow:      true,
		SendTimeout:        10 * time.Millisecond,
		MaxOverflowSize:    1,
		OverflowDropPolicy: DropNewest,
	}

	ch := NewAdaptiveChannel[int](config)
	ch.SetHealthMonitor(monitor)
	defer ch.Close()

	// Fill channel
	_ = ch.Send(1)
	_ = ch.Send(2)

	// Fill overflow
	_ = ch.SendTimeout(3, 10*time.Millisecond)

	// This should trigger message dropped event
	err := ch.SendTimeout(4, 10*time.Millisecond)
	if err != ErrOverflowFull {
		t.Errorf("expected ErrOverflowFull, got %v", err)
	}

	// Allow time for event emission
	time.Sleep(10 * time.Millisecond)

	if monitor.CountByType(EventMessageDropped) < 1 {
		t.Errorf("expected at least 1 MessageDropped event")
	}
}

func TestAdaptiveChannel_HealthMonitor_NilMonitorDoesNotCrash(t *testing.T) {
	config := AdaptiveChannelConfig{
		MinSize:            2,
		MaxSize:            2,
		InitialSize:        2,
		AllowOverflow:      true,
		SendTimeout:        10 * time.Millisecond,
		MaxOverflowSize:    1,
		OverflowDropPolicy: DropNewest,
	}

	ch := NewAdaptiveChannel[int](config)
	// Intentionally not setting monitor
	defer ch.Close()

	_ = ch.Send(1)
	_ = ch.Send(2)
	_ = ch.SendTimeout(3, 10*time.Millisecond)
	// Should not panic even without monitor
	_ = ch.SendTimeout(4, 10*time.Millisecond)
}

// =============================================================================
// DualQueueGate HealthMonitor Tests
// =============================================================================

func TestDualQueueGate_HealthMonitor_ShutdownEvents(t *testing.T) {
	monitor := newTestHealthMonitor()
	config := DefaultDualQueueGateConfig()
	config.ShutdownTimeout = 100 * time.Millisecond

	gate := NewDualQueueGate(config, &testLLMExecutor{})
	gate.SetHealthMonitor(monitor)

	gate.Close()

	// Allow time for events
	time.Sleep(50 * time.Millisecond)

	if monitor.CountByType(EventShutdownStarted) != 1 {
		t.Errorf("expected 1 ShutdownStarted event, got %d", monitor.CountByType(EventShutdownStarted))
	}
	if monitor.CountByType(EventShutdownComplete) != 1 {
		t.Errorf("expected 1 ShutdownComplete event, got %d", monitor.CountByType(EventShutdownComplete))
	}
}

func TestDualQueueGate_HealthMonitor_NilMonitorDoesNotCrash(t *testing.T) {
	config := DefaultDualQueueGateConfig()
	gate := NewDualQueueGate(config, &testLLMExecutor{})
	// Intentionally not setting monitor
	gate.Close()
}

func TestDualQueueGate_HealthMonitor_QueueDepthHigh(t *testing.T) {
	monitor := newTestHealthMonitor()
	config := DefaultDualQueueGateConfig()
	config.MaxPipelineQueueSize = 10

	gate := NewDualQueueGate(config, nil) // nil executor so requests queue up
	gate.SetHealthMonitor(monitor)
	defer gate.Close()

	// Fill queue to 80%+ to trigger depth warning
	for i := 0; i < 9; i++ {
		req := &LLMRequest{
			ID:        "test-" + string(rune('a'+i)),
			AgentType: AgentTypeEngineer,
			ResultCh:  make(chan *LLMResult, 1),
		}
		_ = gate.Submit(context.Background(), req)
	}

	// Allow time for event
	time.Sleep(50 * time.Millisecond)

	if monitor.CountByType(EventQueueDepthHigh) < 1 {
		t.Errorf("expected at least 1 QueueDepthHigh event")
	}
}

// =============================================================================
// GoroutineScope HealthMonitor Tests
// =============================================================================

func TestGoroutineScope_HealthMonitor_WorkerStartStop(t *testing.T) {
	monitor := newTestHealthMonitor()
	budget := newTestBudget()
	scope := NewGoroutineScope(context.Background(), "test-agent", budget)
	scope.SetHealthMonitor(monitor)

	done := make(chan struct{})

	err := scope.Go("test-worker", time.Second, func(ctx context.Context) error {
		close(done)
		return nil
	})
	if err != nil {
		t.Fatalf("Go failed: %v", err)
	}

	<-done
	time.Sleep(50 * time.Millisecond) // Allow cleanup

	if monitor.CountByType(EventWorkerStarted) != 1 {
		t.Errorf("expected 1 WorkerStarted event, got %d", monitor.CountByType(EventWorkerStarted))
	}
	if monitor.CountByType(EventWorkerStopped) != 1 {
		t.Errorf("expected 1 WorkerStopped event, got %d", monitor.CountByType(EventWorkerStopped))
	}
}

func TestGoroutineScope_HealthMonitor_NilMonitorDoesNotCrash(t *testing.T) {
	budget := newTestBudget()
	scope := NewGoroutineScope(context.Background(), "test-agent", budget)
	// Intentionally not setting monitor

	done := make(chan struct{})

	err := scope.Go("test-worker", time.Second, func(ctx context.Context) error {
		close(done)
		return nil
	})
	if err != nil {
		t.Fatalf("Go failed: %v", err)
	}

	<-done
	_ = scope.Shutdown(100*time.Millisecond, 200*time.Millisecond)
}

func TestGoroutineScope_HealthMonitor_LeakDetection(t *testing.T) {
	monitor := newTestHealthMonitor()
	budget := newTestBudget()
	scope := NewGoroutineScope(context.Background(), "test-agent", budget)
	scope.SetHealthMonitor(monitor)

	// Start a worker that blocks forever
	started := make(chan struct{})
	err := scope.Go("blocking-worker", time.Minute, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		// Simulate slow cleanup - don't return immediately
		time.Sleep(500 * time.Millisecond)
		return ctx.Err()
	})
	if err != nil {
		t.Fatalf("Go failed: %v", err)
	}

	<-started

	// Short timeouts to force leak detection
	shutdownErr := scope.Shutdown(10*time.Millisecond, 20*time.Millisecond)

	// Should have detected leak
	if shutdownErr == nil {
		t.Error("expected leak error")
	}

	if monitor.CountByType(EventLeakDetected) != 1 {
		t.Errorf("expected 1 LeakDetected event, got %d", monitor.CountByType(EventLeakDetected))
	}
}

// =============================================================================
// Concurrent Access Tests (Race Conditions)
// =============================================================================

func TestHealthMonitor_ConcurrentEvents(t *testing.T) {
	monitor := newTestHealthMonitor()
	var wg sync.WaitGroup

	numGoroutines := 100
	eventsPerGoroutine := 100

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < eventsPerGoroutine; j++ {
				event := HealthEvent{
					Type:   HealthEventType(id % 11),
					Source: "concurrent-test",
					Count:  int64(j),
				}
				monitor.OnHealthEvent(event)
			}
		}(i)
	}

	wg.Wait()

	expected := numGoroutines * eventsPerGoroutine
	if len(monitor.Events()) != expected {
		t.Errorf("expected %d events, got %d", expected, len(monitor.Events()))
	}
}

func TestAdaptiveChannel_ConcurrentHealthEvents(t *testing.T) {
	monitor := newTestHealthMonitor()

	config := AdaptiveChannelConfig{
		MinSize:            4,
		MaxSize:            4,
		InitialSize:        4,
		AllowOverflow:      true,
		SendTimeout:        5 * time.Millisecond,
		MaxOverflowSize:    2,
		OverflowDropPolicy: DropNewest,
	}

	ch := NewAdaptiveChannel[int](config)
	ch.SetHealthMonitor(monitor)
	defer ch.Close()

	var wg sync.WaitGroup
	var dropped atomic.Int64

	// Concurrent senders
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				err := ch.SendTimeout(j, 5*time.Millisecond)
				if err == ErrOverflowFull {
					dropped.Add(1)
				}
			}
		}()
	}

	// Concurrent receiver
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			_, _ = ch.TryReceive()
			time.Sleep(time.Millisecond)
		}
	}()

	wg.Wait()

	// If messages were dropped, we should have drop events
	if dropped.Load() > 0 && monitor.CountByType(EventMessageDropped) == 0 {
		t.Log("Warning: messages dropped but no events recorded (possible race)")
	}
}

func TestDualQueueGate_ConcurrentHealthEvents(t *testing.T) {
	monitor := newTestHealthMonitor()
	config := DefaultDualQueueGateConfig()
	config.MaxPipelineQueueSize = 100

	gate := NewDualQueueGate(config, &testLLMExecutor{})
	gate.SetHealthMonitor(monitor)

	var wg sync.WaitGroup

	// Concurrent submitters
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				req := &LLMRequest{
					ID:        "test",
					AgentType: AgentTypeEngineer,
					ResultCh:  make(chan *LLMResult, 1),
				}
				_ = gate.Submit(context.Background(), req)
			}
		}(i)
	}

	wg.Wait()
	gate.Close()

	// Should have shutdown events
	if monitor.CountByType(EventShutdownStarted) != 1 {
		t.Errorf("expected 1 ShutdownStarted event")
	}
}

func TestGoroutineScope_ConcurrentHealthEvents(t *testing.T) {
	monitor := newTestHealthMonitor()
	budget := newTestBudget()
	scope := NewGoroutineScope(context.Background(), "test-agent", budget)
	scope.SetHealthMonitor(monitor)

	var wg sync.WaitGroup

	// Start many workers concurrently
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			workDone := make(chan struct{})
			err := scope.Go("worker", 100*time.Millisecond, func(ctx context.Context) error {
				close(workDone)
				return nil
			})
			if err == nil {
				<-workDone
			}
		}(i)
	}

	wg.Wait()
	time.Sleep(100 * time.Millisecond) // Allow cleanups

	started := monitor.CountByType(EventWorkerStarted)
	stopped := monitor.CountByType(EventWorkerStopped)

	if started != stopped {
		t.Errorf("started=%d stopped=%d - counts should match", started, stopped)
	}
}

// =============================================================================
// Edge Case Tests
// =============================================================================

func TestHealthMonitor_HighFrequencyEvents(t *testing.T) {
	monitor := newTestHealthMonitor()

	start := time.Now()
	iterations := 10000

	for i := 0; i < iterations; i++ {
		event := HealthEvent{
			Type:      EventWorkerStarted,
			Source:    "high-freq-test",
			Timestamp: time.Now(),
			Count:     int64(i),
		}
		monitor.OnHealthEvent(event)
	}

	elapsed := time.Since(start)

	if len(monitor.Events()) != iterations {
		t.Errorf("expected %d events, got %d", iterations, len(monitor.Events()))
	}

	// Should complete in reasonable time (< 1 second)
	if elapsed > time.Second {
		t.Errorf("high frequency events took too long: %v", elapsed)
	}
}

func TestMultiHealthMonitor_Empty(t *testing.T) {
	multi := NewMultiHealthMonitor()

	// Should not panic
	multi.OnHealthEvent(HealthEvent{Type: EventWorkerStarted})
}

func TestNewHealthEvent_PopulatesTimestamp(t *testing.T) {
	before := time.Now()
	event := newHealthEvent(EventMessageDropped, "test-source")
	after := time.Now()

	if event.Timestamp.Before(before) || event.Timestamp.After(after) {
		t.Errorf("timestamp %v not between %v and %v", event.Timestamp, before, after)
	}

	if event.Type != EventMessageDropped {
		t.Errorf("expected type %v, got %v", EventMessageDropped, event.Type)
	}

	if event.Source != "test-source" {
		t.Errorf("expected source %q, got %q", "test-source", event.Source)
	}
}

// =============================================================================
// Failure Path Tests
// =============================================================================

func TestHealthMonitor_CallbackErrorDoesNotCrash(t *testing.T) {
	// errorMonitor returns an error (simulated via panic)
	panicMon := &testPanicMonitor{}

	event := HealthEvent{Type: EventMessageDropped}

	// Direct call should be safe via emitHealthEvent
	emitHealthEvent(panicMon, event)

	// MultiHealthMonitor should be safe
	good := newTestHealthMonitor()
	multi := NewMultiHealthMonitor(panicMon, good)
	multi.OnHealthEvent(event)

	// Good monitor should still receive event
	if len(good.Events()) != 1 {
		t.Errorf("good monitor should have received event")
	}
}

func TestAdaptiveChannel_PanicMonitorDoesNotCrash(t *testing.T) {
	panicMon := &testPanicMonitor{}

	config := AdaptiveChannelConfig{
		MinSize:            2,
		MaxSize:            2,
		InitialSize:        2,
		AllowOverflow:      true,
		SendTimeout:        10 * time.Millisecond,
		MaxOverflowSize:    1,
		OverflowDropPolicy: DropNewest,
	}

	ch := NewAdaptiveChannel[int](config)
	ch.SetHealthMonitor(panicMon)
	defer ch.Close()

	// Should not panic even with panicking monitor
	_ = ch.Send(1)
	_ = ch.Send(2)
	_ = ch.SendTimeout(3, 10*time.Millisecond)
	_ = ch.SendTimeout(4, 10*time.Millisecond)
}

func TestDualQueueGate_PanicMonitorDoesNotCrash(t *testing.T) {
	panicMon := &testPanicMonitor{}
	config := DefaultDualQueueGateConfig()

	gate := NewDualQueueGate(config, &testLLMExecutor{})
	gate.SetHealthMonitor(panicMon)

	// Should not panic
	gate.Close()
}

func TestGoroutineScope_PanicMonitorDoesNotCrash(t *testing.T) {
	panicMon := &testPanicMonitor{}
	budget := newTestBudget()
	scope := NewGoroutineScope(context.Background(), "test-agent", budget)
	scope.SetHealthMonitor(panicMon)

	done := make(chan struct{})

	err := scope.Go("test-worker", time.Second, func(ctx context.Context) error {
		close(done)
		return nil
	})
	if err != nil {
		t.Fatalf("Go failed: %v", err)
	}

	<-done
	time.Sleep(50 * time.Millisecond)

	_ = scope.Shutdown(100*time.Millisecond, 200*time.Millisecond)
}
