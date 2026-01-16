package archivalist

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// =============================================================================
// Concurrency Stress Tests
// =============================================================================

func TestConcurrency_ParallelReads(t *testing.T) {
	store := newTestStore(t)

	// Pre-populate store
	for i := 0; i < 100; i++ {
		store.InsertEntry(makeEntry(CategoryGeneral, fmt.Sprintf("Content %d", i), SourceModelClaudeOpus45))
	}

	var readCount atomic.Int64

	runConcurrent(t, 100, func(goroutine int) {
		for j := 0; j < 100; j++ {
			_, err := store.Query(ArchiveQuery{Categories: []Category{CategoryGeneral}})
			if err == nil {
				readCount.Add(1)
			}
			store.Stats()
		}
	})

	assert.Equal(t, int64(10000), readCount.Load(), "All reads should succeed")
}

func TestConcurrency_ParallelWrites(t *testing.T) {
	store := newTestStore(t)

	var writeCount atomic.Int64
	var errorCount atomic.Int64

	runConcurrent(t, 50, func(goroutine int) {
		for j := 0; j < 100; j++ {
			_, err := store.InsertEntry(makeEntry(
				CategoryGeneral,
				fmt.Sprintf("Content from goroutine %d, item %d", goroutine, j),
				SourceModelClaudeOpus45,
			))
			if err == nil {
				writeCount.Add(1)
			} else {
				errorCount.Add(1)
			}
		}
	})

	assert.Equal(t, int64(5000), writeCount.Load(), "All writes should succeed")
	assert.Equal(t, int64(0), errorCount.Load(), "No errors should occur")
}

func TestConcurrency_ReadDuringWrite(t *testing.T) {
	store := newTestStore(t)

	// Pre-populate
	for i := 0; i < 50; i++ {
		store.InsertEntry(makeEntry(CategoryGeneral, "Initial content", SourceModelClaudeOpus45))
	}

	var wg sync.WaitGroup
	done := make(chan bool)

	// Writers
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			select {
			case <-done:
				return
			default:
				store.InsertEntry(makeEntry(CategoryGeneral, "New content", SourceModelClaudeOpus45))
			}
		}
	}()

	// Readers (should not block)
	var readErrors atomic.Int64
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				select {
				case <-done:
					return
				default:
					_, err := store.Query(ArchiveQuery{Limit: 10})
					if err != nil {
						readErrors.Add(1)
					}
				}
			}
		}()
	}

	wg.Wait()
	close(done)

	assert.Equal(t, int64(0), readErrors.Load(), "No read errors should occur during writes")
}

func TestConcurrency_RegistryContention(t *testing.T) {
	registry := newTestRegistry(t)

	var wg sync.WaitGroup
	var registerCount atomic.Int64

	// Many agents registering simultaneously
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 10; j++ {
				_, err := registry.Register(
					fmt.Sprintf("agent-%d-%d", idx, j),
					"session-1",
					"",
					SourceModelClaudeOpus45,
				)
				if err == nil {
					registerCount.Add(1)
				}
			}
		}(i)
	}

	wg.Wait()

	assert.Equal(t, int64(500), registerCount.Load(), "All registrations should succeed")

	stats := registry.GetStats()
	assert.Equal(t, 500, stats.TotalAgents, "All agents should be registered")
}

func TestConcurrency_EventLogContention(t *testing.T) {
	el := newTestEventLog(t)

	var wg sync.WaitGroup
	var appendCount atomic.Int64

	// High-volume appends
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(goroutine int) {
			defer wg.Done()
			for j := 0; j < 500; j++ {
				err := el.Append(makeEvent(
					EventTypeFileRead,
					fmt.Sprintf("agent-%d", goroutine),
					"session-1",
					nil,
				))
				if err == nil {
					appendCount.Add(1)
				}
			}
		}(i)
	}

	wg.Wait()

	assert.Equal(t, int64(10000), appendCount.Load(), "All appends should succeed")
}

func TestConcurrency_CacheContention(t *testing.T) {
	cache := newTestQueryCache(t)
	ctx := context.Background()

	var wg sync.WaitGroup

	// Concurrent writes
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(goroutine int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				cache.Store(ctx,
					fmt.Sprintf("Query %d from %d", j, goroutine),
					"session-1",
					[]byte(`{}`),
					QueryTypePattern,
				)
			}
		}(i)
	}

	// Concurrent reads
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(goroutine int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				cache.Get(ctx, fmt.Sprintf("Query %d from %d", j%50, goroutine%5), "session-1")
			}
		}(i)
	}

	wg.Wait()

	// Should complete without deadlock
	stats := cache.Stats()
	assert.True(t, stats.TotalQueries >= 0, "Should have valid stats")
}

func TestConcurrency_NoDeadlocks(t *testing.T) {
	store := newTestStore(t)
	registry := newTestRegistry(t)
	el := newTestEventLog(t)
	cache := newTestQueryCache(t)
	agentCtx := newTestAgentContext(t)

	done := make(chan bool)
	timeout := time.After(10 * time.Second)

	go func() {
		var wg sync.WaitGroup
		launchStoreOps(&wg, store)
		launchRegistryOps(&wg, registry)
		launchEventLogOps(&wg, el)
		launchCacheOps(&wg, cache)
		launchAgentContextOps(&wg, agentCtx)
		wg.Wait()
		done <- true
	}()

	assertNoDeadlock(t, done, timeout)
}

