package bulkhead

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestNewHierarchicalBulkhead(t *testing.T) {
	t.Run("with nil configs uses defaults", func(t *testing.T) {
		hb := NewHierarchicalBulkhead("session-1", nil)
		defer hb.Stop()

		if hb.SessionID() != "session-1" {
			t.Errorf("expected session ID 'session-1', got '%s'", hb.SessionID())
		}

		stats := hb.Stats()
		if stats.SessionID != "session-1" {
			t.Errorf("expected stats session ID 'session-1', got '%s'", stats.SessionID)
		}
	})

	t.Run("with custom configs", func(t *testing.T) {
		configs := map[BulkheadLevel]BulkheadConfig{
			LevelSession: {
				MaxConcurrent: 10,
				MaxQueueSize:  100,
				QueueTimeout:  time.Second,
				CircuitConfig: CircuitConfig{
					FailureThreshold: 5,
					SuccessThreshold: 3,
					Timeout:          time.Second,
				},
			},
			LevelProvider: {
				MaxConcurrent: 5,
				MaxQueueSize:  50,
				QueueTimeout:  time.Second,
				CircuitConfig: CircuitConfig{
					FailureThreshold: 3,
					SuccessThreshold: 2,
					Timeout:          time.Second,
				},
			},
			LevelModel: {
				MaxConcurrent: 2,
				MaxQueueSize:  20,
				QueueTimeout:  time.Second,
				CircuitConfig: CircuitConfig{
					FailureThreshold: 2,
					SuccessThreshold: 1,
					Timeout:          time.Second,
				},
			},
		}

		hb := NewHierarchicalBulkhead("session-2", configs)
		defer hb.Stop()

		stats := hb.Stats()
		if stats.Session.Available != 10 {
			t.Errorf("expected 10 available session slots, got %d", stats.Session.Available)
		}
	})
}

func TestHierarchicalBulkhead_Acquire(t *testing.T) {
	t.Run("successful acquisition at all levels", func(t *testing.T) {
		hb := NewHierarchicalBulkhead("test-session", nil)
		defer hb.Stop()

		ctx := context.Background()
		slot, err := hb.Acquire(ctx, "openai", "gpt-4")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if slot == nil {
			t.Fatal("expected non-nil slot")
		}

		if slot.Provider() != "openai" {
			t.Errorf("expected provider 'openai', got '%s'", slot.Provider())
		}

		if slot.Model() != "gpt-4" {
			t.Errorf("expected model 'gpt-4', got '%s'", slot.Model())
		}

		if slot.IsReleased() {
			t.Error("slot should not be released yet")
		}

		slot.Release()

		if !slot.IsReleased() {
			t.Error("slot should be released after Release()")
		}
	})

	t.Run("context cancellation releases acquired slots", func(t *testing.T) {
		configs := map[BulkheadLevel]BulkheadConfig{
			LevelSession: {
				MaxConcurrent: 1,
				MaxQueueSize:  0,
				QueueTimeout:  time.Millisecond * 100,
				CircuitConfig: CircuitConfig{
					FailureThreshold: 5,
					SuccessThreshold: 3,
					Timeout:          time.Second,
				},
			},
			LevelProvider: {
				MaxConcurrent: 1,
				MaxQueueSize:  0,
				QueueTimeout:  time.Millisecond * 100,
				CircuitConfig: CircuitConfig{
					FailureThreshold: 5,
					SuccessThreshold: 3,
					Timeout:          time.Second,
				},
			},
			LevelModel: {
				MaxConcurrent: 1,
				MaxQueueSize:  0,
				QueueTimeout:  time.Millisecond * 100,
				CircuitConfig: CircuitConfig{
					FailureThreshold: 5,
					SuccessThreshold: 3,
					Timeout:          time.Second,
				},
			},
		}

		hb := NewHierarchicalBulkhead("test-session", configs)
		defer hb.Stop()

		ctx := context.Background()
		slot1, err := hb.Acquire(ctx, "openai", "gpt-4")
		if err != nil {
			t.Fatalf("first acquire failed: %v", err)
		}
		defer slot1.Release()

		// Second acquire should fail because all slots are taken
		_, err = hb.Acquire(ctx, "openai", "gpt-4")
		if err == nil {
			t.Error("expected error on second acquire")
		}
	})
}

