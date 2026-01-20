package mitigations

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProvenanceTracker_ConcurrentCacheAccess(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := NewProvenanceTracker(db)
	defer tracker.Close()

	// Manually populate the cache to test concurrent access patterns
	tracker.cacheMu.Lock()
	tracker.provenanceCache["test-node"] = []*Provenance{
		{ID: "1", NodeID: "test-node", Confidence: 0.9},
	}
	tracker.cacheOrder = append(tracker.cacheOrder, "test-node")
	tracker.cacheMu.Unlock()

	var wg sync.WaitGroup

	// Concurrent readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				tracker.cacheMu.RLock()
				_ = tracker.provenanceCache["test-node"]
				tracker.cacheMu.RUnlock()
			}
		}()
	}

	// Concurrent cache invalidations
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				tracker.cacheMu.Lock()
				tracker.provenanceCache = make(map[string][]*Provenance)
				tracker.cacheOrder = nil
				tracker.cacheMu.Unlock()
			}
		}()
	}

	wg.Wait()
}

func TestProvenanceTracker_ConcurrentGetProvenance(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := NewProvenanceTracker(db)
	defer tracker.Close()

	var wg sync.WaitGroup

	// Concurrent readers - these will hit DB then cache
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				// GetProvenance should not race even without data
				_, _ = tracker.GetProvenance("concurrent-test")
			}
		}()
	}

	// Concurrent cache clears
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_ = tracker.InvalidateBySource("any-source")
			}
		}()
	}

	wg.Wait()
}

func TestProvenanceTracker_Close(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := NewProvenanceTracker(db)

	// Manually populate the cache
	tracker.cacheMu.Lock()
	tracker.provenanceCache["close-node"] = []*Provenance{
		{ID: "1", NodeID: "close-node"},
	}
	tracker.cacheOrder = []string{"close-node"}
	tracker.cacheMu.Unlock()

	// Close should not panic and should clear cache
	tracker.Close()

	// Internal cache should be empty after close
	tracker.cacheMu.RLock()
	cacheLen := len(tracker.provenanceCache)
	tracker.cacheMu.RUnlock()
	assert.Equal(t, 0, cacheLen)
}

func TestProvenanceTracker_InvalidateCache(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	tracker := NewProvenanceTracker(db)
	defer tracker.Close()

	// Populate cache
	tracker.cacheMu.Lock()
	tracker.provenanceCache["node-1"] = []*Provenance{{ID: "1"}}
	tracker.provenanceCache["node-2"] = []*Provenance{{ID: "2"}}
	tracker.cacheOrder = []string{"node-1", "node-2"}
	tracker.cacheMu.Unlock()

	// Invalidate one entry
	tracker.invalidateCache("node-1")

	tracker.cacheMu.RLock()
	_, exists1 := tracker.provenanceCache["node-1"]
	_, exists2 := tracker.provenanceCache["node-2"]
	tracker.cacheMu.RUnlock()

	assert.False(t, exists1)
	assert.True(t, exists2)
}
