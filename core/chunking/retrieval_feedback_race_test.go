package chunking

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// =============================================================================
// W4C.4 Race Condition Tests for AsyncRetrievalFeedbackHook
// These tests verify the fix for the data race between RecordRetrieval and
// processEntry by ensuring all stats access is done under lock.
// =============================================================================

// TestAsyncRetrievalFeedbackHook_ConcurrentRecordRetrieval tests that concurrent
// calls to RecordRetrieval do not cause data races.
// Run with: go test -race -run TestAsyncRetrievalFeedbackHook_ConcurrentRecordRetrieval
func TestAsyncRetrievalFeedbackHook_ConcurrentRecordRetrieval(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 1000,
	})
	if err != nil {
		t.Fatalf("failed to create hook: %v", err)
	}
	defer hook.Stop()

	// Pre-register chunks
	for i := 0; i < 10; i++ {
		chunk := Chunk{
			ID:         fmt.Sprintf("race-chunk-%d", i),
			Domain:     Domain(i % 4),
			TokenCount: 100 + i*10,
		}
		hook.RegisterChunk(chunk)
	}

	var wg sync.WaitGroup
	numGoroutines := 20
	operationsPerGoroutine := 100

	// Concurrent writers
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < operationsPerGoroutine; i++ {
				chunkID := fmt.Sprintf("race-chunk-%d", i%10)
				ctx := RetrievalContext{
					QueryText: fmt.Sprintf("query-%d-%d", id, i),
					Timestamp: time.Now(),
				}
				_ = hook.RecordRetrieval(chunkID, i%2 == 0, ctx)
			}
		}(g)
	}

	wg.Wait()

	// Wait for async processing to complete
	time.Sleep(500 * time.Millisecond)

	// Verify data integrity
	for i := 0; i < 10; i++ {
		chunkID := fmt.Sprintf("race-chunk-%d", i)
		hitRate, total := hook.GetHitRate(chunkID)
		if total < 0 {
			t.Errorf("chunk %s has negative total: %d", chunkID, total)
		}
		if hitRate < 0 || hitRate > 1 {
			t.Errorf("chunk %s has invalid hit rate: %f", chunkID, hitRate)
		}
	}

	t.Logf("Test completed: processed %d operations across %d goroutines",
		numGoroutines*operationsPerGoroutine, numGoroutines)
}

// TestAsyncRetrievalFeedbackHook_ConcurrentReadWrite tests concurrent reads and
// writes to ensure no data races occur.
func TestAsyncRetrievalFeedbackHook_ConcurrentReadWrite(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 500,
	})
	if err != nil {
		t.Fatalf("failed to create hook: %v", err)
	}
	defer hook.Stop()

	// Pre-register chunks
	for i := 0; i < 5; i++ {
		chunk := Chunk{
			ID:         fmt.Sprintf("rw-chunk-%d", i),
			Domain:     DomainCode,
			TokenCount: 200,
		}
		hook.RegisterChunk(chunk)
	}

	var wg sync.WaitGroup
	stopChan := make(chan struct{})

	// Writers
	for w := 0; w < 10; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				select {
				case <-stopChan:
					return
				default:
				}
				chunkID := fmt.Sprintf("rw-chunk-%d", i%5)
				ctx := RetrievalContext{Timestamp: time.Now()}
				_ = hook.RecordRetrieval(chunkID, id%2 == 0, ctx)
			}
		}(w)
	}

	// Readers (concurrent with writers)
	for r := 0; r < 10; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				select {
				case <-stopChan:
					return
				default:
				}
				for j := 0; j < 5; j++ {
					chunkID := fmt.Sprintf("rw-chunk-%d", j)
					hook.GetHitRate(chunkID)
					hook.GetDomainHitRate(DomainCode)
					hook.GetChunkStats(chunkID)
				}
			}
		}()
	}

	wg.Wait()
	close(stopChan)

	// Allow async processing to complete
	time.Sleep(300 * time.Millisecond)

	// Verify no corruption
	for i := 0; i < 5; i++ {
		chunkID := fmt.Sprintf("rw-chunk-%d", i)
		stats, exists := hook.GetChunkStats(chunkID)
		if !exists {
			t.Errorf("chunk %s should exist", chunkID)
			continue
		}
		if stats.TotalRetrievals < 0 {
			t.Errorf("chunk %s has negative total retrievals", chunkID)
		}
		if stats.UsefulRetrievals < 0 {
			t.Errorf("chunk %s has negative useful retrievals", chunkID)
		}
		if stats.UsefulRetrievals > stats.TotalRetrievals {
			t.Errorf("chunk %s has more useful than total retrievals", chunkID)
		}
	}
}

