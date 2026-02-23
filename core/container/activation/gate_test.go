package activation

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/container"
)

func TestActivationGate_SingleCaller(t *testing.T) {
	gate := NewActivationGate()

	called := atomic.Int32{}
	c, coalesced, err := gate.DoOrWait(context.Background(), "engineer", func(_ context.Context) (*container.Container, error) {
		called.Add(1)
		return &container.Container{}, nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c == nil {
		t.Fatal("expected non-nil container")
	}
	if coalesced {
		t.Fatal("first caller should not be coalesced")
	}
	if called.Load() != 1 {
		t.Fatalf("expected activate called once, got %d", called.Load())
	}
}

func TestActivationGate_ConcurrentCallersCoalesce(t *testing.T) {
	gate := NewActivationGate()

	activateStarted := make(chan struct{})
	activateRelease := make(chan struct{})
	called := atomic.Int32{}

	expected := &container.Container{}

	const numWaiters = 10
	var wg sync.WaitGroup
	wg.Add(numWaiters + 1)

	// First caller — holds the activation open.
	go func() {
		defer wg.Done()
		c, _, err := gate.DoOrWait(context.Background(), "engineer", func(_ context.Context) (*container.Container, error) {
			called.Add(1)
			close(activateStarted)
			<-activateRelease
			return expected, nil
		})
		if err != nil || c != expected {
			t.Errorf("first caller: err=%v, c=%v", err, c)
		}
	}()

	// Wait for activation to start.
	<-activateStarted

	// Subsequent callers — should coalesce.
	coalescedCount := atomic.Int32{}
	for range numWaiters {
		go func() {
			defer wg.Done()
			c, coalesced, err := gate.DoOrWait(context.Background(), "engineer", func(_ context.Context) (*container.Container, error) {
				called.Add(1)
				return &container.Container{}, nil
			})
			if err != nil {
				t.Errorf("waiter error: %v", err)
			}
			if c != expected {
				t.Error("waiter got different container")
			}
			if coalesced {
				coalescedCount.Add(1)
			}
		}()
	}

	// Let the activation complete.
	time.Sleep(10 * time.Millisecond) // ensure waiters are waiting
	close(activateRelease)
	wg.Wait()

	if called.Load() != 1 {
		t.Fatalf("expected activate called once, got %d", called.Load())
	}
	if coalescedCount.Load() != int32(numWaiters) {
		t.Fatalf("expected %d coalesced, got %d", numWaiters, coalescedCount.Load())
	}
}

func TestActivationGate_ContextCancellation(t *testing.T) {
	gate := NewActivationGate()
	activateStarted := make(chan struct{})

	go func() {
		_, _, _ = gate.DoOrWait(context.Background(), "engineer", func(_ context.Context) (*container.Container, error) {
			close(activateStarted)
			time.Sleep(time.Second)
			return &container.Container{}, nil
		})
	}()

	<-activateStarted

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	_, _, err := gate.DoOrWait(ctx, "engineer", func(_ context.Context) (*container.Container, error) {
		return &container.Container{}, nil
	})
	if err != context.DeadlineExceeded {
		t.Fatalf("expected DeadlineExceeded, got %v", err)
	}
}

func TestActivationGate_DifferentTypesIndependent(t *testing.T) {
	gate := NewActivationGate()

	var wg sync.WaitGroup
	wg.Add(2)

	results := make([]*container.Container, 2)
	go func() {
		defer wg.Done()
		c, _, _ := gate.DoOrWait(context.Background(), "engineer", func(_ context.Context) (*container.Container, error) {
			return &container.Container{}, nil
		})
		results[0] = c
	}()
	go func() {
		defer wg.Done()
		c, _, _ := gate.DoOrWait(context.Background(), "architect", func(_ context.Context) (*container.Container, error) {
			return &container.Container{}, nil
		})
		results[1] = c
	}()
	wg.Wait()

	if results[0] == nil || results[1] == nil {
		t.Fatal("expected both containers to be non-nil")
	}
	if results[0] == results[1] {
		t.Fatal("different types should produce different containers")
	}
}
