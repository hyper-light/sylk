package knowledge

import (
	"context"
	"sync"
	"testing"
	"time"
)

type testBackgroundWaiter struct {
	ready chan struct{}
}

func (w *testBackgroundWaiter) Ready() <-chan struct{} { return w.ready }
func (w *testBackgroundWaiter) Progress() (indexed, total int64) {
	return 0, 0
}
func (w *testBackgroundWaiter) OnProgress(func(indexed, total int64)) {}

// mockReadinessPublisher collects published events for test assertions.
type mockReadinessPublisher struct {
	mu     sync.Mutex
	events []ReadinessEvent
}

func (p *mockReadinessPublisher) PublishKnowledgeReady(event ReadinessEvent) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.events = append(p.events, event)
}

func (p *mockReadinessPublisher) collected() []ReadinessEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := make([]ReadinessEvent, len(p.events))
	copy(cp, p.events)
	return cp
}

func TestNewKnowledgeStore(t *testing.T) {
	pub := &mockReadinessPublisher{}
	ks := NewKnowledgeStore(pub, nil)
	defer ks.Close()

	if ks.Coordinator() == nil {
		t.Fatal("Coordinator() returned nil")
	}

	if ks.Level() != ReadinessNone {
		t.Errorf("Level() = %d, want %d", ks.Level(), ReadinessNone)
	}

	if ks.BackgroundWaiter() != nil {
		t.Error("BackgroundWaiter() should be nil before promotion")
	}
}

func TestKnowledgeStore_NilPublisher(t *testing.T) {
	ks := NewKnowledgeStore(nil, nil)
	defer ks.Close()

	// PromoteFull with nil publisher must not panic.
	ks.PromoteFull()

	if ks.Level() != ReadinessFull {
		t.Errorf("Level() = %d, want %d", ks.Level(), ReadinessFull)
	}
}

func TestKnowledgeStore_PromoteFull(t *testing.T) {
	pub := &mockReadinessPublisher{}
	ks := NewKnowledgeStore(pub, nil)
	defer ks.Close()

	ks.PromoteFull()

	if ks.Level() != ReadinessFull {
		t.Errorf("Level() = %d, want %d", ks.Level(), ReadinessFull)
	}

	events := pub.collected()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Level != ReadinessFull {
		t.Errorf("event level = %d, want %d", events[0].Level, ReadinessFull)
	}
}

func TestKnowledgeStore_WaitForPartial_AlreadyClosed(t *testing.T) {
	ks := NewKnowledgeStore(nil, nil)
	defer ks.Close()

	// Close the partialReady channel manually to simulate promotion.
	close(ks.partialReady)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if err := ks.WaitForPartial(ctx); err != nil {
		t.Errorf("WaitForPartial() returned error: %v", err)
	}
}

func TestKnowledgeStore_WaitForPartial_Cancelled(t *testing.T) {
	ks := NewKnowledgeStore(nil, nil)
	defer ks.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	if err := ks.WaitForPartial(ctx); err == nil {
		t.Error("WaitForPartial() with cancelled ctx should return error")
	}
}

func TestKnowledgeStore_WaitForFull(t *testing.T) {
	ks := NewKnowledgeStore(nil, nil)
	defer ks.Close()

	done := make(chan error, 1)
	go func() {
		done <- ks.WaitForFull(context.Background())
	}()

	// Give goroutine time to block.
	time.Sleep(10 * time.Millisecond)

	ks.PromoteFull()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("WaitForFull() returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("WaitForFull() did not unblock after PromoteFull()")
	}
}

func TestKnowledgeStore_DoubleClose(t *testing.T) {
	ks := NewKnowledgeStore(nil, nil)

	if err := ks.Close(); err != nil {
		t.Errorf("first Close() returned error: %v", err)
	}
	if err := ks.Close(); err != nil {
		t.Errorf("second Close() returned error: %v", err)
	}
}

func TestKnowledgeStore_CoordinatorSurvivesPromotion(t *testing.T) {
	ks := NewKnowledgeStore(nil, nil)
	defer ks.Close()

	coordBefore := ks.Coordinator()
	ks.PromoteFull()
	coordAfter := ks.Coordinator()

	if coordBefore != coordAfter {
		t.Error("coordinator instance changed after PromoteFull — expected same pointer")
	}
}

func TestKnowledgeStore_RepeatedPromotionsAreIdempotent(t *testing.T) {
	pub := &mockReadinessPublisher{}
	ks := NewKnowledgeStore(pub, nil)
	defer ks.Close()

	ks.PromotePartial(nil, nil, nil)
	ks.PromotePartial(nil, nil, nil)
	ks.PromoteFull()
	ks.PromotePartial(nil, nil, nil)
	ks.PromoteFull()

	if got := ks.Level(); got != ReadinessFull {
		t.Fatalf("Level() = %d, want %d", got, ReadinessFull)
	}

	events := pub.collected()
	if len(events) != 2 {
		t.Fatalf("event count = %d, want 2", len(events))
	}
	if events[0].Level != ReadinessPartial {
		t.Fatalf("first event level = %d, want %d", events[0].Level, ReadinessPartial)
	}
	if events[1].Level != ReadinessFull {
		t.Fatalf("second event level = %d, want %d", events[1].Level, ReadinessFull)
	}
}

func TestKnowledgeStore_LifecycleObserverReceivesRepeatedPartialCycles(t *testing.T) {
	ks := NewKnowledgeStore(nil, nil)
	defer ks.Close()

	waiter1 := &testBackgroundWaiter{ready: make(chan struct{})}
	waiter2 := &testBackgroundWaiter{ready: make(chan struct{})}

	var (
		mu     sync.Mutex
		events []LifecycleEvent
	)
	ks.SetLifecycleObserver(func(event LifecycleEvent) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	})

	ks.PromotePartial(nil, waiter1, nil)
	ks.PromoteFull()
	ks.PromotePartial(nil, waiter2, nil)

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 3 {
		t.Fatalf("lifecycle event count = %d, want 3", len(events))
	}
	if events[0].Kind != LifecycleEventPartial || events[0].Waiter != waiter1 {
		t.Fatalf("first lifecycle event = %#v, want partial waiter1", events[0])
	}
	if events[1].Kind != LifecycleEventFull {
		t.Fatalf("second lifecycle event = %#v, want full", events[1])
	}
	if events[2].Kind != LifecycleEventPartial || events[2].Waiter != waiter2 {
		t.Fatalf("third lifecycle event = %#v, want partial waiter2", events[2])
	}
}
