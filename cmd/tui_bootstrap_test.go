package cmd

import (
	"context"
	"testing"

	"github.com/adalundhe/sylk/core/knowledge"
	"github.com/adalundhe/sylk/core/knowledge/query"
)

type testBackgroundWaiter struct {
	ready chan struct{}
}

func (w *testBackgroundWaiter) Ready() <-chan struct{} { return w.ready }
func (w *testBackgroundWaiter) Progress() (indexed, total int64) {
	return 0, 0
}
func (w *testBackgroundWaiter) OnProgress(func(indexed, total int64)) {}

func TestWaitForBootBleveReady_PromotesFull(t *testing.T) {
	store := knowledge.NewKnowledgeStore(nil, nil)
	waiter := &testBackgroundWaiter{ready: make(chan struct{})}
	store.PromotePartial(query.NewBleveSearcher(nil), waiter, nil)
	close(waiter.ready)

	if err := waitForBootBleveReady(context.Background(), store); err != nil {
		t.Fatalf("waitForBootBleveReady() error = %v", err)
	}

	if got := store.Level(); got != knowledge.ReadinessFull {
		t.Fatalf("knowledge level = %v, want %v", got, knowledge.ReadinessFull)
	}
}

func TestWaitForBootBleveReady_CanceledContextSkipsPromotion(t *testing.T) {
	store := knowledge.NewKnowledgeStore(nil, nil)
	waiter := &testBackgroundWaiter{ready: make(chan struct{})}
	store.PromotePartial(query.NewBleveSearcher(nil), waiter, nil)
	close(waiter.ready)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := waitForBootBleveReady(ctx, store); err != nil {
		t.Fatalf("waitForBootBleveReady() error = %v", err)
	}

	if got := store.Level(); got != knowledge.ReadinessPartial {
		t.Fatalf("knowledge level = %v, want %v", got, knowledge.ReadinessPartial)
	}
}
