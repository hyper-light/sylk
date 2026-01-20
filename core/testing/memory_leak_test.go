// Package testing provides memory leak detection tests for the core components.
package testing

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/adalundhe/sylk/core/concurrency"
	"github.com/adalundhe/sylk/core/search"
	"github.com/adalundhe/sylk/core/search/coordinator"
	"github.com/adalundhe/sylk/core/vectorgraphdb"
	"github.com/adalundhe/sylk/core/vectorgraphdb/hnsw"
)

const (
	// memoryGrowthTolerance allows 10% growth between iterations.
	memoryGrowthTolerance = 0.10
	// iterations is the number of cycles for leak detection.
	iterations = 5
	// operationsPerIteration is the number of operations per cycle.
	operationsPerIteration = 100
)

// memorySnapshot captures memory statistics at a point in time.
type memorySnapshot struct {
	HeapAlloc   uint64
	HeapObjects uint64
	NumGC       uint32
}

// takeMemorySnapshot forces GC and captures memory stats.
func takeMemorySnapshot() memorySnapshot {
	runtime.GC()
	runtime.GC() // Double GC to ensure finalizers run
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return memorySnapshot{
		HeapAlloc:   ms.HeapAlloc,
		HeapObjects: ms.HeapObjects,
		NumGC:       ms.NumGC,
	}
}

// checkMemoryGrowth verifies memory growth is within tolerance.
func checkMemoryGrowth(t *testing.T, before, after memorySnapshot, label string) {
	t.Helper()
	if before.HeapAlloc == 0 {
		return
	}
	// Memory went down - no leak
	if after.HeapAlloc <= before.HeapAlloc {
		return
	}
	growth := float64(after.HeapAlloc-before.HeapAlloc) / float64(before.HeapAlloc)
	if growth > memoryGrowthTolerance {
		t.Errorf("%s: memory grew %.1f%% (before: %d, after: %d)",
			label, growth*100, before.HeapAlloc, after.HeapAlloc)
	}
}

// TestMemoryLeakHNSWGraph tests for memory leaks in HNSW operations.
func TestMemoryLeakHNSWGraph(t *testing.T) {
	t.Run("InsertDeleteCycle", testHNSWInsertDeleteCycle)
	t.Run("SearchOperations", testHNSWSearchOperations)
}

func testHNSWInsertDeleteCycle(t *testing.T) {
	baseline := takeMemorySnapshot()
	cfg := hnsw.Config{M: 8, EfConstruct: 50, EfSearch: 50, Dimension: 64}

	for i := range iterations {
		idx := hnsw.New(cfg)
		runHNSWInsertDelete(t, idx, i)
	}

	final := takeMemorySnapshot()
	checkMemoryGrowth(t, baseline, final, "HNSW insert/delete cycle")
}

func runHNSWInsertDelete(t *testing.T, idx *hnsw.Index, iteration int) {
	t.Helper()
	ids := make([]string, operationsPerIteration)

	for j := range operationsPerIteration {
		id := fmt.Sprintf("node-%d-%d", iteration, j)
		vec := randomVector(64)
		if err := idx.Insert(id, vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
			t.Fatalf("insert failed: %v", err)
		}
		ids[j] = id
	}

	for _, id := range ids {
		if err := idx.Delete(id); err != nil {
			t.Fatalf("delete failed: %v", err)
		}
	}
}

func testHNSWSearchOperations(t *testing.T) {
	cfg := hnsw.Config{M: 8, EfConstruct: 50, EfSearch: 50, Dimension: 64}
	idx := hnsw.New(cfg)
	populateHNSWIndex(t, idx, 200)

	baseline := takeMemorySnapshot()

	for range iterations * operationsPerIteration {
		query := randomVector(64)
		_ = idx.Search(query, 10, nil)
	}

	final := takeMemorySnapshot()
	checkMemoryGrowth(t, baseline, final, "HNSW search operations")
}

func populateHNSWIndex(t *testing.T, idx *hnsw.Index, count int) {
	t.Helper()
	for i := range count {
		id := fmt.Sprintf("base-%d", i)
		vec := randomVector(64)
		if err := idx.Insert(id, vec, vectorgraphdb.DomainCode, vectorgraphdb.NodeTypeFile); err != nil {
			t.Fatalf("populate failed: %v", err)
		}
	}
}

// TestMemoryLeakBoundedOverflow tests for memory leaks in bounded overflow.
func TestMemoryLeakBoundedOverflow(t *testing.T) {
	t.Run("DrainOperations", testBoundedOverflowDrain)
	t.Run("FillAndClear", testBoundedOverflowFillClear)
}

func testBoundedOverflowDrain(t *testing.T) {
	baseline := takeMemorySnapshot()

	for range iterations {
		buf := concurrency.NewBoundedOverflow[*testPayload](100, concurrency.DropOldest)
		fillBoundedOverflow(buf)
		_ = buf.Drain()
		buf.Close()
	}

	final := takeMemorySnapshot()
	checkMemoryGrowth(t, baseline, final, "BoundedOverflow drain")
}