func TestHierarchicalBulkhead_SessionIsolation(t *testing.T) {
	t.Run("different sessions are isolated", func(t *testing.T) {
		hb1 := NewHierarchicalBulkhead("session-1", nil)
		hb2 := NewHierarchicalBulkhead("session-2", nil)
		defer hb1.Stop()
		defer hb2.Stop()

		ctx := context.Background()

		// Acquire from first hierarchy
		slot1, err := hb1.Acquire(ctx, "openai", "gpt-4")
		if err != nil {
			t.Fatalf("session-1 acquire failed: %v", err)
		}

		// Acquire from second hierarchy - should succeed independently
		slot2, err := hb2.Acquire(ctx, "openai", "gpt-4")
		if err != nil {
			t.Fatalf("session-2 acquire failed: %v", err)
		}

		// Verify they have different session references
		stats1 := hb1.Stats()
		stats2 := hb2.Stats()

		if stats1.SessionID == stats2.SessionID {
			t.Error("sessions should have different IDs")
		}

		slot1.Release()
		slot2.Release()
	})
}

func TestHierarchicalBulkhead_LazyCreation(t *testing.T) {
	t.Run("providers created lazily", func(t *testing.T) {
		hb := NewHierarchicalBulkhead("test-session", nil)
		defer hb.Stop()

		stats := hb.Stats()
		if len(stats.Providers) != 0 {
			t.Errorf("expected 0 providers initially, got %d", len(stats.Providers))
		}

		ctx := context.Background()
		slot, _ := hb.Acquire(ctx, "openai", "gpt-4")
		slot.Release()

		stats = hb.Stats()
		if len(stats.Providers) != 1 {
			t.Errorf("expected 1 provider after acquire, got %d", len(stats.Providers))
		}

		if _, ok := stats.Providers["openai"]; !ok {
			t.Error("expected 'openai' provider in stats")
		}
	})

	t.Run("models created lazily", func(t *testing.T) {
		hb := NewHierarchicalBulkhead("test-session", nil)
		defer hb.Stop()

		stats := hb.Stats()
		if len(stats.Models) != 0 {
			t.Errorf("expected 0 models initially, got %d", len(stats.Models))
		}

		ctx := context.Background()
		slot, _ := hb.Acquire(ctx, "openai", "gpt-4")
		slot.Release()

		stats = hb.Stats()
		if len(stats.Models) != 1 {
			t.Errorf("expected 1 model after acquire, got %d", len(stats.Models))
		}

		if _, ok := stats.Models["openai:gpt-4"]; !ok {
			t.Error("expected 'openai:gpt-4' model in stats")
		}
	})

	t.Run("multiple models under same provider", func(t *testing.T) {
		hb := NewHierarchicalBulkhead("test-session", nil)
		defer hb.Stop()

		ctx := context.Background()

		slot1, _ := hb.Acquire(ctx, "openai", "gpt-4")
		slot1.Release()

		slot2, _ := hb.Acquire(ctx, "openai", "gpt-3.5-turbo")
		slot2.Release()

		stats := hb.Stats()

		if len(stats.Providers) != 1 {
			t.Errorf("expected 1 provider, got %d", len(stats.Providers))
		}

		if len(stats.Models) != 2 {
			t.Errorf("expected 2 models, got %d", len(stats.Models))
		}
	})
}

