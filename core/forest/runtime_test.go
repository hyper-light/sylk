package forest

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRuntimeTracksWorkersAndClose(t *testing.T) {
	rt := newRuntime(context.Background(), nil)
	done := make(chan struct{})
	if err := rt.StartWorker("one", 1, func(ctx context.Context) error {
		close(done)
		<-ctx.Done()
		return nil
	}); err != nil {
		t.Fatalf("start worker: %v", err)
	}
	<-done
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rt.Close(ctx); err != nil {
		t.Fatalf("close runtime: %v", err)
	}
	snap := rt.Snapshot()
	if !snap.Closed || len(snap.Workers) != 1 || snap.Workers[0].Status != RuntimeWorkerStopped {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestRuntimeRejectsInvalidWorkers(t *testing.T) {
	rt := newRuntime(context.Background(), nil)
	if err := rt.StartWorker("", 1, func(context.Context) error { return nil }); err == nil {
		t.Fatal("empty worker name accepted")
	}
	if err := rt.StartWorker("bad-limit", -1, func(context.Context) error { return nil }); err == nil {
		t.Fatal("negative queue limit accepted")
	}
	if err := rt.StartWorker("zero-limit", 0, func(context.Context) error { return nil }); err == nil {
		t.Fatal("zero queue limit accepted")
	}
	if err := rt.StartWorker("nil", 1, nil); err == nil {
		t.Fatal("nil worker accepted")
	}
}

func TestRuntimeRecordsWorkerErrorAndPanic(t *testing.T) {
	rt := newRuntime(context.Background(), nil)
	if err := rt.StartWorker("err", 1, func(context.Context) error {
		return errors.New("boom")
	}); err != nil {
		t.Fatalf("start err worker: %v", err)
	}
	if err := rt.StartWorker("panic", 1, func(context.Context) error {
		panic("bad")
	}); err != nil {
		t.Fatalf("start panic worker: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rt.Close(ctx); err != nil {
		t.Fatalf("close runtime: %v", err)
	}
	snap := rt.Snapshot()
	statuses := map[string]RuntimeWorkerStatus{}
	for _, worker := range snap.Workers {
		statuses[worker.Name] = worker.Status
		if worker.LastError == "" {
			t.Fatalf("worker %s missing error: %+v", worker.Name, worker)
		}
	}
	if statuses["err"] != RuntimeWorkerErrored || statuses["panic"] != RuntimeWorkerPanicked {
		t.Fatalf("statuses = %+v", statuses)
	}
}

func TestRuntimeEdgeLifecycleAndRegistries(t *testing.T) {
	rt := newRuntime(context.Background(), nil)
	if err := rt.RegisterQueue("q", 1); err != nil {
		t.Fatalf("register queue: %v", err)
	}
	if err := rt.RegisterQueue("zero", 0); err == nil {
		t.Fatal("zero-capacity queue accepted")
	}
	if err := rt.RegisterTicker("tick", time.Second); err != nil {
		t.Fatalf("register ticker: %v", err)
	}
	if err := rt.RegisterLease("lease", "holder"); err != nil {
		t.Fatalf("register lease: %v", err)
	}
	rt.RecordQueueOverflow("q", "full")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := rt.Close(ctx); err != nil {
		t.Fatalf("close before start: %v", err)
	}
	if err := rt.Close(ctx); err != nil {
		t.Fatalf("close twice: %v", err)
	}
	if err := rt.StartWorker("late", 1, func(context.Context) error { return nil }); err == nil {
		t.Fatal("start after close accepted")
	}
	snap := rt.Snapshot()
	if len(snap.Queues) != 1 || snap.Queues[0].Overflows != 1 {
		t.Fatalf("queue snapshot = %+v", snap.Queues)
	}
	if len(snap.Tickers) != 1 || len(snap.Leases) != 1 {
		t.Fatalf("snapshot registries = %+v", snap)
	}
}

func TestRuntimeCloseTimeoutIsVisible(t *testing.T) {
	rt := newRuntime(context.Background(), nil)
	block := make(chan struct{})
	if err := rt.StartWorker("blocked", 1, func(ctx context.Context) error {
		<-block
		return ctx.Err()
	}); err != nil {
		t.Fatalf("start worker: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	if err := rt.Close(ctx); err == nil {
		t.Fatal("close timeout returned nil")
	}
	close(block)
	snap := rt.Snapshot()
	if snap.TimeoutError == "" || snap.TimeoutAt.IsZero() {
		t.Fatalf("timeout not visible in snapshot: %+v", snap)
	}
}