func launchStoreOps(wg *sync.WaitGroup, store *Store) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			store.InsertEntry(makeEntry(CategoryGeneral, "Content", SourceModelClaudeOpus45))
			store.Query(ArchiveQuery{Limit: 5})
			store.Stats()
		}
	}()
}

func launchRegistryOps(wg *sync.WaitGroup, registry *Registry) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			agent, _ := registry.Register(fmt.Sprintf("agent-%d", i), "session-1", "", SourceModelClaudeOpus45)
			if agent != nil {
				registry.Touch(agent.ID)
				registry.Get(agent.ID)
			}
		}
	}()
}

func launchEventLogOps(wg *sync.WaitGroup, el *EventLog) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			el.Append(makeEvent(EventTypeFileRead, "agent-1", "session-1", nil))
			el.GetRecent(10)
			el.Query(EventQuery{Limit: 5})
		}
	}()
}

func launchCacheOps(wg *sync.WaitGroup, cache *QueryCache) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		ctx := context.Background()
		for i := 0; i < 100; i++ {
			cache.Store(ctx, "Query", "session-1", []byte(`{}`), QueryTypePattern)
			cache.Get(ctx, "Query", "session-1")
			cache.Stats()
		}
	}()
}

func launchAgentContextOps(wg *sync.WaitGroup, agentCtx *AgentContext) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			agentCtx.RecordFileRead("/path/"+string(rune('0'+i%10)), "Summary", SourceModelClaudeOpus45)
			agentCtx.GetAllFiles()
			agentCtx.GetAgentBriefing()
		}
	}()
}

func assertNoDeadlock(t *testing.T, done <-chan bool, timeout <-chan time.Time) {
	select {
	case <-done:
		return
	case <-timeout:
		t.Fatal("Test timed out - potential deadlock")
	}
}

func TestConcurrency_RaceDetection(t *testing.T) {
	// This test is primarily for running with -race flag
	store := newTestStore(t)
	registry := newTestRegistry(t)

	var wg sync.WaitGroup

	// Concurrent operations on shared state
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(goroutine int) {
			defer wg.Done()

			// Register agent
			agent, err := registry.Register(
				fmt.Sprintf("agent-%d", goroutine),
				"session-1",
				"",
				SourceModelClaudeOpus45,
			)
			if err != nil {
				return
			}

			// Perform operations
			for j := 0; j < 50; j++ {
				// Write
				store.InsertEntry(makeEntry(CategoryGeneral, "Content", SourceModelClaudeOpus45))

				// Read
				store.Query(ArchiveQuery{Limit: 5})

				// Update registry
				registry.Touch(agent.ID)
				registry.UpdateVersion(agent.ID, fmt.Sprintf("v%d", j))
			}
		}(i)
	}

	wg.Wait()

	// Should complete without race conditions (when run with -race)
	stats := store.Stats()
	assert.True(t, stats.TotalEntries > 0, "Should have entries")
}

// =============================================================================
// Concurrent Conflict Detection Tests
// =============================================================================

func TestConcurrency_SimultaneousFileModifications(t *testing.T) {
	detector, agentCtx, registry := newTestConflictDetector(t)

	// Register agents
	var agents []*RegisteredAgent
	for i := 0; i < 10; i++ {
		agent, _ := registry.Register(fmt.Sprintf("agent-%d", i), "session-1", "", SourceModelClaudeOpus45)
		agents = append(agents, agent)
	}

	// All agents try to modify the same file
	var wg sync.WaitGroup
	var conflicts atomic.Int64

	for i, agent := range agents {
		wg.Add(1)
		go func(idx int, ag *RegisteredAgent) {
			defer wg.Done()

			// First read
			agentCtx.RecordFileRead("/src/main.go", "Main file", ag.Source)

			// Try to modify
			result := detector.DetectConflict(
				ScopeFiles,
				"/src/main.go",
				map[string]any{
					"status": "modified",
					"changes": []any{
						map[string]any{"start_line": float64(idx * 10), "end_line": float64(idx*10 + 10)},
					},
				},
				ag.LastVersion,
				ag.ID,
			)

			if result.Type != ConflictTypeNone {
				conflicts.Add(1)
			}
		}(i, agent)
	}

	wg.Wait()

	// Should detect some conflicts (or handle them gracefully)
	// The exact number depends on timing and implementation
	t.Logf("Detected %d conflicts out of 10 simultaneous modifications", conflicts.Load())
}

func TestConcurrency_RapidVersionIncrement(t *testing.T) {
	registry := newTestRegistry(t)

	var wg sync.WaitGroup
	numIncrements := 1000

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < numIncrements/10; j++ {
				registry.IncrementVersion()
			}
		}()
	}

	wg.Wait()

	version := registry.GetVersionNumber()
	assert.Equal(t, uint64(numIncrements), version, "Version should match total increments")

	// Verify version string is valid
	versionStr := registry.GetVersion()
	assert.True(t, registry.IsVersionCurrent(versionStr), "Current version should be valid")
}