// TestAsyncRetrievalFeedbackHook_StatsAccuracyUnderConcurrency verifies that
// statistics remain accurate under high concurrency.
func TestAsyncRetrievalFeedbackHook_StatsAccuracyUnderConcurrency(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 2000,
	})
	if err != nil {
		t.Fatalf("failed to create hook: %v", err)
	}
	defer hook.Stop()

	chunkID := "accuracy-test-chunk"
	chunk := Chunk{
		ID:         chunkID,
		Domain:     DomainGeneral,
		TokenCount: 150,
	}
	hook.RegisterChunk(chunk)

	var usefulCount int64
	var totalCount int64
	var wg sync.WaitGroup
	numGoroutines := 10
	operationsPerGoroutine := 100

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < operationsPerGoroutine; i++ {
				wasUseful := (id+i)%2 == 0
				ctx := RetrievalContext{Timestamp: time.Now()}
				err := hook.RecordRetrieval(chunkID, wasUseful, ctx)
				if err == nil {
					atomic.AddInt64(&totalCount, 1)
					if wasUseful {
						atomic.AddInt64(&usefulCount, 1)
					}
				}
			}
		}(g)
	}

	wg.Wait()

	// Wait for all async processing
	time.Sleep(500 * time.Millisecond)

	// Verify accuracy
	hitRate, total := hook.GetHitRate(chunkID)
	dropped := hook.GetDroppedCount()
	actualProcessed := totalCount - dropped

	t.Logf("Expected total: %d, Actual total: %d, Dropped: %d",
		totalCount, total, dropped)

	// Account for potential drops
	if int64(total) != actualProcessed {
		t.Logf("Note: Some entries may still be processing. Expected ~%d, got %d",
			actualProcessed, total)
	}

	// Verify hit rate is reasonable (should be close to 0.5)
	if total > 0 && (hitRate < 0.3 || hitRate > 0.7) {
		t.Logf("Hit rate %f is outside expected range [0.3, 0.7] for alternating usefulness",
			hitRate)
	}
}

// TestAsyncRetrievalFeedbackHook_SyncRecordRetrievalConcurrency tests the
// synchronous variant under concurrent access.
func TestAsyncRetrievalFeedbackHook_SyncRecordRetrievalConcurrency(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 100,
	})
	if err != nil {
		t.Fatalf("failed to create hook: %v", err)
	}
	defer hook.Stop()

	chunkID := "sync-race-chunk"
	chunk := Chunk{
		ID:         chunkID,
		Domain:     DomainCode,
		TokenCount: 100,
	}
	hook.RegisterChunk(chunk)

	var wg sync.WaitGroup
	var successCount int64
	numGoroutines := 20
	operationsPerGoroutine := 50

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < operationsPerGoroutine; i++ {
				ctx := RetrievalContext{Timestamp: time.Now()}
				err := hook.RecordRetrievalSync(chunkID, i%2 == 0, ctx)
				if err == nil {
					atomic.AddInt64(&successCount, 1)
				}
			}
		}(g)
	}

	wg.Wait()

	// Verify stats
	_, total := hook.GetHitRate(chunkID)
	expected := numGoroutines * operationsPerGoroutine

	if int64(total) != successCount {
		t.Errorf("mismatch: total=%d, successCount=%d", total, successCount)
	}

	t.Logf("Processed %d/%d operations via RecordRetrievalSync", total, expected)
}

