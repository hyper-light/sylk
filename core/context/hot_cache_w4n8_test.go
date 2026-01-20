package context

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// W4N.8 Tests: HotCache Close Thread-Safety Against Add
// =============================================================================

// TestW4N8_HappyPath_AddBeforeClose tests that Add() succeeds before Close().
func TestW4N8_HappyPath_AddBeforeClose(t *testing.T) {
	t.Run("sync mode add before close succeeds", func(t *testing.T) {
		cache := NewHotCache(HotCacheConfig{
			MaxSize:       100000,
			AsyncEviction: false,
		})

		// Add entry before close
		entry := makeTestEntry("test-1", 1000)
		cache.Add("test-1", entry)

		// Verify entry was added
		if !cache.Contains("test-1") {
			t.Error("expected entry to exist after add before close")
		}

		result := cache.Get("test-1")
		if result == nil {
			t.Error("expected to retrieve entry added before close")
		}
		if result.ID != "test-1" {
			t.Errorf("expected ID 'test-1', got '%s'", result.ID)
		}

		// Now close
		cache.Close()
	})

	t.Run("async mode add before close succeeds", func(t *testing.T) {
		cache := NewHotCache(HotCacheConfig{
			MaxSize:       100000,
			AsyncEviction: true,
		})

		// Add entry before close
		entry := makeTestEntry("test-async-1", 1000)
		cache.Add("test-async-1", entry)

		// Verify entry was added
		if !cache.Contains("test-async-1") {
			t.Error("expected entry to exist after add before close in async mode")
		}

		// Now close
		cache.Close()
	})

	t.Run("multiple adds before close all succeed", func(t *testing.T) {
		cache := NewHotCache(HotCacheConfig{
			MaxSize:       100000,
			AsyncEviction: true,
		})

		// Add multiple entries
		for i := 0; i < 10; i++ {
			id := fmt.Sprintf("entry-%d", i)
			cache.Add(id, makeTestEntry(id, 100))
		}

		// Verify all entries exist
		stats := cache.Stats()
		if stats.EntryCount != 10 {
			t.Errorf("expected 10 entries, got %d", stats.EntryCount)
		}

		cache.Close()
	})
}

// TestW4N8_NegativePath_AddAfterClose tests that Add() fails gracefully after Close().
func TestW4N8_NegativePath_AddAfterClose(t *testing.T) {
	t.Run("sync mode add after close is ignored", func(t *testing.T) {
		cache := NewHotCache(HotCacheConfig{
			MaxSize:       100000,
			AsyncEviction: false,
		})

		// Close first
		cache.Close()

		// Try to add after close - should be silently ignored
		entry := makeTestEntry("after-close", 1000)
		cache.Add("after-close", entry)

		// Verify entry was NOT added
		if cache.Contains("after-close") {
			t.Error("expected entry to NOT exist after add following close")
		}
	})

	t.Run("async mode add after close is ignored", func(t *testing.T) {
		cache := NewHotCache(HotCacheConfig{
			MaxSize:       100000,
			AsyncEviction: true,
		})

		// Close first
		cache.Close()

		// Try to add after close - should be silently ignored
		entry := makeTestEntry("after-close-async", 1000)
		cache.Add("after-close-async", entry)

		// Verify entry was NOT added
		if cache.Contains("after-close-async") {
			t.Error("expected entry to NOT exist after add following close in async mode")
		}
	})

	t.Run("multiple adds after close all ignored", func(t *testing.T) {
		cache := NewHotCache(HotCacheConfig{
			MaxSize:       100000,
			AsyncEviction: true,
		})

		// Add one entry before close
		cache.Add("before", makeTestEntry("before", 100))

		// Close
		cache.Close()

		// Try to add multiple entries after close
		for i := 0; i < 10; i++ {
			id := fmt.Sprintf("after-%d", i)
			cache.Add(id, makeTestEntry(id, 100))
		}

		// Only the entry added before close should exist
		stats := cache.Stats()
		if stats.EntryCount != 1 {
			t.Errorf("expected 1 entry (added before close), got %d", stats.EntryCount)
		}
	})

	t.Run("add after close does not panic", func(t *testing.T) {
		cache := NewHotCache(HotCacheConfig{
			MaxSize:       100000,
			AsyncEviction: true,
		})

		cache.Close()

		// Should not panic
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("Add() after Close() caused panic: %v", r)
			}
		}()

		cache.Add("panic-test", makeTestEntry("panic-test", 1000))
	})
}