func testBoundedOverflowFillClear(t *testing.T) {
	buf := concurrency.NewBoundedOverflow[*testPayload](100, concurrency.DropOldest)
	defer buf.Close()

	baseline := takeMemorySnapshot()

	for range iterations {
		fillBoundedOverflow(buf)
		buf.Clear()
	}

	final := takeMemorySnapshot()
	checkMemoryGrowth(t, baseline, final, "BoundedOverflow fill/clear")
}

type testPayload struct {
	data []byte
}

func fillBoundedOverflow(buf *concurrency.BoundedOverflow[*testPayload]) {
	for range operationsPerIteration {
		buf.Add(&testPayload{data: make([]byte, 256)})
	}
}

// TestMemoryLeakAdaptiveChannel tests for memory leaks in adaptive channels.
func TestMemoryLeakAdaptiveChannel(t *testing.T) {
	t.Run("CloseOperations", testAdaptiveChannelClose)
	t.Run("SendReceiveCycle", testAdaptiveChannelSendReceive)
}

func testAdaptiveChannelClose(t *testing.T) {
	baseline := takeMemorySnapshot()
	goroutinesBefore := runtime.NumGoroutine()

	for range iterations {
		ctx, cancel := context.WithCancel(context.Background())
		ch := concurrency.NewAdaptiveChannelWithContext[int](ctx, defaultAdaptiveConfig())
		for j := range 10 {
			_ = ch.Send(j)
		}
		ch.Close()
		cancel()
	}

	time.Sleep(100 * time.Millisecond) // Allow goroutines to exit
	runtime.GC()

	goroutinesAfter := runtime.NumGoroutine()
	checkGoroutineLeak(t, goroutinesBefore, goroutinesAfter, "AdaptiveChannel close")

	final := takeMemorySnapshot()
	checkMemoryGrowth(t, baseline, final, "AdaptiveChannel close")
}

func testAdaptiveChannelSendReceive(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := concurrency.NewAdaptiveChannelWithContext[int](ctx, defaultAdaptiveConfig())
	defer ch.Close()

	baseline := takeMemorySnapshot()

	for range iterations {
		for j := range operationsPerIteration {
			_ = ch.Send(j)
		}
		for range operationsPerIteration {
			_, _ = ch.TryReceive()
		}
	}

	final := takeMemorySnapshot()
	checkMemoryGrowth(t, baseline, final, "AdaptiveChannel send/receive")
}

func defaultAdaptiveConfig() concurrency.AdaptiveChannelConfig {
	cfg := concurrency.DefaultAdaptiveChannelConfig()
	cfg.MaxOverflowSize = 1000
	return cfg
}

// TestMemoryLeakSearchCache tests for memory leaks in search cache eviction.
func TestMemoryLeakSearchCache(t *testing.T) {
	t.Run("EvictionCycle", testSearchCacheEviction)
	t.Run("InvalidateCycle", testSearchCacheInvalidate)
}

func testSearchCacheEviction(t *testing.T) {
	cfg := coordinator.CacheConfig{TTL: time.Hour, MaxSize: 50}
	cache := coordinator.NewSearchCache(cfg)

	baseline := takeMemorySnapshot()

	for i := range iterations {
		fillSearchCache(cache, i)
	}

	final := takeMemorySnapshot()
	checkMemoryGrowth(t, baseline, final, "SearchCache eviction")
}

func testSearchCacheInvalidate(t *testing.T) {
	cfg := coordinator.CacheConfig{TTL: time.Hour, MaxSize: 100}
	cache := coordinator.NewSearchCache(cfg)

	baseline := takeMemorySnapshot()

	for i := range iterations {
		fillSearchCache(cache, i)
		cache.Invalidate()
	}

	final := takeMemorySnapshot()
	checkMemoryGrowth(t, baseline, final, "SearchCache invalidate")
}

func fillSearchCache(cache *coordinator.SearchCache, iteration int) {
	for j := range operationsPerIteration {
		key := fmt.Sprintf("key-%d-%d", iteration, j)
		result := &search.SearchResult{TotalHits: int64(j)}
		cache.Set(key, result)
	}
}

// TestMemoryLeakWAL tests for memory leaks in WAL file operations.
func TestMemoryLeakWAL(t *testing.T) {
	t.Run("AppendCycle", testWALAppendCycle)
	t.Run("ReadCycle", testWALReadCycle)
}

func testWALAppendCycle(t *testing.T) {
	baseline := takeMemorySnapshot()

	for i := range iterations {
		dir := createTempWALDir(t, i)
		runWALAppendCycle(t, dir)
		os.RemoveAll(dir)
	}

	final := takeMemorySnapshot()
	checkMemoryGrowth(t, baseline, final, "WAL append cycle")
}