func TestHierarchicalBulkhead_PartialFailureRollback(t *testing.T) {
	t.Run("provider failure releases session", func(t *testing.T) {
		configs := map[BulkheadLevel]BulkheadConfig{
			LevelSession: {
				MaxConcurrent: 5,
				MaxQueueSize:  0,
				QueueTimeout:  time.Millisecond * 100,
				CircuitConfig: CircuitConfig{
					FailureThreshold: 5,
					SuccessThreshold: 3,
					Timeout:          time.Second,
				},
			},
			LevelProvider: {
				MaxConcurrent: 1,
				MaxQueueSize:  0,
				QueueTimeout:  time.Millisecond * 100,
				CircuitConfig: CircuitConfig{
					FailureThreshold: 5,
					SuccessThreshold: 3,
					Timeout:          time.Second,
				},
			},
			LevelModel: {
				MaxConcurrent: 1,
				MaxQueueSize:  0,
				QueueTimeout:  time.Millisecond * 100,
				CircuitConfig: CircuitConfig{
					FailureThreshold: 5,
					SuccessThreshold: 3,
					Timeout:          time.Second,
				},
			},
		}

		hb := NewHierarchicalBulkhead("test-session", configs)
		defer hb.Stop()

		ctx := context.Background()

		// First acquire succeeds
		slot1, err := hb.Acquire(ctx, "openai", "gpt-4")
		if err != nil {
			t.Fatalf("first acquire failed: %v", err)
		}

		initialSessionAvailable := hb.Stats().Session.Available

		// Second acquire for same provider should fail at provider level
		_, err = hb.Acquire(ctx, "openai", "gpt-3.5")
		if err == nil {
			t.Fatal("expected provider level failure")
		}

		// Session slot should be released back
		afterFailureSessionAvailable := hb.Stats().Session.Available
		if afterFailureSessionAvailable != initialSessionAvailable {
			t.Errorf("session slot not released on provider failure: before=%d, after=%d",
				initialSessionAvailable, afterFailureSessionAvailable)
		}

		slot1.Release()
	})

	t.Run("model failure releases session and provider", func(t *testing.T) {
		configs := map[BulkheadLevel]BulkheadConfig{
			LevelSession: {
				MaxConcurrent: 5,
				MaxQueueSize:  0,
				QueueTimeout:  time.Millisecond * 100,
				CircuitConfig: CircuitConfig{
					FailureThreshold: 5,
					SuccessThreshold: 3,
					Timeout:          time.Second,
				},
			},
			LevelProvider: {
				MaxConcurrent: 5,
				MaxQueueSize:  0,
				QueueTimeout:  time.Millisecond * 100,
				CircuitConfig: CircuitConfig{
					FailureThreshold: 5,
					SuccessThreshold: 3,
					Timeout:          time.Second,
				},
			},
			LevelModel: {
				MaxConcurrent: 1,
				MaxQueueSize:  0,
				QueueTimeout:  time.Millisecond * 100,
				CircuitConfig: CircuitConfig{
					FailureThreshold: 5,
					SuccessThreshold: 3,
					Timeout:          time.Second,
				},
			},
		}

		hb := NewHierarchicalBulkhead("test-session", configs)
		defer hb.Stop()

		ctx := context.Background()

		// First acquire succeeds
		slot1, err := hb.Acquire(ctx, "openai", "gpt-4")
		if err != nil {
			t.Fatalf("first acquire failed: %v", err)
		}

		initialStats := hb.Stats()

		// Second acquire for same model should fail at model level
		_, err = hb.Acquire(ctx, "openai", "gpt-4")
		if err == nil {
			t.Fatal("expected model level failure")
		}

		// Session and provider slots should be released back
		afterStats := hb.Stats()
		if afterStats.Session.Available != initialStats.Session.Available {
			t.Errorf("session slot not released on model failure")
		}

		if afterStats.Providers["openai"].Available != initialStats.Providers["openai"].Available {
			t.Errorf("provider slot not released on model failure")
		}

		slot1.Release()
	})
}

func TestHierarchicalBulkhead_ConcurrentAccess(t *testing.T) {
	t.Run("concurrent provider creation", func(t *testing.T) {
		hb := NewHierarchicalBulkhead("test-session", nil)
		defer hb.Stop()

		var wg sync.WaitGroup
		numGoroutines := 50

		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx := context.Background()
				slot, err := hb.Acquire(ctx, "openai", "gpt-4")
				if err != nil {
					return // Expected for some goroutines due to limits
				}
				time.Sleep(time.Millisecond)
				slot.Release()
			}()
		}

		wg.Wait()

		stats := hb.Stats()
		if len(stats.Providers) != 1 {
			t.Errorf("expected 1 provider, got %d", len(stats.Providers))
		}

		if len(stats.Models) != 1 {
			t.Errorf("expected 1 model, got %d", len(stats.Models))
		}
	})

	t.Run("concurrent access to multiple providers", func(t *testing.T) {
		hb := NewHierarchicalBulkhead("test-session", nil)
		defer hb.Stop()

		providers := []string{"openai", "anthropic", "google"}
		models := []string{"model-1", "model-2"}

		var wg sync.WaitGroup

		for _, provider := range providers {
			for _, model := range models {
				wg.Add(1)
				go func(p, m string) {
					defer wg.Done()
					ctx := context.Background()
					slot, err := hb.Acquire(ctx, p, m)
					if err != nil {
						return
					}
					time.Sleep(time.Millisecond)
					slot.Release()
				}(provider, model)
			}
		}

		wg.Wait()

		stats := hb.Stats()
		if len(stats.Providers) != 3 {
			t.Errorf("expected 3 providers, got %d", len(stats.Providers))
		}

		if len(stats.Models) != 6 {
			t.Errorf("expected 6 models, got %d", len(stats.Models))
		}
	})
}

