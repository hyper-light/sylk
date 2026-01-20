# Sylk Performance Tuning Guide

This guide provides recommendations for tuning Sylk's performance across its core subsystems. Each section includes configuration options, recommended values, and code examples.

## Table of Contents

1. [VectorGraphDB Tuning](#1-vectorgraphdb-tuning)
2. [Bleve Search Tuning](#2-bleve-search-tuning)
3. [Knowledge Graph Optimization](#3-knowledge-graph-optimization)
4. [Concurrency Configuration](#4-concurrency-configuration)
5. [Memory Management](#5-memory-management)
6. [Code Quality Patterns Applied](#6-code-quality-patterns-applied)
7. [Monitoring and Profiling](#7-monitoring-and-profiling)

---

## 1. VectorGraphDB Tuning

VectorGraphDB uses HNSW (Hierarchical Navigable Small World) for approximate nearest neighbor search.

### HNSW Parameters

| Parameter | Default | Description | Tuning Guidance |
|-----------|---------|-------------|-----------------|
| `M` | 16 | Max connections per node | Higher = better recall, more memory |
| `EfConstruction` | 200 | Candidate list size during insertion | Higher = better index quality, slower builds |
| `EfSearch` | 50 | Candidate list size during search | Higher = better recall, slower queries |
| `LevelMult` | 0.36067977499789996 | Level probability multiplier (1/ln(M)) | Rarely needs adjustment |

```go
import "github.com/adalundhe/sylk/core/vectorgraphdb/hnsw"

// High-recall configuration (slower but more accurate)
highRecallConfig := hnsw.Config{
    M:           32,          // More connections
    EfConstruct: 400,         // Larger construction candidate list
    EfSearch:    200,         // Larger search candidate list
    Dimension:   768,         // Embedding dimension
}

// Balanced configuration (default, good for most cases)
balancedConfig := hnsw.Config{
    M:           16,          // Default
    EfConstruct: 200,         // Default
    EfSearch:    50,          // Default
    Dimension:   768,
}

// Fast search configuration (lower recall, faster queries)
fastConfig := hnsw.Config{
    M:           8,           // Fewer connections
    EfConstruct: 100,         // Smaller candidate list
    EfSearch:    20,          // Minimal search overhead
    Dimension:   768,
}

index := hnsw.New(balancedConfig)
```

### Batch Operations

Use `DeleteBatch` for bulk deletions to minimize lock acquisition overhead:

```go
// Inefficient: Multiple lock acquisitions
for _, id := range idsToDelete {
    index.Delete(id)  // Lock/unlock for each call
}

// Efficient: Single lock acquisition
err := index.DeleteBatch(idsToDelete)  // One lock for all deletions
if err != nil {
    // Handle error (atomic: if one fails, none are deleted)
}
```

### NodeStore Caching

The `VersionedNodeStore` uses an LRU cache for version lookups:

```go
import "github.com/adalundhe/sylk/core/vectorgraphdb"

// Default: 10,000 entries
store := vectorgraphdb.NewVersionedNodeStore(nodeStore)

// Custom size for high-throughput scenarios
store := vectorgraphdb.NewVersionedNodeStoreWithSize(nodeStore, 50000)
```

**Configuration Constants:**
- `DefaultVersionCacheSize = 10000` - Default LRU cache entries

### Database Indexes

VectorGraphDB creates the following indexes for query performance:

```sql
-- Node indexes
CREATE INDEX idx_nodes_domain_type ON nodes(domain, node_type);
CREATE INDEX idx_nodes_path ON nodes(path) WHERE path IS NOT NULL;
CREATE INDEX idx_nodes_updated_at ON nodes(updated_at);

-- Edge indexes
CREATE INDEX idx_edges_source ON edges(source_id, edge_type);
CREATE INDEX idx_edges_target ON edges(target_id, edge_type);
CREATE INDEX idx_edges_type_domain ON edges(edge_type, source_id);
```

---

## 2. Bleve Search Tuning

### Async Indexing Queue Configuration

The `AsyncIndexQueue` batches operations for improved throughput:

```go
import "github.com/adalundhe/sylk/core/search/bleve"

config := bleve.AsyncIndexQueueConfig{
    MaxQueueSize:  10000,                  // Max pending operations
    BatchSize:     100,                    // Operations per batch commit
    FlushInterval: 100 * time.Millisecond, // Max time before flush
    Workers:       2,                      // Processing goroutines
}

// For high-throughput indexing
highThroughputConfig := bleve.AsyncIndexQueueConfig{
    MaxQueueSize:  50000,
    BatchSize:     500,
    FlushInterval: 50 * time.Millisecond,
    Workers:       4,
}
```

### Batch Size Optimization

Batch sizes are validated to prevent OOM and excessive commit overhead:

```go
import "github.com/adalundhe/sylk/core/search/bleve"

// Batch size bounds
const (
    MinBatchSize     = 10    // Minimum to prevent excessive commits
    MaxBatchSize     = 10000 // Maximum to prevent OOM
    DefaultBatchSize = 100   // Balanced default
)

// ValidateBatchSize clamps to valid range
validSize := bleve.ValidateBatchSize(requestedSize)
```

### Field Loading Strategies

Use selective field loading to reduce memory usage:

```go
import "github.com/adalundhe/sylk/core/search/bleve"

// MetadataOnlyFieldConfig - Most performant, only essential fields
metadataConfig := bleve.MetadataOnlyFieldConfig()
// Loads: path, type, language, modified_at

// DefaultFieldConfig - Recommended default, excludes content
defaultConfig := bleve.DefaultFieldConfig()
// Loads: path, type, title, modified_at, indexed_at, language,
//        symbols, imports, checksum, git_commit, comments

// FullFieldConfig - When content is needed
fullConfig := bleve.FullFieldConfig()
// Loads: All fields including content

// Search with specific config
result, err := indexManager.SearchWithFields(ctx, req, metadataConfig)

// Lazy-load content only when needed
if needContent {
    content, err := indexManager.GetDocumentContent(ctx, docID)
}
```

### Cache Configuration with Pagination

Cache keys should include pagination parameters to avoid returning stale partial results:

```go
// Good: Include pagination in cache key
cacheKey := fmt.Sprintf("search:%s:offset=%d:limit=%d", query, offset, limit)

// Bad: Cache key without pagination
cacheKey := fmt.Sprintf("search:%s", query)  // Will return wrong subset
```

---

## 3. Knowledge Graph Optimization

### Entity Linker Token Matching

Use `TokenSet` for O(1) fuzzy matching pre-filtering:

```go
import "github.com/adalundhe/sylk/core/knowledge"

// Create TokenSet from identifier (splits camelCase, snake_case)
tokenSet := knowledge.TokenSetFromIdentifier("getUserProfile")
// Tokens: ["get", "user", "profile"]

// O(1) membership test
tokenSet.Contains("user")  // true

// Quick rejection before expensive fuzzy matching
if tokenSet.QuickReject(candidateSet, 0.5) {
    // Definitely not a match, skip expensive comparison
    continue
}

// Jaccard similarity for candidate filtering
similarity := tokenSet.Overlap(candidateSet)

// Pre-filter candidates in bulk
matches := knowledge.PrefilterCandidates(
    queryTokens,
    candidates,
    getTokenSetFunc,
    0.3, // min overlap threshold
)
```

**TokenizerConfig options:**
```go
// Default: General text tokenization
defaultConfig := knowledge.DefaultTokenizerConfig()

// Code: Splits camelCase and snake_case
codeConfig := knowledge.CodeTokenizerConfig()

// N-grams: Character-level similarity
ngramConfig := knowledge.NGramTokenizerConfig(3)
```

### Levenshtein Threshold Configuration

Use threshold-based early exit for fuzzy matching:

```go
// Early exit when distance exceeds threshold
func LevenshteinWithThreshold(s1, s2 string, threshold int) int {
    // Returns -1 if distance > threshold (early exit)
    // This avoids computing full edit distance for non-matches
}

// Usage: Set threshold based on string lengths
maxErrors := len(query) / 4  // Allow ~25% errors
distance := LevenshteinWithThreshold(query, candidate, maxErrors)
if distance < 0 {
    // Exceeded threshold, skip this candidate
    continue
}
```

### Rule Binding Memoization with EdgeIndex

The `RuleEvaluator` uses memoization and edge indexing:

```go
import "github.com/adalundhe/sylk/core/knowledge/inference"

// EdgeIndex provides O(1) lookups
type EdgeIndex struct {
    bySource     map[string][]Edge  // source -> edges
    byTarget     map[string][]Edge  // target -> edges
    byPredicate  map[string][]Edge  // predicate -> edges
    bySourcePred map[string][]Edge  // source:predicate -> edges
    byTargetPred map[string][]Edge  // target:predicate -> edges
}

// Memoization cache cleared per evaluation session
evaluator := inference.NewRuleEvaluator()
results := evaluator.EvaluateRule(ctx, rule, edges)
// matchCache is cleared at start of each EvaluateRule call
```

---

## 4. Concurrency Configuration

### GoroutineScope Sizing and Tracking

```go
import "github.com/adalundhe/sylk/core/concurrency"

// Create scope with budget control
budget := concurrency.NewGoroutineBudget(100)  // Max 100 goroutines
scope := concurrency.NewGoroutineScope(ctx, "agent-id", budget)

// Configure max lifetime (prevents runaway goroutines)
scope.SetMaxLifetime(5 * time.Minute)  // Default

// Launch work with timeout
err := scope.Go("task-description", 30*time.Second, func(ctx context.Context) error {
    // Work here respects context cancellation
    return nil
})

// Graceful shutdown with leak detection
err := scope.Shutdown(
    30*time.Second,  // Grace period
    60*time.Second,  // Hard deadline
)
// Returns *GoroutineLeakError if goroutines didn't terminate
```

### AdaptiveChannel Overflow Limits

```go
import "github.com/adalundhe/sylk/core/concurrency"

// Default configuration
config := concurrency.DefaultAdaptiveChannelConfig()
// MinSize: 16, MaxSize: 4096, InitialSize: 16
// MaxOverflowSize: 10000 (bounded)
// OverflowDropPolicy: DropNewest

// Custom configuration for high-throughput
highThroughput := concurrency.AdaptiveChannelConfig{
    MinSize:            64,
    MaxSize:            8192,
    InitialSize:        256,
    AllowOverflow:      true,
    MaxOverflowSize:    50000,  // Larger overflow buffer
    OverflowDropPolicy: concurrency.DropOldest,  // Prefer newest data
    SendTimeout:        100 * time.Millisecond,
}

ch := concurrency.NewAdaptiveChannelWithContext[Message](ctx, highThroughput)
```

**Drop Policies:**
- `DropNewest` - Reject new messages when full (backpressure)
- `DropOldest` - Evict oldest to make room (prefer fresh data)
- `Block` - Wait until space available (not recommended for high throughput)

### LLMGate Queue Configuration

```go
import "github.com/adalundhe/sylk/core/concurrency"

// Default configuration
config := concurrency.DefaultDualQueueGateConfig()
// MaxPipelineQueueSize: 1000
// MaxUserQueueSize: 1000
// MaxConcurrentRequests: 4
// RequestTimeout: 5 minutes

// High-throughput configuration
highCapacity := concurrency.DualQueueGateConfig{
    MaxPipelineQueueSize:  5000,
    MaxUserQueueSize:      2000,
    UserQueueRejectPolicy: concurrency.RejectPolicyBlock,
    UserQueueBlockTimeout: 10 * time.Second,
    MaxConcurrentRequests: 8,
    RequestTimeout:        3 * time.Minute,
    ShutdownTimeout:       10 * time.Second,
}

gate := concurrency.NewDualQueueGateWithContext(ctx, highCapacity, executor)
```

**Reject Policies:**
```go
const (
    RejectPolicyError      // Return error immediately when full
    RejectPolicyBlock      // Block until space available or timeout
    RejectPolicyDropOldest // Drop oldest to make room
)
```

### WAL Sync Modes

```go
import "github.com/adalundhe/sylk/core/concurrency"

// Sync modes
const (
    SyncEveryWrite // Safest, slowest - fsync after each write
    SyncBatched    // Balanced - fsync after interval elapses
    SyncPeriodic   // Fastest - background periodic fsync
)

// Configuration
config := concurrency.WALConfig{
    Dir:            ".sylk/wal",
    MaxSegmentSize: 64 * 1024 * 1024,  // 64MB segments
    SyncMode:       concurrency.SyncBatched,
    SyncInterval:   100 * time.Millisecond,
}

wal, err := concurrency.NewWriteAheadLogWithContext(ctx, config)
```

**Double-buffering for minimal lock time:**
- Writes continue to new buffer while old buffer syncs
- `syncMu` protects sync operation separate from write lock
- Periodic sync uses `TryLock` to avoid blocking writes

---

## 5. Memory Management

### LRU Cache Sizing

| Cache | Default Size | Location |
|-------|-------------|----------|
| Version Cache | 10,000 entries | `DefaultVersionCacheSize` |
| Event Cache | 10,000 entries | `DefaultEventCacheSize` |
| Hot Cache | 32MB | `DefaultHotCacheMaxSize` |

```go
// Version cache for VectorGraphDB
store := vectorgraphdb.NewVersionedNodeStoreWithSize(ns, 50000)

// Event cache for Archivalist
index := archivalist.NewBleveEventIndex(bleveIndex, 50000)

// Hot cache for adaptive retrieval
config := context.HotCacheConfig{
    MaxSize:           64 * 1024 * 1024,  // 64MB
    EvictionBatchSize: 100,
    AsyncEviction:     true,  // Background eviction
}
cache := context.NewHotCache(config)
```

### Bounded Overflow Configuration

```go
import "github.com/adalundhe/sylk/core/concurrency"

// Create bounded overflow buffer
overflow := concurrency.NewBoundedOverflow[Message](
    10000,                      // Max size
    concurrency.DropOldest,     // Drop policy
)

// Add with backpressure feedback
if !overflow.Add(msg) {
    // Message was dropped (DropNewest policy only)
}

// Monitor drops
droppedCount := overflow.DroppedCount()
```

### Hot Cache Eviction Parameters

```go
import "github.com/adalundhe/sylk/core/context"

const (
    DefaultHotCacheMaxSize    = 32 * 1024 * 1024  // 32MB
    DefaultEvictionBatchSize  = 100               // Entries per batch
    MaxEvictionsPerAdd        = 10                // Bounds latency spikes
)

config := context.HotCacheConfig{
    MaxSize:           64 * 1024 * 1024,
    EvictionBatchSize: 200,      // Larger batches for high churn
    OnEvict:           callback, // Called outside lock
    AsyncEviction:     true,     // Background goroutine
}

cache := context.NewHotCache(config)
cache.SetEvictionBatchSize(150)  // Runtime adjustment
```

### Observation Log Async Write Settings

```go
import "github.com/adalundhe/sylk/core/context"

const (
    DefaultObservationBufferSize = 100
    DefaultWriteQueueSize        = 1000
    DefaultSyncInterval          = 100 * time.Millisecond
)

config := context.ObservationLogConfig{
    Path:           "/path/to/log",
    BufferSize:     200,                    // Observation channel buffer
    WriteQueueSize: 2000,                   // Async write queue depth
    SyncInterval:   50 * time.Millisecond,  // Faster sync for durability
}

log, err := context.NewObservationLog(ctx, config)
```

---

## 6. Code Quality Patterns Applied

### sync.Once for Safe Channel Close

```go
// Pattern: Prevent double-close panics
type Worker struct {
    stopCh    chan struct{}
    closeOnce sync.Once
}

func (w *Worker) Close() {
    w.closeOnce.Do(func() {
        close(w.stopCh)
    })
}
```

### atomic.Bool for State Flags

```go
// Pattern: Lock-free state checks
type Component struct {
    closed atomic.Bool
}

func (c *Component) IsClosed() bool {
    return c.closed.Load()  // No lock needed
}

func (c *Component) Close() {
    if c.closed.Swap(true) {
        return  // Already closed
    }
    // Perform cleanup
}
```

### Internal *Locked Methods to Prevent Deadlock

```go
// Pattern: Separate public and internal methods
func (s *Store) Update(id string, data []byte) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    return s.updateLocked(id, data)
}

// Internal method assumes lock is held
func (s *Store) updateLocked(id string, data []byte) error {
    // Safe to call other *Locked methods
    return nil
}
```

### Reference-by-ID Pattern for Race Safety

```go
// Pattern: Pass IDs instead of pointers across goroutines
func (p *Processor) Process(itemID string) {
    go func(id string) {  // Capture ID, not pointer
        item := p.store.Get(id)  // Fresh lookup
        // Process item
    }(itemID)
}
```

### Collect-Keys-First for Map Iteration

```go
// Pattern: Avoid modifying map during iteration
func (c *Cache) EvictExpired() {
    c.mu.Lock()

    // Collect keys first
    var toDelete []string
    for id, entry := range c.entries {
        if entry.IsExpired() {
            toDelete = append(toDelete, id)
        }
    }

    // Then delete
    for _, id := range toDelete {
        delete(c.entries, id)
    }

    c.mu.Unlock()
}
```

---

## 7. Monitoring and Profiling

### Key Metrics to Monitor

| Subsystem | Metric | Warning Threshold |
|-----------|--------|-------------------|
| HNSW | Search latency p99 | > 50ms |
| HNSW | Index size | > 1M vectors |
| Bleve | Queue depth | > 80% of MaxQueueSize |
| Bleve | Batch processing time | > 1s |
| LLMGate | Queue depth | > 80% capacity |
| LLMGate | Rejection rate | > 1% |
| HotCache | Hit rate | < 80% |
| HotCache | Eviction rate | > 1000/s sustained |
| WAL | Sync latency | > 500ms |
| GoroutineScope | Leaked goroutines | > 0 |

### Profiling with pprof

```go
import (
    "net/http"
    _ "net/http/pprof"
)

// Enable pprof endpoints
go func() {
    http.ListenAndServe("localhost:6060", nil)
}()
```

**Profile collection:**
```bash
# CPU profile (30 seconds)
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30

# Heap profile
go tool pprof http://localhost:6060/debug/pprof/heap

# Goroutine profile
go tool pprof http://localhost:6060/debug/pprof/goroutine

# Block profile (contention)
go tool pprof http://localhost:6060/debug/pprof/block

# Mutex profile
go tool pprof http://localhost:6060/debug/pprof/mutex
```

### Common Bottleneck Patterns and Solutions

| Pattern | Symptom | Solution |
|---------|---------|----------|
| Lock contention | High block profile, goroutine pileup | Use *Locked methods, reduce critical section |
| GC pressure | High CPU in GC, saw-tooth memory | Increase cache sizes, reduce allocations |
| Unbounded growth | OOM, increasing memory | Add bounds (MaxOverflowSize, cache limits) |
| Slow sync | High WAL latency | Use SyncPeriodic, increase SyncInterval |
| Search latency | Slow HNSW queries | Increase EfSearch, check index health |
| Index build time | Slow insertions | Reduce EfConstruct, use batch operations |
| Queue overflow | Dropped messages | Increase queue size, add backpressure |

### Stats Collection Examples

```go
// HNSW stats
stats := index.Stats()
fmt.Printf("HNSW: nodes=%d, maxLevel=%d, M=%d\n",
    stats.TotalNodes, stats.MaxLevel, stats.M)

// Async queue stats
queueStats := indexManager.AsyncStats()
fmt.Printf("Queue: length=%d, processed=%d, dropped=%d\n",
    queueStats.QueueLength, queueStats.Processed, queueStats.Dropped)

// Hot cache stats
cacheStats := cache.Stats()
fmt.Printf("Cache: entries=%d, size=%dMB, hitRate=%.2f\n",
    cacheStats.EntryCount,
    cacheStats.CurrentSize/1024/1024,
    cacheStats.HitRate)

// LLM gate stats
gateStats := gate.Stats()
fmt.Printf("Gate: userQueue=%d, pipelineQueue=%d, active=%d\n",
    gateStats.UserQueueSize, gateStats.PipelineQueueSize, gateStats.ActiveRequests)

// Bounded overflow stats
overflowStats := overflow.Stats()
fmt.Printf("Overflow: len=%d, cap=%d, dropped=%d\n",
    overflowStats.Length, overflowStats.Capacity, overflowStats.DroppedCount)
```

---

## Quick Reference: Default Values

| Parameter | Default | File |
|-----------|---------|------|
| `DefaultM` | 16 | `core/vectorgraphdb/constants.go` |
| `DefaultEfConstruct` | 200 | `core/vectorgraphdb/constants.go` |
| `DefaultEfSearch` | 50 | `core/vectorgraphdb/constants.go` |
| `DefaultVersionCacheSize` | 10000 | `core/vectorgraphdb/versioned_nodes.go` |
| `DefaultEventCacheSize` | 10000 | `agents/archivalist/bleve_index.go` |
| `MinBatchSize` | 10 | `core/search/bleve/index_manager.go` |
| `MaxBatchSize` | 10000 | `core/search/bleve/index_manager.go` |
| `DefaultBatchSize` | 100 | `core/search/bleve/index_manager.go` |
| `DefaultHotCacheMaxSize` | 32MB | `core/context/hot_cache.go` |
| `MaxEvictionsPerAdd` | 10 | `core/context/hot_cache.go` |
| `DefaultAdaptiveMaxOverflow` | 10000 | `core/concurrency/adaptive_channel.go` |
| `DefaultWriteQueueSize` | 1000 | `core/context/observation_log.go` |
| `DefaultSyncInterval` | 100ms | `core/context/observation_log.go` |
