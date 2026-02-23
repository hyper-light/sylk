package orchestrator

import (
	"context"
	"sync"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/dgraph-io/ristretto"
)

// BufferRegistryConfig configures the per-task circular buffer system.
type BufferRegistryConfig struct {
	BufferCapacity int           // Per-task ring buffer size. Derived: max(32, maxConcurrency*4)
	MaxBuffers     int           // Maximum active task buffers. Derived: max(256, maxConcurrency*16)
	TTL            time.Duration // Idle buffer TTL before GC flushes to SQLite
	GCInterval     time.Duration // How often GC runs
	RistrettoMax   int64         // Ristretto MaxCost. Derived from capacity * maxBuffers * avgEntryCost
}

// DefaultBufferRegistryConfig derives configuration from maxConcurrency.
func DefaultBufferRegistryConfig(maxConcurrency int) BufferRegistryConfig {
	bufCap := max(32, maxConcurrency*4)
	maxBuf := max(256, maxConcurrency*16)
	// Average entry cost estimate: 200 bytes per TaskUpdateEntry
	avgCost := int64(200)
	ristrettoMax := int64(bufCap) * int64(maxBuf) * avgCost

	return BufferRegistryConfig{
		BufferCapacity: bufCap,
		MaxBuffers:     maxBuf,
		TTL:            10 * time.Minute,
		GCInterval:     30 * time.Second,
		RistrettoMax:   ristrettoMax,
	}
}

// TaskBuffer is a per-task circular buffer of pipeline updates.
type TaskBuffer struct {
	mu       sync.Mutex
	entries  []TaskUpdateEntry
	head     int
	count    int
	capacity int
	taskID   string
	lastSeen time.Time
}

func newTaskBuffer(taskID string, capacity int) *TaskBuffer {
	return &TaskBuffer{
		entries:  make([]TaskUpdateEntry, capacity),
		capacity: capacity,
		taskID:   taskID,
		lastSeen: time.Now(),
	}
}

// push appends an entry to the circular buffer.
// If full, overwrites the oldest entry (head advances).
func (b *TaskBuffer) push(entry TaskUpdateEntry) {
	b.mu.Lock()
	defer b.mu.Unlock()

	idx := (b.head + b.count) % b.capacity
	b.entries[idx] = entry
	b.lastSeen = entry.Timestamp

	if b.count < b.capacity {
		b.count++
	} else {
		b.head = (b.head + 1) % b.capacity
	}
}

// read returns up to limit entries in chronological order.
func (b *TaskBuffer) read(limit int) []TaskUpdateEntry {
	b.mu.Lock()
	defer b.mu.Unlock()

	n := b.count
	if limit > 0 && limit < n {
		n = limit
	}

	result := make([]TaskUpdateEntry, n)
	for i := range n {
		idx := (b.head + b.count - n + i) % b.capacity
		result[i] = b.entries[idx]
	}
	return result
}

// readSince returns entries after the given time in chronological order.
func (b *TaskBuffer) readSince(since time.Time) []TaskUpdateEntry {
	b.mu.Lock()
	defer b.mu.Unlock()

	var result []TaskUpdateEntry
	for i := range b.count {
		idx := (b.head + i) % b.capacity
		if b.entries[idx].Timestamp.After(since) {
			result = append(result, b.entries[idx])
		}
	}
	return result
}

// latest returns the most recent entry, if any.
func (b *TaskBuffer) latest() (TaskUpdateEntry, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.count == 0 {
		return TaskUpdateEntry{}, false
	}
	idx := (b.head + b.count - 1) % b.capacity
	return b.entries[idx], true
}

// flush returns all entries and resets the buffer.
func (b *TaskBuffer) flush() []TaskUpdateEntry {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.count == 0 {
		return nil
	}

	result := make([]TaskUpdateEntry, b.count)
	for i := range b.count {
		result[i] = b.entries[(b.head+i)%b.capacity]
	}
	b.head = 0
	b.count = 0
	return result
}

// BufferRegistry manages per-task circular buffers with Ristretto hot cache
// and SQLite cold storage fallback.
type BufferRegistry struct {
	mu      sync.RWMutex
	buffers map[string]*TaskBuffer
	config  BufferRegistryConfig
	store   *Store
	cache   *ristretto.Cache
	scope   *concurrency.GoroutineScope
	closed  bool
}

// NewBufferRegistry creates a new BufferRegistry.
func NewBufferRegistry(cfg BufferRegistryConfig, store *Store, scope *concurrency.GoroutineScope) (*BufferRegistry, error) {
	// Ristretto sizing: NumCounters ≈ 10× expected items
	totalKeys := int64(cfg.MaxBuffers)
	numCounters := totalKeys * 10

	cache, err := ristretto.NewCache(&ristretto.Config{
		NumCounters: numCounters,
		MaxCost:     cfg.RistrettoMax,
		BufferItems: 64,
	})
	if err != nil {
		return nil, err
	}

	return &BufferRegistry{
		buffers: make(map[string]*TaskBuffer),
		config:  cfg,
		store:   store,
		cache:   cache,
		scope:   scope,
	}, nil
}

