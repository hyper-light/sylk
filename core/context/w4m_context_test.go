package context

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestW4M32_ObservationLogAsyncWrite tests async write queue in observation log.
func TestW4M32_ObservationLogAsyncWrite(t *testing.T) {
	t.Parallel()

	tmpFile, err := os.CreateTemp(t.TempDir(), "obs-*.wal")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpFile.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	config := ObservationLogConfig{
		Path:           tmpFile.Name(),
		BufferSize:     100,
		WriteQueueSize: 50,
		SyncInterval:   10 * time.Millisecond,
	}

	log, err := NewObservationLog(ctx, config)
	if err != nil {
		t.Fatalf("failed to create observation log: %v", err)
	}
	defer log.Close()

	var wg sync.WaitGroup
	var writeCount atomic.Int64
	const numWriters = 10
	const writesPerWriter = 50

	// Concurrent writers
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for j := 0; j < writesPerWriter; j++ {
				obs := &EpisodeObservation{
					Timestamp:     time.Now(),
					Position:      int64(j),
					TaskCompleted: true,
				}
				err := log.Record(ctx, obs)
				if err == nil {
					writeCount.Add(1)
				} else if err == ErrWriteQueueFull {
					// Expected under high load - backpressure working
					t.Logf("backpressure: writer %d got ErrWriteQueueFull", writerID)
				} else if err != ErrObservationLogClosed {
					t.Errorf("writer %d unexpected error: %v", writerID, err)
				}
			}
		}(i)
	}

	wg.Wait()

	// Some writes should have succeeded
	if writeCount.Load() == 0 {
		t.Error("expected some successful writes")
	}
	t.Logf("successful writes: %d/%d", writeCount.Load(), numWriters*writesPerWriter)
}

// TestW4M38_HotCacheEvictionBatch tests batch eviction with minimal lock time.
func TestW4M38_HotCacheEvictionBatch(t *testing.T) {
	t.Parallel()

	var evictionCount atomic.Int64
	evictionCallback := func(id string, entry *ContentEntry) {
		evictionCount.Add(1)
	}

	config := HotCacheConfig{
		MaxSize:           1024, // 1KB
		EvictionBatchSize: 5,
		OnEvict:           evictionCallback,
		AsyncEviction:     false, // Test sync eviction path
	}
	cache := NewHotCache(config)
	defer cache.Close()

	// Add entries that will exceed max size
	for i := 0; i < 20; i++ {
		entry := &ContentEntry{
			ID:      string(rune('A' + i)),
			Content: string(make([]byte, 100)), // 100 bytes each
		}
		cache.Add(entry.ID, entry)
	}

	// Should have triggered eviction
	if evictionCount.Load() == 0 {
		t.Error("expected eviction to have occurred")
	}

	stats := cache.Stats()
	t.Logf("entries: %d, size: %d, evictions: %d",
		stats.EntryCount, stats.CurrentSize, stats.Evictions)

	// Size should be under or around max
	if stats.CurrentSize > int64(config.MaxSize)*2 {
		t.Errorf("cache size %d significantly exceeds max %d", stats.CurrentSize, config.MaxSize)
	}
}

// TestW4M38_HotCacheAsyncEviction tests async eviction mode.
func TestW4M38_HotCacheAsyncEviction(t *testing.T) {
	t.Parallel()

	var evictionCount atomic.Int64
	evictionCallback := func(id string, entry *ContentEntry) {
		evictionCount.Add(1)
	}

	config := HotCacheConfig{
		MaxSize:           512,
		EvictionBatchSize: 10,
		OnEvict:           evictionCallback,
		AsyncEviction:     true, // Enable async eviction
	}
	cache := NewHotCache(config)
	defer cache.Close()

	var wg sync.WaitGroup
	const numWriters = 5
	const entriesPerWriter = 20

	// Concurrent adds
	for i := 0; i < numWriters; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for j := 0; j < entriesPerWriter; j++ {
				entry := &ContentEntry{
					ID:      string(rune('A'+writerID)) + string(rune('0'+j)),
					Content: string(make([]byte, 50)),
				}
				cache.Add(entry.ID, entry)
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				cache.Get(string(rune('A' + j%5)))
				time.Sleep(time.Millisecond)
			}
		}()
	}

	wg.Wait()

	// Give async eviction time to complete
	time.Sleep(100 * time.Millisecond)

	t.Logf("eviction count: %d, cache stats: %+v", evictionCount.Load(), cache.Stats())
}

// TestW4M38_HotCacheConcurrentAccess tests concurrent cache operations.
func TestW4M38_HotCacheConcurrentAccess(t *testing.T) {
	t.Parallel()

	config := HotCacheConfig{
		MaxSize:           2048,
		EvictionBatchSize: 10,
		AsyncEviction:     true,
	}
	cache := NewHotCache(config)
	defer cache.Close()

	var wg sync.WaitGroup
	var addCount, getCount, hitCount atomic.Int64

	// Writers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				entry := &ContentEntry{
					ID:      string(rune('A' + j%26)),
					Content: "test content",
				}
				cache.Add(entry.ID, entry)
				addCount.Add(1)
			}
		}(i)
	}

	// Readers
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				key := string(rune('A' + j%26))
				if cache.Get(key) != nil {
					hitCount.Add(1)
				}
				getCount.Add(1)
			}
		}()
	}

	wg.Wait()

	t.Logf("adds: %d, gets: %d, hits: %d", addCount.Load(), getCount.Load(), hitCount.Load())
}

