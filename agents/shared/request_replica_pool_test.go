package shared

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/providers/gateway"
)

type testGrantResult struct {
	label string
	lease *RequestReplicaLease
	err   error
}

type fakeGatewayProvider struct{}

func (fakeGatewayProvider) GatewayMetrics() gateway.MetricsSnapshot {
	return gateway.MetricsSnapshot{
		Inflight:    3,
		Queued:      2,
		RateLimited: 5,
		Errors429:   7,
	}
}

func (fakeGatewayProvider) GatewayCapacity() gateway.CapacitySnapshot {
	return gateway.CapacitySnapshot{
		Name:                  "anthropic",
		MaxConcurrentRequests: 6,
		MaxQueueSize:          12,
		MaxWaitTime:           45 * time.Second,
	}
}

func TestRequestReplicaPoolGrantsQueuedWorkWhenPressureRelaxes(t *testing.T) {
	now := time.Unix(1700000000, 0)
	memoryPressure := 0.90

	pool := NewAutoscalingRequestReplicaPool(RequestReplicaPoolConfig{
		Name: "librarian",
		Now: func() time.Time {
			return now
		},
		HostTelemetry: func() RequestReplicaHostSnapshot {
			return RequestReplicaHostSnapshot{
				GOMAXPROCS:     1,
				NumGoroutine:   32,
				MemoryPressure: memoryPressure,
			}
		},
		HardMaxQueued: 8,
	})
	defer pool.Close()

	lease1, err := pool.Acquire(context.Background(), RequestReplicaAcquireOptions{
		SourceKey: "pipeline-a",
	})
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer lease1.Release()

	queued := make(chan int, 1)
	granted := make(chan *RequestReplicaLease, 1)
	acquireErr := make(chan error, 1)
	go func() {
		lease, err := pool.Acquire(context.Background(), RequestReplicaAcquireOptions{
			SourceKey: "pipeline-b",
			OnQueued: func(_ RequestReplicaPoolSnapshot, queuePosition int) {
				queued <- queuePosition
			},
		})
		if err != nil {
			acquireErr <- err
			return
		}
		granted <- lease
	}()

	select {
	case position := <-queued:
		if position != 1 {
			t.Fatalf("queue position = %d, want 1", position)
		}
	case err := <-acquireErr:
		t.Fatalf("second acquire errored before queueing: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queued acquire")
	}

	snapshot := pool.Snapshot()
	if snapshot.Queued != 1 {
		t.Fatalf("queued = %d, want 1", snapshot.Queued)
	}
	if snapshot.Desired < 2 {
		t.Fatalf("desired = %d, want at least 2 under queue pressure", snapshot.Desired)
	}

	now = now.Add(12 * time.Second)
	memoryPressure = 0.20
	pool.reconcile()

	var lease2 *RequestReplicaLease
	select {
	case lease2 = <-granted:
	case err := <-acquireErr:
		t.Fatalf("second acquire failed after reconcile: %v", err)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queued work to be granted")
	}
	defer lease2.Release()

	snapshot = pool.Snapshot()
	if snapshot.Active != 2 {
		t.Fatalf("active = %d, want 2 after queued grant", snapshot.Active)
	}
}

func TestRequestReplicaPoolFairlyAlternatesQueuedSources(t *testing.T) {
	pool := NewAutoscalingRequestReplicaPool(RequestReplicaPoolConfig{
		Name:          "academic",
		HardMaxActive: 1,
		HardMaxQueued: 8,
	})
	defer pool.Close()

	rootLease, err := pool.Acquire(context.Background(), RequestReplicaAcquireOptions{
		SourceKey: "seed",
	})
	if err != nil {
		t.Fatalf("seed acquire: %v", err)
	}

	queueReady := make(chan string, 3)
	granted := make(chan testGrantResult, 3)
	enqueue := func(label, source string) {
		go func() {
			lease, err := pool.Acquire(context.Background(), RequestReplicaAcquireOptions{
				SourceKey: source,
				OnQueued: func(_ RequestReplicaPoolSnapshot, _ int) {
					queueReady <- label
				},
			})
			granted <- testGrantResult{label: label, lease: lease, err: err}
		}()
	}

	enqueueAndWait := func(label, source string) {
		enqueue(label, source)
		select {
		case got := <-queueReady:
			if got != label {
				t.Fatalf("queued label = %s, want %s", got, label)
			}
		case <-time.After(time.Second):
			t.Fatalf("timed out waiting for %s to queue", label)
		}
	}

	enqueueAndWait("A1", "pipeline-a")
	enqueueAndWait("B1", "pipeline-b")
	enqueueAndWait("A2", "pipeline-a")

	rootLease.Release()

	first := waitReplicaGrant(t, granted)
	if first.label != "A1" {
		t.Fatalf("first granted = %s, want A1", first.label)
	}
	first.lease.Release()

	second := waitReplicaGrant(t, granted)
	if second.label != "B1" {
		t.Fatalf("second granted = %s, want B1", second.label)
	}
	second.lease.Release()

	third := waitReplicaGrant(t, granted)
	if third.label != "A2" {
		t.Fatalf("third granted = %s, want A2", third.label)
	}
	third.lease.Release()
}

