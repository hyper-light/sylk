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