// TestW4N8_Failure_AddDuringClose tests that Add() during Close() is handled correctly.
func TestW4N8_Failure_AddDuringClose(t *testing.T) {
	t.Run("add during close is handled gracefully", func(t *testing.T) {
		cache := NewHotCache(HotCacheConfig{
			MaxSize:       100000,
			AsyncEviction: true,
		})

		// Add some initial entries
		for i := 0; i < 5; i++ {
			cache.Add(fmt.Sprintf("initial-%d", i), makeTestEntry(fmt.Sprintf("initial-%d", i), 100))
		}

		var wg sync.WaitGroup
		var addSucceeded atomic.Bool
		var closeCompleted atomic.Bool

		// Start close in a goroutine
		wg.Add(1)
		go func() {
			defer wg.Done()
			cache.Close()
			closeCompleted.Store(true)
		}()

		// Try to add during close
		wg.Add(1)
		go func() {
			defer wg.Done()
			entry := makeTestEntry("during-close", 1000)
			cache.Add("during-close", entry)
			if cache.Contains("during-close") {
				addSucceeded.Store(true)
			}
		}()

		wg.Wait()

		// Either the add succeeded (before close flag was set) or it didn't
		// Both outcomes are valid - the important thing is no panic or deadlock
		t.Logf("Add succeeded: %v, Close completed: %v", addSucceeded.Load(), closeCompleted.Load())
	})
}

// TestW4N8_RaceConditions_ConcurrentAddAndClose tests concurrent Add() and Close().
func TestW4N8_RaceConditions_ConcurrentAddAndClose(t *testing.T) {
	t.Run("concurrent adds and single close", func(t *testing.T) {
		for iteration := 0; iteration < 10; iteration++ {
			cache := NewHotCache(HotCacheConfig{
				MaxSize:       100000,
				AsyncEviction: true,
			})

			var wg sync.WaitGroup
			var addCount atomic.Int64
			numAdders := 50

			// Start multiple concurrent adders
			for i := 0; i < numAdders; i++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					entryID := fmt.Sprintf("concurrent-%d", id)
					cache.Add(entryID, makeTestEntry(entryID, 100))
					addCount.Add(1)
				}(i)
			}

			// Close after a brief moment
			wg.Add(1)
			go func() {
				defer wg.Done()
				time.Sleep(time.Microsecond * 10)
				cache.Close()
			}()

			wg.Wait()

			// All goroutines should complete without panic or deadlock
			t.Logf("Iteration %d: %d adds attempted", iteration, addCount.Load())
		}
	})

	t.Run("rapid add and close alternation", func(t *testing.T) {
		for iteration := 0; iteration < 10; iteration++ {
			cache := NewHotCache(HotCacheConfig{
				MaxSize:       100000,
				AsyncEviction: true,
			})

			var wg sync.WaitGroup

			// Rapid adds
			for i := 0; i < 20; i++ {
				wg.Add(1)
				go func(id int) {
					defer wg.Done()
					cache.Add(fmt.Sprintf("rapid-%d", id), makeTestEntry(fmt.Sprintf("rapid-%d", id), 50))
				}(i)
			}

			// Close somewhere in the middle
			wg.Add(1)
			go func() {
				defer wg.Done()
				cache.Close()
			}()

			wg.Wait()
		}
	})

	t.Run("stress test concurrent add and close", func(t *testing.T) {
		cache := NewHotCache(HotCacheConfig{
			MaxSize:       10000,
			AsyncEviction: true,
		})

		var wg sync.WaitGroup
		var closeOnce sync.Once

		// Many concurrent adders
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				for j := 0; j < 10; j++ {
					entryID := fmt.Sprintf("stress-%d-%d", id, j)
					cache.Add(entryID, makeTestEntry(entryID, 100))
					time.Sleep(time.Microsecond)
				}
			}(i)
		}

		// Close after some time
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(time.Millisecond * 5)
			closeOnce.Do(func() {
				cache.Close()
			})
		}()

		wg.Wait()
	})
}