func runWALAppendCycle(t *testing.T, dir string) {
	t.Helper()
	cfg := concurrency.WALConfig{
		Dir:            dir,
		MaxSegmentSize: 1024 * 1024,
		SyncMode:       concurrency.SyncBatched,
		SyncInterval:   100 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wal, err := concurrency.NewWriteAheadLogWithContext(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}
	defer wal.Close()

	for j := range operationsPerIteration {
		entry := &concurrency.WALEntry{
			Type:    concurrency.EntryStateChange,
			Payload: []byte(fmt.Sprintf("data-%d", j)),
		}
		if _, err := wal.Append(entry); err != nil {
			t.Fatalf("append failed: %v", err)
		}
	}
}

func testWALReadCycle(t *testing.T) {
	dir := createTempWALDir(t, 0)
	defer os.RemoveAll(dir)

	cfg := concurrency.WALConfig{
		Dir:            dir,
		MaxSegmentSize: 1024 * 1024,
		SyncMode:       concurrency.SyncBatched,
		SyncInterval:   100 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	wal, err := concurrency.NewWriteAheadLogWithContext(ctx, cfg)
	if err != nil {
		t.Fatalf("failed to create WAL: %v", err)
	}

	populateWAL(t, wal)

	baseline := takeMemorySnapshot()

	for range iterations {
		_, _ = wal.ReadAll()
	}

	if err := wal.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	final := takeMemorySnapshot()
	checkMemoryGrowth(t, baseline, final, "WAL read cycle")
}

func populateWAL(t *testing.T, wal *concurrency.WriteAheadLog) {
	t.Helper()
	for j := range operationsPerIteration {
		entry := &concurrency.WALEntry{
			Type:    concurrency.EntryStateChange,
			Payload: []byte(fmt.Sprintf("data-%d", j)),
		}
		if _, err := wal.Append(entry); err != nil {
			t.Fatalf("populate WAL failed: %v", err)
		}
	}
}

func createTempWALDir(t *testing.T, iteration int) string {
	t.Helper()
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("wal-leak-test-%d-%d", time.Now().UnixNano(), iteration))
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	return dir
}

// TestMemoryLeakFinalizerTracking verifies finalizers are called properly.
func TestMemoryLeakFinalizerTracking(t *testing.T) {
	t.Run("TrackedObjectFinalizer", testTrackedObjectFinalizer)
}

func testTrackedObjectFinalizer(t *testing.T) {
	var finalizerCalled atomic.Int32

	for range iterations {
		obj := &trackedObject{id: 1}
		runtime.SetFinalizer(obj, func(o *trackedObject) {
			finalizerCalled.Add(1)
		})
		obj = nil
		_ = obj // Use to avoid compiler warning
	}

	runtime.GC()
	runtime.GC()
	time.Sleep(50 * time.Millisecond)

	called := finalizerCalled.Load()
	if called == 0 {
		t.Error("finalizers were never called")
	}
	t.Logf("finalizers called: %d/%d", called, iterations)
}

type trackedObject struct {
	id int
}

// TestMemoryLeakGoroutineDetection tests for goroutine leaks.
func TestMemoryLeakGoroutineDetection(t *testing.T) {
	t.Run("AdaptiveChannelGoroutines", testAdaptiveChannelGoroutines)
	t.Run("WALGoroutines", testWALGoroutines)
}

func testAdaptiveChannelGoroutines(t *testing.T) {
	goroutinesBefore := runtime.NumGoroutine()

	for range iterations {
		ctx, cancel := context.WithCancel(context.Background())
		ch := concurrency.NewAdaptiveChannelWithContext[int](ctx, defaultAdaptiveConfig())
		ch.Close()
		cancel()
	}

	time.Sleep(200 * time.Millisecond)
	goroutinesAfter := runtime.NumGoroutine()
	checkGoroutineLeak(t, goroutinesBefore, goroutinesAfter, "AdaptiveChannel goroutines")
}

func testWALGoroutines(t *testing.T) {
	goroutinesBefore := runtime.NumGoroutine()

	for i := range iterations {
		dir := createTempWALDir(t, i)
		ctx, cancel := context.WithCancel(context.Background())

		cfg := concurrency.WALConfig{
			Dir:            dir,
			MaxSegmentSize: 1024 * 1024,
			SyncMode:       concurrency.SyncPeriodic,
			SyncInterval:   50 * time.Millisecond,
		}

		wal, err := concurrency.NewWriteAheadLogWithContext(ctx, cfg)
		if err != nil {
			cancel()
			os.RemoveAll(dir)
			t.Fatalf("failed to create WAL: %v", err)
		}

		if err := wal.Close(); err != nil {
			t.Logf("WAL close error: %v", err)
		}
		cancel()
		os.RemoveAll(dir)
	}

	time.Sleep(200 * time.Millisecond)
	goroutinesAfter := runtime.NumGoroutine()
	checkGoroutineLeak(t, goroutinesBefore, goroutinesAfter, "WAL goroutines")
}

func checkGoroutineLeak(t *testing.T, before, after int, label string) {
	t.Helper()
	leaked := after - before
	// Allow up to 2 goroutines difference for runtime fluctuation
	if leaked > 2 {
		t.Errorf("%s: leaked %d goroutines (before: %d, after: %d)",
			label, leaked, before, after)
	}
}

// randomVector generates a random float32 vector.
func randomVector(dim int) []float32 {
	vec := make([]float32, dim)
	for i := range vec {
		vec[i] = rand.Float32()
	}
	return vec
}
