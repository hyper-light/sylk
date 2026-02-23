package activation

import (
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/container"
)

// WarmPool is a bounded LRU cache of paused containers. When at capacity,
// Put evicts the least-recently-used entry and returns it for teardown.
type WarmPool struct {
	mu       sync.Mutex
	entries  map[string]*warmEntry // agentType -> entry
	order    []string              // LRU order: most recent at end
	capacity int
}

type warmEntry struct {
	container *container.Container
	pausedAt  time.Time
}

// NewWarmPool creates a pool with the given capacity. Capacity must be >= 1.
func NewWarmPool(capacity int) *WarmPool {
	return &WarmPool{
		entries:  make(map[string]*warmEntry, capacity),
		order:    make([]string, 0, capacity),
		capacity: capacity,
	}
}

// Put stores a paused container. If at capacity, evicts the LRU entry
// and returns its container for the caller to tear down. Returns nil
// if no eviction was needed.
func (wp *WarmPool) Put(agentType string, c *container.Container) *container.Container {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	// If already present, update in place and move to back (most recent).
	if existing, ok := wp.entries[agentType]; ok {
		evicted := existing.container
		existing.container = c
		existing.pausedAt = time.Now()
		wp.moveToBackLocked(agentType)
		return evicted
	}

	var evictedContainer *container.Container
	if len(wp.entries) >= wp.capacity {
		evictedContainer = wp.evictLRULocked()
	}

	wp.entries[agentType] = &warmEntry{
		container: c,
		pausedAt:  time.Now(),
	}
	wp.order = append(wp.order, agentType)
	return evictedContainer
}

// Take removes and returns the warm container for the given type.
// Returns nil if not present.
func (wp *WarmPool) Take(agentType string) *container.Container {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	entry, ok := wp.entries[agentType]
	if !ok {
		return nil
	}
	delete(wp.entries, agentType)
	wp.removeFromOrderLocked(agentType)
	return entry.container
}

// Len returns the current pool size.
func (wp *WarmPool) Len() int {
	wp.mu.Lock()
	defer wp.mu.Unlock()
	return len(wp.entries)
}

// Contains reports whether the pool has an entry for the given type.
func (wp *WarmPool) Contains(agentType string) bool {
	wp.mu.Lock()
	defer wp.mu.Unlock()
	_, ok := wp.entries[agentType]
	return ok
}

func (wp *WarmPool) evictLRULocked() *container.Container {
	if len(wp.order) == 0 {
		return nil
	}
	oldest := wp.order[0]
	wp.order = wp.order[1:]
	entry := wp.entries[oldest]
	delete(wp.entries, oldest)
	return entry.container
}

func (wp *WarmPool) moveToBackLocked(agentType string) {
	wp.removeFromOrderLocked(agentType)
	wp.order = append(wp.order, agentType)
}

func (wp *WarmPool) removeFromOrderLocked(agentType string) {
	for i, t := range wp.order {
		if t == agentType {
			wp.order = append(wp.order[:i], wp.order[i+1:]...)
			return
		}
	}
}
