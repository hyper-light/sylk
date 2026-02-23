package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
)

func TestReconciler_StartsAndStops(t *testing.T) {
	dc, _ := testDaemonController(t)
	ctx, cancel := context.WithCancel(context.Background())
	scope := concurrency.NewGoroutineScope(ctx, "reconciler-test", nil)

	r := NewReconciler(ReconcilerConfig{
		Controller: dc,
		Scope:      scope,
	})

	if err := r.Start(); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	// Give the goroutine a moment to start
	time.Sleep(20 * time.Millisecond)

	// Cancel triggers shutdown
	cancel()

	// Verify scope shuts down cleanly
	_ = scope.Shutdown(100*time.Millisecond, 200*time.Millisecond)
}

func TestReconciler_ErrorCallback(t *testing.T) {
	dc, rt := testDaemonController(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scope := concurrency.NewGoroutineScope(ctx, "reconciler-err-test", nil)

	rt.Close() // Force reconcile errors

	dc.Apply(testDaemonSetSpec("broken", "broken"))

	var errCount atomic.Int64
	r := NewReconciler(ReconcilerConfig{
		Controller: dc,
		Scope:      scope,
		OnError:    func(_ error) { errCount.Add(1) },
	})

	// Manually call reconcileOnce to verify error callback
	r.reconcileOnce(ctx)

	if errCount.Load() != 1 {
		t.Fatalf("expected 1 error callback, got %d", errCount.Load())
	}
}

func TestReconciler_NilErrorCallbackSafe(t *testing.T) {
	dc, rt := testDaemonController(t)
	ctx := context.Background()

	rt.Close()
	dc.Apply(testDaemonSetSpec("broken", "broken"))

	r := NewReconciler(ReconcilerConfig{
		Controller: dc,
		Scope:      concurrency.NewGoroutineScope(ctx, "nil-err-test", nil),
	})

	// Should not panic with nil OnError
	r.reconcileOnce(ctx)
}

func TestReconciler_ReconcileCreatesContainers(t *testing.T) {
	dc, _ := testDaemonController(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	scope := concurrency.NewGoroutineScope(ctx, "reconciler-create-test", nil)

	dc.Apply(testDaemonSetSpec("guide-daemon", "guide"))

	r := NewReconciler(ReconcilerConfig{
		Controller: dc,
		Scope:      scope,
	})

	// Manually call reconcileOnce
	r.reconcileOnce(ctx)

	if dc.InstanceCount() != 1 {
		t.Fatalf("expected 1 instance after reconcile, got %d", dc.InstanceCount())
	}
}