// TestW4N8_EdgeCases tests edge cases for Close/Add thread safety.
func TestW4N8_EdgeCases(t *testing.T) {
	t.Run("multiple close calls are safe", func(t *testing.T) {
		cache := NewHotCache(HotCacheConfig{
			MaxSize:       100000,
			AsyncEviction: true,
		})

		cache.Add("test", makeTestEntry("test", 1000))

		// Multiple close calls should not panic
		cache.Close()
		cache.Close()
		cache.Close()
	})

	t.Run("close on empty cache", func(t *testing.T) {
		cache := NewHotCache(HotCacheConfig{
			MaxSize:       100000,
			AsyncEviction: true,
		})

		// Close empty cache
		cache.Close()

		// Add should still be rejected
		cache.Add("after-empty-close", makeTestEntry("after-empty-close", 100))
		if cache.Contains("after-empty-close") {
			t.Error("expected add to be rejected after close on empty cache")
		}
	})

	t.Run("add with nil entry after close", func(t *testing.T) {
		cache := NewHotCache(HotCacheConfig{
			MaxSize:       100000,
			AsyncEviction: false,
		})

		cache.Close()

		// Add nil should not panic even after close
		cache.Add("nil-test", nil)
	})

	t.Run("sync mode close and add", func(t *testing.T) {
		cache := NewHotCache(HotCacheConfig{
			MaxSize:       100000,
			AsyncEviction: false,
		})

		var wg sync.WaitGroup

		// Concurrent adds with sync eviction
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				cache.Add(fmt.Sprintf("sync-%d", id), makeTestEntry(fmt.Sprintf("sync-%d", id), 100))
			}(i)
		}

		// Close
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(time.Microsecond * 5)
			cache.Close()
		}()

		wg.Wait()
	})

	t.Run("closed flag check is atomic", func(t *testing.T) {
		cache := NewHotCache(HotCacheConfig{
			MaxSize:       100000,
			AsyncEviction: true,
		})

		var wg sync.WaitGroup
		iterations := 1000

		// Many concurrent adds checking the closed flag
		for i := 0; i < iterations; i++ {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()
				cache.Add(fmt.Sprintf("atomic-%d", id), makeTestEntry(fmt.Sprintf("atomic-%d", id), 10))
			}(i)
		}

		// Close in the middle
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(time.Microsecond * 100)
			cache.Close()
		}()

		wg.Wait()

		// Verify no adds after close
		cache.Add("post-close-check", makeTestEntry("post-close-check", 100))
		if cache.Contains("post-close-check") {
			t.Error("add succeeded after close - closed flag check may not be atomic")
		}
	})

	t.Run("eviction goroutine stops cleanly", func(t *testing.T) {
		cache := NewHotCache(HotCacheConfig{
			MaxSize:       1000, // Small size to trigger evictions
			AsyncEviction: true,
		})

		// Fill cache to trigger evictions
		for i := 0; i < 100; i++ {
			cache.Add(fmt.Sprintf("evict-%d", i), makeTestEntry(fmt.Sprintf("evict-%d", i), 100))
		}

		// Allow eviction to start
		time.Sleep(time.Millisecond * 10)

		// Close should wait for eviction goroutine
		done := make(chan struct{})
		go func() {
			cache.Close()
			close(done)
		}()

		select {
		case <-done:
			// Success - Close completed
		case <-time.After(time.Second * 2):
			t.Error("Close() did not complete in time - possible deadlock in eviction goroutine")
		}
	})

	t.Run("rapid close reopen pattern simulation", func(t *testing.T) {
		// Simulate rapid close and create new cache pattern
		for i := 0; i < 20; i++ {
			cache := NewHotCache(HotCacheConfig{
				MaxSize:       10000,
				AsyncEviction: true,
			})

			// Add some entries
			for j := 0; j < 10; j++ {
				cache.Add(fmt.Sprintf("reopen-%d-%d", i, j), makeTestEntry(fmt.Sprintf("reopen-%d-%d", i, j), 100))
			}

			// Immediately close
			cache.Close()

			// Verify cache is properly closed
			cache.Add("after-close", makeTestEntry("after-close", 100))
			if cache.Contains("after-close") {
				t.Errorf("iteration %d: add succeeded after close", i)
			}
		}
	})
}

// TestW4N8_AddBatchAfterClose tests AddBatch behavior after close.
func TestW4N8_AddBatchAfterClose(t *testing.T) {
	t.Run("add batch after close is ignored", func(t *testing.T) {
		cache := NewHotCache(HotCacheConfig{
			MaxSize:       100000,
			AsyncEviction: true,
		})

		cache.Close()

		entries := map[string]*ContentEntry{
			"batch-1": makeTestEntry("batch-1", 100),
			"batch-2": makeTestEntry("batch-2", 100),
			"batch-3": makeTestEntry("batch-3", 100),
		}

		cache.AddBatch(entries)

		stats := cache.Stats()
		if stats.EntryCount != 0 {
			t.Errorf("expected 0 entries after AddBatch on closed cache, got %d", stats.EntryCount)
		}
	})
}

// TestW4N8_EvictionCallbackAfterClose tests eviction callback behavior.
func TestW4N8_EvictionCallbackAfterClose(t *testing.T) {
	t.Run("eviction callback not triggered for rejected adds", func(t *testing.T) {
		var callbackCount atomic.Int64

		cache := NewHotCache(HotCacheConfig{
			MaxSize:       1000,
			AsyncEviction: false,
			OnEvict: func(id string, entry *ContentEntry) {
				callbackCount.Add(1)
			},
		})

		// Fill cache to trigger evictions
		for i := 0; i < 20; i++ {
			cache.Add(fmt.Sprintf("pre-close-%d", i), makeTestEntry(fmt.Sprintf("pre-close-%d", i), 100))
		}

		preCloseCallbacks := callbackCount.Load()

		// Close cache
		cache.Close()

		// Try to add more - these should be rejected without triggering eviction
		for i := 0; i < 10; i++ {
			cache.Add(fmt.Sprintf("post-close-%d", i), makeTestEntry(fmt.Sprintf("post-close-%d", i), 100))
		}

		postCloseCallbacks := callbackCount.Load()

		// No new eviction callbacks should have been triggered after close
		if postCloseCallbacks > preCloseCallbacks {
			t.Errorf("eviction callbacks triggered after close: pre=%d, post=%d",
				preCloseCallbacks, postCloseCallbacks)
		}
	})
}