func TestRequestReplicaPoolBusyErrorIncludesRetryAfter(t *testing.T) {
	pool := NewAutoscalingRequestReplicaPool(RequestReplicaPoolConfig{
		Name:          "archivalist",
		HardMaxActive: 1,
		HardMaxQueued: 1,
	})
	defer pool.Close()

	lease, err := pool.Acquire(context.Background(), RequestReplicaAcquireOptions{
		SourceKey: "pipeline-a",
	})
	if err != nil {
		t.Fatalf("seed acquire: %v", err)
	}
	defer lease.Release()

	waitCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queued := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		queuedLease, err := pool.Acquire(waitCtx, RequestReplicaAcquireOptions{
			SourceKey: "pipeline-b",
			OnQueued: func(_ RequestReplicaPoolSnapshot, _ int) {
				queued <- struct{}{}
			},
		})
		if queuedLease != nil {
			queuedLease.Release()
		}
		done <- err
	}()

	select {
	case <-queued:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queued acquire")
	}

	_, err = pool.Acquire(context.Background(), RequestReplicaAcquireOptions{
		SourceKey: "pipeline-c",
	})
	var busyErr *AgentBusyError
	if !errors.As(err, &busyErr) {
		t.Fatalf("busy error = %v, want *AgentBusyError", err)
	}
	if busyErr.RetryAfter <= 0 {
		t.Fatalf("retry after = %s, want > 0", busyErr.RetryAfter)
	}
	if busyErr.Snapshot.EstimatedRetryAfter <= 0 {
		t.Fatalf("snapshot retry after = %s, want > 0", busyErr.Snapshot.EstimatedRetryAfter)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for queued acquire cancellation")
	}
}

func TestRequestReplicaProviderSnapshotFromProvider(t *testing.T) {
	snapshot := RequestReplicaProviderSnapshotFromProvider(fakeGatewayProvider{})
	if snapshot.Name != "anthropic" {
		t.Fatalf("name = %q, want anthropic", snapshot.Name)
	}
	if snapshot.Inflight != 3 || snapshot.MaxInflight != 6 {
		t.Fatalf("inflight/max = %d/%d, want 3/6", snapshot.Inflight, snapshot.MaxInflight)
	}
	if snapshot.Queued != 2 || snapshot.MaxQueued != 12 {
		t.Fatalf("queued/max = %d/%d, want 2/12", snapshot.Queued, snapshot.MaxQueued)
	}
	if snapshot.RateLimited != 5 || snapshot.Errors429 != 7 {
		t.Fatalf("rate_limited/errors429 = %d/%d, want 5/7", snapshot.RateLimited, snapshot.Errors429)
	}
	if snapshot.MaxWait != 45*time.Second {
		t.Fatalf("max wait = %s, want 45s", snapshot.MaxWait)
	}
}

func waitReplicaGrant(t *testing.T, granted <-chan testGrantResult) testGrantResult {
	t.Helper()
	select {
	case result := <-granted:
		if result.err != nil {
			t.Fatalf("grant returned error: %v", result.err)
		}
		if result.lease == nil {
			t.Fatal("grant returned nil lease")
		}
		return result
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for granted lease")
		return testGrantResult{}
	}
}