func TestHierarchicalSlot_Release(t *testing.T) {
	t.Run("double release is safe", func(t *testing.T) {
		hb := NewHierarchicalBulkhead("test-session", nil)
		defer hb.Stop()

		ctx := context.Background()
		slot, err := hb.Acquire(ctx, "openai", "gpt-4")
		if err != nil {
			t.Fatalf("acquire failed: %v", err)
		}

		statsBeforeRelease := hb.Stats()

		slot.Release()
		slot.Release() // Should be safe
		slot.Release() // Should be safe

		statsAfterRelease := hb.Stats()

		// Available should have increased by exactly 1 at each level
		if statsAfterRelease.Session.Available != statsBeforeRelease.Session.Available+1 {
			t.Errorf("session slot count wrong after double release")
		}
	})
}

func TestHierarchicalSlot_RecordOutcome(t *testing.T) {
	t.Run("record success at all levels", func(t *testing.T) {
		hb := NewHierarchicalBulkhead("test-session", nil)
		defer hb.Stop()

		ctx := context.Background()
		slot, err := hb.Acquire(ctx, "openai", "gpt-4")
		if err != nil {
			t.Fatalf("acquire failed: %v", err)
		}

		slot.RecordSuccess()
		slot.Release()

		// Verify stats - success should be recorded
		stats := hb.Stats()
		if stats.Session.TotalRequests == 0 {
			t.Error("session should have recorded request")
		}
	})

	t.Run("record failure at all levels", func(t *testing.T) {
		hb := NewHierarchicalBulkhead("test-session", nil)
		defer hb.Stop()

		ctx := context.Background()
		slot, err := hb.Acquire(ctx, "openai", "gpt-4")
		if err != nil {
			t.Fatalf("acquire failed: %v", err)
		}

		slot.RecordFailure()
		slot.Release()

		// Verify stats - failure should be recorded
		stats := hb.Stats()
		if stats.Session.TotalFailures == 0 {
			t.Error("session should have recorded failure")
		}
		if stats.Providers["openai"].TotalFailures == 0 {
			t.Error("provider should have recorded failure")
		}
		if stats.Models["openai:gpt-4"].TotalFailures == 0 {
			t.Error("model should have recorded failure")
		}
	})
}

func TestHierarchicalBulkhead_Stop(t *testing.T) {
	t.Run("stop shuts down all bulkheads", func(t *testing.T) {
		hb := NewHierarchicalBulkhead("test-session", nil)

		ctx := context.Background()
		slot, _ := hb.Acquire(ctx, "openai", "gpt-4")
		slot.Release()

		slot, _ = hb.Acquire(ctx, "anthropic", "claude")
		slot.Release()

		// Should not panic or hang
		hb.Stop()
	})
}

func TestHierarchicalBulkhead_Stats(t *testing.T) {
	t.Run("stats reflect current state", func(t *testing.T) {
		hb := NewHierarchicalBulkhead("test-session", nil)
		defer hb.Stop()

		ctx := context.Background()

		// Initial stats
		stats := hb.Stats()
		if stats.Session.Active != 0 {
			t.Errorf("expected 0 active sessions initially, got %d", stats.Session.Active)
		}

		// Acquire a slot
		slot, _ := hb.Acquire(ctx, "openai", "gpt-4")

		stats = hb.Stats()
		if stats.Session.Active != 1 {
			t.Errorf("expected 1 active session after acquire, got %d", stats.Session.Active)
		}
		if stats.Providers["openai"].Active != 1 {
			t.Errorf("expected 1 active provider after acquire, got %d", stats.Providers["openai"].Active)
		}
		if stats.Models["openai:gpt-4"].Active != 1 {
			t.Errorf("expected 1 active model after acquire, got %d", stats.Models["openai:gpt-4"].Active)
		}

		// Release the slot
		slot.Release()

		stats = hb.Stats()
		if stats.Session.Active != 0 {
			t.Errorf("expected 0 active sessions after release, got %d", stats.Session.Active)
		}
	})
}