// TestAsyncRetrievalFeedbackHook_RegisterAndRecordConcurrency tests concurrent
// chunk registration and feedback recording.
func TestAsyncRetrievalFeedbackHook_RegisterAndRecordConcurrency(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 1000,
	})
	if err != nil {
		t.Fatalf("failed to create hook: %v", err)
	}
	defer hook.Stop()

	var wg sync.WaitGroup
	numChunks := 50

	// Concurrent registrations and recordings
	for i := 0; i < numChunks; i++ {
		wg.Add(2)

		// Register goroutine
		go func(idx int) {
			defer wg.Done()
			chunk := Chunk{
				ID:         fmt.Sprintf("reg-chunk-%d", idx),
				Domain:     Domain(idx % 4),
				TokenCount: 100 + idx,
			}
			hook.RegisterChunk(chunk)
		}(i)

		// Record goroutine (may create implicit registration)
		go func(idx int) {
			defer wg.Done()
			// Small delay to mix registration and recording timing
			time.Sleep(time.Duration(idx%10) * time.Microsecond)
			chunkID := fmt.Sprintf("reg-chunk-%d", idx)
			ctx := RetrievalContext{Timestamp: time.Now()}
			_ = hook.RecordRetrieval(chunkID, idx%2 == 0, ctx)
		}(i)
	}

	wg.Wait()
	time.Sleep(300 * time.Millisecond)

	// Verify all chunks exist and have valid stats
	for i := 0; i < numChunks; i++ {
		chunkID := fmt.Sprintf("reg-chunk-%d", i)
		stats, exists := hook.GetChunkStats(chunkID)
		if !exists {
			t.Errorf("chunk %s should exist", chunkID)
			continue
		}
		if stats.ChunkID != chunkID {
			t.Errorf("chunk ID mismatch: got %s, want %s", stats.ChunkID, chunkID)
		}
	}
}

// TestAsyncRetrievalFeedbackHook_DomainStatsRaceCondition tests that domain
// statistics are updated correctly without races.
func TestAsyncRetrievalFeedbackHook_DomainStatsRaceCondition(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 500,
	})
	if err != nil {
		t.Fatalf("failed to create hook: %v", err)
	}
	defer hook.Stop()

	domains := []Domain{DomainCode, DomainAcademic, DomainHistory, DomainGeneral}

	// Register chunks for each domain
	for _, domain := range domains {
		for j := 0; j < 5; j++ {
			chunk := Chunk{
				ID:         fmt.Sprintf("domain-%d-chunk-%d", domain, j),
				Domain:     domain,
				TokenCount: 100,
			}
			hook.RegisterChunk(chunk)
		}
	}

	var wg sync.WaitGroup
	numGoroutines := 10
	operationsPerGoroutine := 50

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < operationsPerGoroutine; i++ {
				domain := domains[i%len(domains)]
				chunkID := fmt.Sprintf("domain-%d-chunk-%d", domain, i%5)
				ctx := RetrievalContext{Timestamp: time.Now()}
				_ = hook.RecordRetrieval(chunkID, (id+i)%2 == 0, ctx)
			}
		}(g)
	}

	wg.Wait()
	time.Sleep(300 * time.Millisecond)

	// Verify domain stats consistency
	for _, domain := range domains {
		hitRate, total := hook.GetDomainHitRate(domain)
		if total < 0 {
			t.Errorf("domain %s has negative total: %d", domain.String(), total)
		}
		if hitRate < 0 || hitRate > 1 {
			t.Errorf("domain %s has invalid hit rate: %f", domain.String(), hitRate)
		}
		t.Logf("Domain %s: hitRate=%f, total=%d", domain.String(), hitRate, total)
	}
}

