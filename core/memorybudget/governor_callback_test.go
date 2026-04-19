package memorybudget

import (
	"sync"
	"sync/atomic"
	"testing"
)

// TestGovernor_RegisterEvictionCallback_FiresOnLimitExceeded is the core
// MEM-06 invariant: when a reservation would exceed a scope limit, every
// registered eviction callback fires with the scope and requested bytes.
func TestGovernor_RegisterEvictionCallback_FiresOnLimitExceeded(t *testing.T) {
	g := NewGovernor(1024, 1024, 1024, 1024)

	var invoked atomic.Int32
	var seenScope Scope
	var seenRequested int64
	g.RegisterEvictionCallback(func(scope Scope, requested int64) int64 {
		invoked.Add(1)
		seenScope = scope
		seenRequested = requested
		return 0 // didn't free anything
	})

	// Pre-fill the overlay scope to its limit.
	blocker, err := g.Reserve(ScopeOverlay, 1024)
	if err != nil {
		t.Fatalf("pre-fill Reserve: %v", err)
	}
	defer blocker.Release()

	// Now attempt to reserve more — should fire the callback and fail.
	_, err = g.Reserve(ScopeOverlay, 100)
	if err != ErrMemoryLimitExceeded {
		t.Fatalf("expected ErrMemoryLimitExceeded after callback returned 0, got %v", err)
	}
	if invoked.Load() != 1 {
		t.Errorf("callback fired %d times, want 1", invoked.Load())
	}
	if seenScope != ScopeOverlay {
		t.Errorf("callback saw scope %q, want %q", seenScope, ScopeOverlay)
	}
	if seenRequested != 100 {
		t.Errorf("callback requested = %d, want 100", seenRequested)
	}
}

// TestGovernor_EvictionCallbackSucceeds verifies that a callback which
// actually frees enough bytes lets the reservation proceed.
func TestGovernor_EvictionCallbackSucceeds(t *testing.T) {
	g := NewGovernor(1024, 1024, 1024, 1024)

	// Hold a reservation that can release bytes on eviction.
	blocker, _ := g.Reserve(ScopeOverlay, 1024)

	g.RegisterEvictionCallback(func(_ Scope, requested int64) int64 {
		// Free 200 bytes by shrinking the blocker.
		if err := blocker.Resize(824); err != nil {
			return 0
		}
		return 200
	})

	reservation, err := g.Reserve(ScopeOverlay, 100)
	if err != nil {
		t.Fatalf("Reserve after eviction: %v", err)
	}
	if reservation.bytes != 100 {
		t.Errorf("reservation bytes = %d, want 100", reservation.bytes)
	}
}

// TestGovernor_MultipleCallbacksInRegistrationOrder verifies ordering —
// callbacks fire in the order they were registered, and the Governor
// stops once enough bytes are released.
func TestGovernor_MultipleCallbacksInRegistrationOrder(t *testing.T) {
	g := NewGovernor(1024, 1024, 1024, 1024)
	blocker, _ := g.Reserve(ScopeOverlay, 1024)

	var order []string
	var mu sync.Mutex

	g.RegisterEvictionCallback(func(_ Scope, _ int64) int64 {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, "first")
		_ = blocker.Resize(924) // frees 100
		return 100
	})
	g.RegisterEvictionCallback(func(_ Scope, _ int64) int64 {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, "second")
		return 0
	})

	// Needs only 50 bytes → first callback should free 100 → second
	// should NOT fire.
	_, err := g.Reserve(ScopeOverlay, 50)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 1 || order[0] != "first" {
		t.Errorf("callback order = %v, want [first] (second should not fire after first freed enough)", order)
	}
}

// TestGovernor_CallbackChainFreesEnough verifies that multiple callbacks
// contribute in succession when the first one is insufficient.
func TestGovernor_CallbackChainFreesEnough(t *testing.T) {
	g := NewGovernor(1024, 1024, 1024, 1024)
	blockerA, _ := g.Reserve(ScopeOverlay, 512)
	blockerB, _ := g.Reserve(ScopeOverlay, 512)

	g.RegisterEvictionCallback(func(_ Scope, _ int64) int64 {
		_ = blockerA.Resize(462) // frees 50
		return 50
	})
	g.RegisterEvictionCallback(func(_ Scope, _ int64) int64 {
		_ = blockerB.Resize(462) // frees 50
		return 50
	})

	// Need 100 bytes → requires both callbacks to fire.
	_, err := g.Reserve(ScopeOverlay, 100)
	if err != nil {
		t.Fatalf("Reserve after chain: %v", err)
	}
}

// TestGovernor_NilCallbackIgnored verifies registering a nil callback is
// a no-op.
func TestGovernor_NilCallbackIgnored(t *testing.T) {
	g := NewGovernor(1024, 1024, 1024, 1024)
	g.RegisterEvictionCallback(nil)
	if g.CallbackCount() != 0 {
		t.Error("nil callback must not be registered")
	}
}

// TestGovernor_ClearEvictionCallbacks verifies Clear drops all registered
// callbacks so the next reservation hits ErrMemoryLimitExceeded without
// invoking anything.
func TestGovernor_ClearEvictionCallbacks(t *testing.T) {
	g := NewGovernor(1024, 1024, 1024, 1024)
	blocker, _ := g.Reserve(ScopeOverlay, 1024)
	defer blocker.Release()

	var fires atomic.Int32
	g.RegisterEvictionCallback(func(_ Scope, _ int64) int64 {
		fires.Add(1)
		return 0
	})
	g.ClearEvictionCallbacks()

	_, _ = g.Reserve(ScopeOverlay, 10) // should fail; callback cleared
	if fires.Load() != 0 {
		t.Errorf("callbacks fired %d times after Clear, want 0", fires.Load())
	}
	if g.CallbackCount() != 0 {
		t.Error("CallbackCount nonzero after Clear")
	}
}

// TestGovernor_CallbacksDoNotDeadlockOnMu verifies that a callback
// which itself calls Reserve or Resize on the same Governor does not
// deadlock. This is the key correctness invariant that makes the "drop
// the lock before firing callbacks" pattern work.
func TestGovernor_CallbacksDoNotDeadlockOnMu(t *testing.T) {
	g := NewGovernor(1024, 1024, 1024, 1024)
	blocker, _ := g.Reserve(ScopeOverlay, 1024)

	g.RegisterEvictionCallback(func(_ Scope, requested int64) int64 {
		// The callback mutates g.used via Resize — this would deadlock
		// if the Governor still held g.mu.
		if err := blocker.Resize(1024 - requested); err != nil {
			return 0
		}
		return requested
	})

	r, err := g.Reserve(ScopeOverlay, 200)
	if err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	r.Release()
}