// Push writes a task update entry to the hot buffer and Ristretto cache.
// Create-on-first-write semantics.
func (r *BufferRegistry) Push(entry TaskUpdateEntry) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}

	buf, ok := r.buffers[entry.TaskID]
	if !ok {
		if len(r.buffers) >= r.config.MaxBuffers {
			r.mu.Unlock()
			return nil // Drop if at buffer limit
		}
		buf = newTaskBuffer(entry.TaskID, r.config.BufferCapacity)
		r.buffers[entry.TaskID] = buf
	}
	r.mu.Unlock()

	buf.push(entry)

	// Update Ristretto hot cache with latest entry
	r.cache.SetWithTTL(
		"latest:"+entry.TaskID,
		&entry,
		entry.Cost(),
		r.config.TTL,
	)

	return nil
}

// Read returns task update entries. Hot buffer first, cold SQLite fallback.
func (r *BufferRegistry) Read(taskID string, limit int) []TaskUpdateEntry {
	r.mu.RLock()
	buf, ok := r.buffers[taskID]
	r.mu.RUnlock()

	if ok {
		entries := buf.read(limit)
		if len(entries) > 0 {
			return entries
		}
	}

	// Cold fallback to SQLite
	if r.store != nil {
		entries, err := r.store.QueryTaskUpdates(taskID, limit)
		if err == nil && len(entries) > 0 {
			return entries
		}
	}

	return nil
}

// ReadSince returns entries after a given time. Hot first, cold fallback.
func (r *BufferRegistry) ReadSince(taskID string, since time.Time) []TaskUpdateEntry {
	r.mu.RLock()
	buf, ok := r.buffers[taskID]
	r.mu.RUnlock()

	if ok {
		entries := buf.readSince(since)
		if len(entries) > 0 {
			return entries
		}
	}

	if r.store != nil {
		entries, err := r.store.QueryTaskUpdatesSince(taskID, since)
		if err == nil && len(entries) > 0 {
			return entries
		}
	}

	return nil
}

// Latest returns the most recent entry for a task from hot cache.
func (r *BufferRegistry) Latest(taskID string) (*TaskUpdateEntry, bool) {
	// Try Ristretto first (fastest path)
	if val, found := r.cache.Get("latest:" + taskID); found {
		if entry, ok := val.(*TaskUpdateEntry); ok {
			return entry, true
		}
	}

	// Try buffer
	r.mu.RLock()
	buf, ok := r.buffers[taskID]
	r.mu.RUnlock()

	if ok {
		if entry, found := buf.latest(); found {
			return &entry, true
		}
	}

	return nil, false
}

// StartGC launches the background GC goroutine via GoroutineScope.
func (r *BufferRegistry) StartGC(ctx context.Context) {
	if r.scope == nil {
		return
	}
	r.scope.Go("buffer-registry-gc", 0, func(ctx context.Context) error {
		ticker := time.NewTicker(r.config.GCInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
				r.runGC()
			}
		}
	})
}

// runGC evicts idle buffers to SQLite cold storage.
func (r *BufferRegistry) runGC() {
	r.mu.Lock()
	var evictIDs []string
	now := time.Now()
	for taskID, buf := range r.buffers {
		buf.mu.Lock()
		idle := now.Sub(buf.lastSeen) > r.config.TTL
		buf.mu.Unlock()
		if idle {
			evictIDs = append(evictIDs, taskID)
		}
	}

	// Extract buffers to evict under write lock
	toFlush := make(map[string]*TaskBuffer, len(evictIDs))
	for _, id := range evictIDs {
		toFlush[id] = r.buffers[id]
		delete(r.buffers, id)
	}
	r.mu.Unlock()

	// Flush to SQLite outside the lock
	for taskID, buf := range toFlush {
		entries := buf.flush()
		if len(entries) > 0 && r.store != nil {
			r.store.InsertTaskUpdates(entries)
		}
		r.cache.Del("latest:" + taskID)
	}
}

// Close flushes all hot buffers to SQLite and closes Ristretto.
func (r *BufferRegistry) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true

	// Snapshot buffers for flush
	allBuffers := make(map[string]*TaskBuffer, len(r.buffers))
	for k, v := range r.buffers {
		allBuffers[k] = v
	}
	r.buffers = make(map[string]*TaskBuffer)
	r.mu.Unlock()

	// Flush all to SQLite
	for _, buf := range allBuffers {
		entries := buf.flush()
		if len(entries) > 0 && r.store != nil {
			r.store.InsertTaskUpdates(entries)
		}
	}

	r.cache.Close()
	return nil
}

// ActiveBufferCount returns the number of active task buffers.
func (r *BufferRegistry) ActiveBufferCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.buffers)
}

// BufferSnapshots returns snapshot summaries for state_snapshot.
func (r *BufferRegistry) BufferSnapshots(limit int) []bufferSnap {
	r.mu.RLock()
	defer r.mu.RUnlock()

	snaps := make([]bufferSnap, 0, min(len(r.buffers), limit))
	for _, buf := range r.buffers {
		if entry, ok := buf.latest(); ok {
			snaps = append(snaps, bufferSnap{
				TaskID:   buf.taskID,
				Count:    buf.count,
				Latest:   entry.Status,
				Progress: entry.Progress,
			})
		}
		if len(snaps) >= limit {
			break
		}
	}
	return snaps
}