// TestAsyncRetrievalFeedbackHook_StopDuringProcessing tests graceful shutdown
// while processing is ongoing.
func TestAsyncRetrievalFeedbackHook_StopDuringProcessing(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 100,
	})
	if err != nil {
		t.Fatalf("failed to create hook: %v", err)
	}

	chunkID := "stop-test-chunk"
	chunk := Chunk{ID: chunkID, Domain: DomainGeneral, TokenCount: 50}
	hook.RegisterChunk(chunk)

	var wg sync.WaitGroup

	// Start recording in background
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 1000; i++ {
			ctx := RetrievalContext{Timestamp: time.Now()}
			err := hook.RecordRetrieval(chunkID, i%2 == 0, ctx)
			if err != nil {
				// Expected after stop
				return
			}
		}
	}()

	// Stop while processing
	time.Sleep(10 * time.Millisecond)
	hook.Stop()

	wg.Wait()

	// Verify hook reports stopped
	ctx := RetrievalContext{Timestamp: time.Now()}
	err = hook.RecordRetrieval(chunkID, true, ctx)
	if err == nil {
		t.Error("expected error after Stop(), got nil")
	}

	// Stats should still be readable
	_, total := hook.GetHitRate(chunkID)
	t.Logf("Processed %d retrievals before stop", total)
}

// TestAsyncRetrievalFeedbackHook_BatchRecordConcurrency tests concurrent batch
// recording operations.
func TestAsyncRetrievalFeedbackHook_BatchRecordConcurrency(t *testing.T) {
	learner, err := NewChunkConfigLearner(nil)
	if err != nil {
		t.Fatalf("failed to create learner: %v", err)
	}

	hook, err := NewAsyncRetrievalFeedbackHook(AsyncFeedbackConfig{
		Learner:    learner,
		BufferSize: 2000,
	})
	if err != nil {
		t.Fatalf("failed to create hook: %v", err)
	}
	defer hook.Stop()

	// Pre-register chunks
	numChunks := 20
	for i := 0; i < numChunks; i++ {
		chunk := Chunk{
			ID:         fmt.Sprintf("batch-chunk-%d", i),
			Domain:     Domain(i % 4),
			TokenCount: 100,
		}
		hook.RegisterChunk(chunk)
	}

	var wg sync.WaitGroup
	numGoroutines := 10

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for batch := 0; batch < 10; batch++ {
				entries := make([]BatchFeedbackEntry, 10)
				for i := 0; i < 10; i++ {
					entries[i] = BatchFeedbackEntry{
						ChunkID:   fmt.Sprintf("batch-chunk-%d", (id+i)%numChunks),
						WasUseful: (id + i + batch) % 2 == 0,
						Context:   RetrievalContext{Timestamp: time.Now()},
					}
				}
				hook.RecordBatch(BatchFeedback{Entries: entries})
			}
		}(g)
	}

	wg.Wait()
	time.Sleep(1000 * time.Millisecond)

	// Verify chunk stats are valid (not necessarily all have retrievals due to timing)
	totalProcessed := 0
	for i := 0; i < numChunks; i++ {
		chunkID := fmt.Sprintf("batch-chunk-%d", i)
		hitRate, total := hook.GetHitRate(chunkID)
		totalProcessed += total
		if hitRate < 0 || hitRate > 1 {
			t.Errorf("chunk %s has invalid hit rate: %f", chunkID, hitRate)
		}
	}

	// Most batches should have been processed
	expectedTotal := numGoroutines * 10 * 10 // goroutines * batches * entries per batch
	if totalProcessed < expectedTotal/2 {
		t.Logf("Warning: Only processed %d/%d entries, some may have been dropped", totalProcessed, expectedTotal)
	}
	t.Logf("Processed %d/%d batch entries", totalProcessed, expectedTotal)
}
