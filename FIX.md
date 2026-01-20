# Performance, Correctness, and Resource Optimization Fixes

**Generated:** 2026-01-19
**Updated:** 2026-01-19 (Wave 3 & Wave 4 Analysis Added)
**Status:** 222 concrete issues identified across 16 subsystems

---

## Wave 4 Implementation Analysis - Critical Fixes

This section documents 90 issues identified during comprehensive Wave 4 code review, including race conditions, correctness bugs, memory leaks, and performance problems.

---

## CRITICAL ISSUES (Immediate Fix Required)

### C.1 Unbounded Candidates Growth in HNSW Search

**Severity:** CRITICAL - Memory Exhaustion / Algorithm Violation
**File:** `core/vectorgraphdb/hnsw/hnsw.go`
**Lines:** 183-197

**Current Code:**
```go
for i := range candidates {           // Range captures len at START
    if len(candidates) >= ef*2 {      // Check AFTER append
        break
    }
    curr := candidates[i]
    neighbors := h.layers[level].getNeighbors(curr.ID)
    for _, neighbor := range neighbors {
        visited[neighbor] = true
        // ...
        candidates = append(candidates, SearchResult{...}) // LINE 196 - GROWS UNBOUNDED
    }
}
```

**Problem:**
- `range` captures slice length at loop start
- New candidates appended INSIDE the loop
- Check `len(candidates) >= ef*2` happens AFTER appends already occurred
- Loop continues beyond intended bounds, potentially exhausting memory
- Violates HNSW algorithm's complexity guarantees (should be O(log n), becomes O(n))

**Impact:**
- Memory exhaustion on large graphs
- Search complexity degrades from O(log n) to O(n)
- System crashes under load

**Fix:**
```go
func (h *Index) searchLayer(query []float32, queryMag float64, ep string, ef int, level int) []SearchResult {
    candidates := make([]SearchResult, 0, ef)
    candidates = append(candidates, SearchResult{ID: ep, Similarity: 1.0 - CosineSimilarity(query, h.vectors[ep], queryMag, h.magnitudes[ep])})

    visited := make(map[string]bool)
    visited[ep] = true

    maxCandidates := ef * 2  // Pre-compute limit

    // Use explicit index with proper bounds checking
    for i := 0; i < len(candidates) && len(candidates) < maxCandidates; i++ {
        curr := candidates[i]
        neighbors := h.layers[level].getNeighbors(curr.ID)

        for _, neighbor := range neighbors {
            if visited[neighbor] {
                continue
            }
            visited[neighbor] = true

            vec, exists := h.vectors[neighbor]
            if !exists {
                continue
            }

            mag, exists := h.magnitudes[neighbor]
            if !exists {
                continue  // Defensive: skip if magnitude missing
            }

            sim := CosineSimilarity(query, vec, queryMag, mag)

            // Only append if we haven't hit the limit
            if len(candidates) < maxCandidates {
                candidates = append(candidates, SearchResult{ID: neighbor, Similarity: sim})
            }
        }
    }

    // Sort and truncate to ef
    sort.Slice(candidates, func(i, j int) bool {
        return candidates[i].Similarity > candidates[j].Similarity
    })

    if len(candidates) > ef {
        candidates = candidates[:ef]
    }

    return candidates
}
```

**Acceptance Criteria:**
- [ ] Candidates never exceed ef*2
- [ ] Memory usage bounded regardless of graph size
- [ ] Search complexity remains O(log n) average case
- [ ] All existing tests pass

---

### C.2 Filter Logic Bug - MinSimilarity Never Applied

**Severity:** CRITICAL - Incorrect Results
**File:** `core/vectorgraphdb/hnsw/hnsw.go`
**Lines:** 272-290

**Current Code:**
```go
func (h *Index) matchesFilter(nodeID string, similarity float64, filter *SearchFilter) bool {
    if filter == nil {
        return true
    }

    if filter.MinSimilarity > 0 {
        return true  // BUG: Returns true WITHOUT checking similarity!
    }

    // ... domain and type checks follow
}
```

**Problem:**
- When `MinSimilarity > 0`, function returns `true` immediately
- The actual similarity comparison is NEVER performed
- ALL nodes pass the filter regardless of their similarity score
- Search results include completely irrelevant matches

**Impact:**
- Search quality severely degraded
- Users receive irrelevant results
- Filtering feature completely broken

**Fix:**
```go
func (h *Index) matchesFilter(nodeID string, similarity float64, filter *SearchFilter) bool {
    if filter == nil {
        return true
    }

    // Check minimum similarity threshold FIRST
    if filter.MinSimilarity > 0 && similarity < filter.MinSimilarity {
        return false  // Reject if below threshold
    }

    // Check domain filter
    if len(filter.Domains) > 0 {
        domain, exists := h.domains[nodeID]
        if !exists {
            return false
        }
        domainMatch := false
        for _, d := range filter.Domains {
            if d == domain {
                domainMatch = true
                break
            }
        }
        if !domainMatch {
            return false
        }
    }

    // Check node type filter
    if len(filter.NodeTypes) > 0 {
        nodeType, exists := h.nodeTypes[nodeID]
        if !exists {
            return false
        }
        typeMatch := false
        for _, t := range filter.NodeTypes {
            if t == nodeType {
                typeMatch = true
                break
            }
        }
        if !typeMatch {
            return false
        }
    }

    return true
}
```

**Acceptance Criteria:**
- [ ] Nodes below MinSimilarity threshold are rejected
- [ ] Filter returns false for similarity < MinSimilarity
- [ ] All filter conditions properly evaluated
- [ ] Unit tests verify similarity filtering

---

### C.3 Persistent Conditional State Bug in Call Graph Analysis

**Severity:** CRITICAL - Data Corruption
**File:** `core/knowledge/relations/call_graph.go`
**Lines:** 195-225 (Go extraction), 313-365 (Regex extraction)

**Current Code (Go extraction):**
```go
var inConditional bool

ast.Inspect(fn.Body, func(n ast.Node) bool {
    switch node := n.(type) {
    case *ast.IfStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
        inConditional = true  // Set to true but NEVER reset to false!
    case *ast.CallExpr:
        calleeName, callType := e.extractGoCallee(node, inConditional)
        // All subsequent calls marked as conditional!
    }
    return true
})
```

**Problem:**
- Once ANY conditional statement is encountered, `inConditional` is set to `true`
- The flag is NEVER reset when exiting the conditional scope
- ALL function calls after the first conditional are incorrectly marked as conditional
- Corrupts call graph confidence scores and analysis

**Example of Bug:**
```go
func process() {
    if flag {
        helperA()  // Correctly: CallTypeConditional, confidence 0.8
    }
    helperB()      // BUG: Marked as conditional (should be direct, confidence 1.0)
    helperC()      // BUG: Marked as conditional (should be direct, confidence 1.0)
}
```

**Impact:**
- Call graph analysis fundamentally incorrect
- Confidence scores unreliable
- Downstream consumers receive corrupted data

**Fix (using scope depth tracking):**
```go
type scopeTracker struct {
    conditionalDepth int
}

func (e *CallGraphExtractor) extractCallsFromGoFunc(
    fn *ast.FuncDecl,
    caller extractors.Entity,
    entityByName map[string][]extractors.Entity,
    fset *token.FileSet,
    lines []string,
) []knowledge.ExtractedRelation {
    var relations []knowledge.ExtractedRelation
    scope := &scopeTracker{conditionalDepth: 0}

    // Pre-order: increment depth when entering conditional
    // Post-order: decrement depth when exiting conditional
    ast.Inspect(fn.Body, func(n ast.Node) bool {
        if n == nil {
            return false
        }

        switch node := n.(type) {
        case *ast.IfStmt:
            scope.conditionalDepth++
            defer func() { scope.conditionalDepth-- }()
        case *ast.SwitchStmt:
            scope.conditionalDepth++
            defer func() { scope.conditionalDepth-- }()
        case *ast.TypeSwitchStmt:
            scope.conditionalDepth++
            defer func() { scope.conditionalDepth-- }()
        case *ast.SelectStmt:
            scope.conditionalDepth++
            defer func() { scope.conditionalDepth-- }()
        case *ast.ForStmt:
            scope.conditionalDepth++
            defer func() { scope.conditionalDepth-- }()
        case *ast.RangeStmt:
            scope.conditionalDepth++
            defer func() { scope.conditionalDepth-- }()
        case *ast.CallExpr:
            inConditional := scope.conditionalDepth > 0
            calleeName, callType := e.extractGoCallee(node, inConditional)
            if calleeName != "" {
                // ... create relation
            }
        }
        return true
    })

    return relations
}
```

**Alternative Fix (cleaner AST walking):**
```go
func (e *CallGraphExtractor) extractCallsFromGoFunc(...) []knowledge.ExtractedRelation {
    var relations []knowledge.ExtractedRelation

    // Use a custom walker that tracks scope entry/exit
    walker := &callExtractorWalker{
        extractor:    e,
        caller:       caller,
        entityByName: entityByName,
        fset:         fset,
        lines:        lines,
        relations:    &relations,
        scopeStack:   make([]scopeType, 0),
    }

    ast.Walk(walker, fn.Body)
    return relations
}

type scopeType int

const (
    scopeNormal scopeType = iota
    scopeConditional
    scopeLoop
)

type callExtractorWalker struct {
    extractor    *CallGraphExtractor
    caller       extractors.Entity
    entityByName map[string][]extractors.Entity
    fset         *token.FileSet
    lines        []string
    relations    *[]knowledge.ExtractedRelation
    scopeStack   []scopeType
}

func (w *callExtractorWalker) Visit(n ast.Node) ast.Visitor {
    if n == nil {
        // Exiting a node - pop scope if we pushed one
        if len(w.scopeStack) > 0 {
            w.scopeStack = w.scopeStack[:len(w.scopeStack)-1]
        }
        return nil
    }

    switch node := n.(type) {
    case *ast.IfStmt, *ast.SwitchStmt, *ast.TypeSwitchStmt, *ast.SelectStmt:
        w.scopeStack = append(w.scopeStack, scopeConditional)
    case *ast.ForStmt, *ast.RangeStmt:
        w.scopeStack = append(w.scopeStack, scopeLoop)
    case *ast.CallExpr:
        inConditional := w.isInConditionalScope()
        // ... extract call
    default:
        w.scopeStack = append(w.scopeStack, scopeNormal)
    }

    return w
}

func (w *callExtractorWalker) isInConditionalScope() bool {
    for _, scope := range w.scopeStack {
        if scope == scopeConditional {
            return true
        }
    }
    return false
}
```

**Acceptance Criteria:**
- [ ] Calls after conditional blocks correctly marked as direct
- [ ] Nested conditionals properly tracked
- [ ] Loop scopes also tracked correctly
- [ ] Confidence scores accurate (1.0 for direct, 0.8 for conditional)

---

### C.4 Race Condition in AsyncRetrievalFeedbackHook

**Severity:** CRITICAL - Data Race / Undefined Behavior
**File:** `core/chunking/retrieval_feedback.go`
**Lines:** 224-259

**Current Code:**
```go
func (h *AsyncRetrievalFeedbackHook) RecordRetrieval(chunkID string, wasUseful bool, ctx RetrievalContext) error {
    // ...
    h.mu.Lock()
    stats, exists := h.chunkStats[chunkID]
    if !exists {
        stats = &ChunkStats{
            ChunkID:     chunkID,
            FirstAccess: time.Now(),
        }
        h.chunkStats[chunkID] = stats
    }
    h.mu.Unlock()  // Lock released TOO EARLY!

    entry := feedbackEntry{
        chunkID:    chunkID,
        wasUseful:  wasUseful,
        context:    ctx,
        chunkStats: stats,  // Shared pointer used AFTER lock released!
    }

    select {
    case h.feedbackChan <- entry:  // Background goroutine modifies same ChunkStats!
        return nil
    // ...
    }
}
```

**Problem:**
- Lock is released at line 237
- `stats` pointer is then used at line 244 and sent to channel at line 247
- Background goroutine in `processEntry()` modifies the same `ChunkStats` object
- Two goroutines access the same object without synchronization = DATA RACE

**Impact:**
- Race detector will flag this
- Corrupted statistics (torn reads/writes)
- Undefined behavior, potential crashes

**Fix (extend lock scope):**
```go
func (h *AsyncRetrievalFeedbackHook) RecordRetrieval(chunkID string, wasUseful bool, ctx RetrievalContext) error {
    if h.closed.Load() {
        return ErrHookClosed
    }

    h.mu.Lock()
    stats, exists := h.chunkStats[chunkID]
    if !exists {
        stats = &ChunkStats{
            ChunkID:     chunkID,
            FirstAccess: time.Now(),
        }
        h.chunkStats[chunkID] = stats
    }

    // Create entry while still holding lock
    entry := feedbackEntry{
        chunkID:    chunkID,
        wasUseful:  wasUseful,
        context:    ctx,
        timestamp:  time.Now(),
        chunkStats: stats,
    }
    h.mu.Unlock()

    // Send to channel after releasing lock
    // But stats modifications in processEntry must be atomic!
    select {
    case h.feedbackChan <- entry:
        return nil
    case <-time.After(h.config.SendTimeout):
        return ErrFeedbackTimeout
    case <-h.ctx.Done():
        return h.ctx.Err()
    }
}

// In processEntry, use atomic operations for stats updates:
func (h *AsyncRetrievalFeedbackHook) processEntry(entry feedbackEntry) {
    stats := entry.chunkStats

    // Use atomic operations for thread-safe updates
    atomic.AddInt64(&stats.TotalRetrievals, 1)
    if entry.wasUseful {
        atomic.AddInt64(&stats.UsefulRetrievals, 1)
    }

    // For non-atomic fields, acquire lock
    h.mu.Lock()
    stats.LastAccess = entry.timestamp
    // ... other updates
    h.mu.Unlock()
}
```

**Alternative Fix (copy stats for channel):**
```go
func (h *AsyncRetrievalFeedbackHook) RecordRetrieval(chunkID string, wasUseful bool, ctx RetrievalContext) error {
    h.mu.Lock()
    stats, exists := h.chunkStats[chunkID]
    if !exists {
        stats = &ChunkStats{
            ChunkID:     chunkID,
            FirstAccess: time.Now(),
        }
        h.chunkStats[chunkID] = stats
    }

    // Copy the stats ID, don't share the pointer
    statsID := stats.ChunkID
    h.mu.Unlock()

    entry := feedbackEntry{
        chunkID:   chunkID,
        statsID:   statsID,  // Reference by ID, not pointer
        wasUseful: wasUseful,
        context:   ctx,
        timestamp: time.Now(),
    }

    // ... send to channel
}

// processEntry looks up stats by ID under lock
func (h *AsyncRetrievalFeedbackHook) processEntry(entry feedbackEntry) {
    h.mu.Lock()
    defer h.mu.Unlock()

    stats, exists := h.chunkStats[entry.statsID]
    if !exists {
        return
    }

    stats.TotalRetrievals++
    if entry.wasUseful {
        stats.UsefulRetrievals++
    }
    stats.LastAccess = entry.timestamp
}
```

**Acceptance Criteria:**
- [ ] `go test -race` passes
- [ ] No concurrent access to ChunkStats without synchronization
- [ ] Statistics remain accurate under concurrent load

---

### C.5 Race Condition in Bleve HybridSearch

**Severity:** CRITICAL - Use-After-Close
**File:** `core/vectorgraphdb/bleve_db.go`
**Lines:** 208-227

**Current Code:**
```go
func (db *BleveIntegratedDB) HybridSearch(ctx context.Context, query string, embedding []float32, opts HybridSearchOptions) ([]HybridResult, error) {
    db.mu.RLock()
    if db.closed {
        db.mu.RUnlock()
        return nil, ErrViewClosed
    }
    db.mu.RUnlock()  // Lock released HERE

    // RACE WINDOW: Another goroutine can call Close() here!

    // Perform vector search - uses db.vectorDB which may be nil/closed
    vectorResults, err := db.vectorSearch(embedding, opts)
    if err != nil {
        return nil, fmt.Errorf("vector search: %w", err)
    }

    // ... more operations using db internals
}
```

**Problem:**
- Lock released at line 213
- Search operations begin at line 227
- Between lines 213-227, another goroutine can call `Close()`
- `Close()` sets `db.closed = true` and may nil out internal components
- Search operations then operate on closed/nil resources

**Impact:**
- Panic on nil pointer dereference
- Use-after-close on database resources
- Undefined behavior

**Fix (hold lock during entire operation):**
```go
func (db *BleveIntegratedDB) HybridSearch(ctx context.Context, query string, embedding []float32, opts HybridSearchOptions) ([]HybridResult, error) {
    db.mu.RLock()
    defer db.mu.RUnlock()

    if db.closed {
        return nil, ErrViewClosed
    }

    // All operations now protected by read lock
    vectorResults, err := db.vectorSearchLocked(embedding, opts)
    if err != nil {
        return nil, fmt.Errorf("vector search: %w", err)
    }

    textResults, err := db.textSearchLocked(ctx, query, opts)
    if err != nil {
        return nil, fmt.Errorf("text search: %w", err)
    }

    return db.fuseResultsLocked(vectorResults, textResults, opts), nil
}

// Internal methods that assume lock is held
func (db *BleveIntegratedDB) vectorSearchLocked(embedding []float32, opts HybridSearchOptions) ([]VectorResult, error) {
    // No lock acquisition needed - caller holds it
    return db.vectorDB.Search(embedding, opts.VectorK)
}
```

**Alternative Fix (reference counting):**
```go
type BleveIntegratedDB struct {
    // ...
    refCount atomic.Int32
    closeCh  chan struct{}
}

func (db *BleveIntegratedDB) HybridSearch(ctx context.Context, ...) ([]HybridResult, error) {
    // Increment reference count
    if !db.acquireRef() {
        return nil, ErrViewClosed
    }
    defer db.releaseRef()

    // Safe to use - close will wait for refs to drain
    // ...
}

func (db *BleveIntegratedDB) acquireRef() bool {
    for {
        count := db.refCount.Load()
        if count < 0 {
            return false  // Closing
        }
        if db.refCount.CompareAndSwap(count, count+1) {
            return true
        }
    }
}

func (db *BleveIntegratedDB) releaseRef() {
    db.refCount.Add(-1)
}

func (db *BleveIntegratedDB) Close() error {
    // Signal closing
    db.refCount.Store(-1)

    // Wait for all operations to complete
    for db.refCount.Load() != -1 {
        runtime.Gosched()
    }

    // Safe to close resources
    // ...
}
```

**Acceptance Criteria:**
- [ ] No use-after-close possible
- [ ] Concurrent searches and close handled safely
- [ ] No panics under stress testing

---

### C.6 Map Modification During Iteration

**Severity:** CRITICAL - Runtime Panic
**File:** `core/handoff/prepared_context.go`
**Lines:** 395-403

**Current Code:**
```go
func (pc *PreparedContext) pruneInactiveToolStates() {
    cutoff := time.Now().Add(-pc.config.MaxAge * 2)
    for name, state := range pc.toolStates {  // Iterating map
        if !state.Active && state.LastUsed.Before(cutoff) {
            delete(pc.toolStates, name)  // Modifying map during iteration!
        }
    }
}
```

**Problem:**
- Go spec: "If a map entry is deleted during iteration, the corresponding iteration value may or may not be produced"
- In practice, this can cause:
  - Skipped entries
  - Duplicate iterations
  - Panic in some Go versions with concurrent access

**Impact:**
- Potential panic
- Incorrect pruning (some states may be missed)
- Undefined behavior

**Fix (collect keys first):**
```go
func (pc *PreparedContext) pruneInactiveToolStates() {
    cutoff := time.Now().Add(-pc.config.MaxAge * 2)

    // Collect keys to delete
    var toDelete []string
    for name, state := range pc.toolStates {
        if !state.Active && state.LastUsed.Before(cutoff) {
            toDelete = append(toDelete, name)
        }
    }

    // Delete after iteration
    for _, name := range toDelete {
        delete(pc.toolStates, name)
    }
}
```

**Acceptance Criteria:**
- [ ] No map modification during iteration
- [ ] All inactive states correctly pruned
- [ ] No panics or skipped entries

---

## HIGH SEVERITY ISSUES

### H.1 Version Cache Never Evicted - Memory Leak

**Severity:** HIGH - OOM Risk
**File:** `core/vectorgraphdb/versioned_nodes.go`
**Lines:** 41-75

**Current Code:**
```go
type VersionedNodeStore struct {
    db           *sql.DB
    versionCache sync.Map  // Grows unbounded
}

func (store *VersionedNodeStore) fetchAndCacheVersion(nodeID string) (uint64, error) {
    // ...
    store.versionCache.Store(nodeID, version)  // Never evicted
    return version, nil
}
```

**Problem:**
- Every node ID ever accessed is cached
- No TTL, no LRU eviction, no size limit
- Long-running processes accumulate unbounded memory

**Fix (add LRU eviction):**
```go
type VersionedNodeStore struct {
    db           *sql.DB
    cache        *lru.Cache[string, uint64]
    mu           sync.RWMutex
}

func NewVersionedNodeStore(db *sql.DB, maxCacheSize int) *VersionedNodeStore {
    if maxCacheSize <= 0 {
        maxCacheSize = 10000  // Default
    }
    cache, _ := lru.New[string, uint64](maxCacheSize)
    return &VersionedNodeStore{
        db:    db,
        cache: cache,
    }
}

func (store *VersionedNodeStore) fetchAndCacheVersion(nodeID string) (uint64, error) {
    store.mu.Lock()
    defer store.mu.Unlock()

    if version, ok := store.cache.Get(nodeID); ok {
        return version, nil
    }

    var version uint64
    err := store.db.QueryRow("SELECT version FROM nodes WHERE id = ?", nodeID).Scan(&version)
    if err != nil {
        return 0, err
    }

    store.cache.Add(nodeID, version)  // LRU eviction handled automatically
    return version, nil
}
```

**Acceptance Criteria:**
- [ ] Cache size bounded
- [ ] LRU eviction when limit reached
- [ ] Memory stable under sustained load

---

### H.2 Nested Lock Acquisition Causes Deadlock

**Severity:** HIGH - Deadlock
**File:** `core/handoff/prepared_context.go`
**Lines:** 320-337, 169-194

**Current Code:**
```go
func (pc *PreparedContext) AddMessage(msg Message) {
    pc.mu.Lock()  // Acquires PreparedContext lock
    defer pc.mu.Unlock()

    pc.summary.AddMessage(msg)      // AddMessage acquires RollingSummary lock
    pc.recentMessages.Push(msg)     // Push acquires CircularBuffer lock
    pc.recalculateTokenCount()      // Calls methods that try to acquire child locks!
    // ...
}

func (pc *PreparedContext) recalculateTokenCount() {
    count := 0
    count += pc.summary.TokenCount()       // Tries to acquire RollingSummary RLock
    for _, msg := range pc.recentMessages.Items() {  // Tries to acquire CircularBuffer RLock
        count += msg.TokenCount
    }
    // ...
}
```

**Problem:**
- `AddMessage` holds `pc.mu` (write lock)
- `recalculateTokenCount` calls `pc.summary.TokenCount()` which tries to acquire `summary.mu`
- If `RollingSummary.TokenCount()` is designed to be called independently, it acquires its own lock
- Nested lock acquisition with inconsistent ordering leads to potential deadlock

**Fix (internal methods without locking):**
```go
func (pc *PreparedContext) AddMessage(msg Message) {
    pc.mu.Lock()
    defer pc.mu.Unlock()

    if msg.Timestamp.IsZero() {
        msg.Timestamp = time.Now()
    }
    if msg.TokenCount == 0 {
        msg.TokenCount = estimateTokens(msg.Content)
    }

    // Call internal methods that don't acquire their own locks
    pc.summary.addMessageLocked(msg)
    pc.recentMessages.pushLocked(msg)
    pc.recalculateTokenCountLocked()
    pc.lastUpdated = time.Now()
    pc.version++
}

// Internal method - caller must hold pc.mu
func (pc *PreparedContext) recalculateTokenCountLocked() {
    count := 0
    count += pc.summary.tokenCountLocked()  // Internal, no lock
    for _, msg := range pc.recentMessages.itemsLocked() {  // Internal, no lock
        count += msg.TokenCount
    }
    for _, state := range pc.toolStates {
        count += estimateToolStateTokens(state)
    }
    pc.tokenCount = count
}
```

**Acceptance Criteria:**
- [ ] No nested lock acquisition
- [ ] No deadlocks under concurrent access
- [ ] Clear documentation of lock requirements

---

### H.3 Event Cache Unbounded Growth - OOM Risk

**Severity:** HIGH - Memory Exhaustion
**File:** `agents/archivalist/bleve_index.go`
**Lines:** 32-35, 90

**Current Code:**
```go
type BleveEventIndex struct {
    index      BleveIndex
    mu         sync.RWMutex
    eventCache map[string]*events.ActivityEvent  // No size limit!
}

func (b *BleveEventIndex) IndexEvent(event *events.ActivityEvent) error {
    // ...
    b.eventCache[event.ID] = event  // Just keeps growing
    // ...
}
```

**Fix:** Implement LRU cache with configurable max size (see H.1 for pattern).

---

### H.4 Double Close of Channel - Panic

**Severity:** HIGH - Runtime Panic
**File:** `core/handoff/manager.go`
**Lines:** 254-287

**Current Code:**
```go
func (m *HandoffManager) Stop() error {
    if m.closed.Swap(true) {
        return ErrManagerClosed
    }

    if !m.started.Load() {
        close(m.doneCh)  // Close #1
        return nil
    }

    close(m.stopCh)
    <-m.doneCh  // Wait for background loop
    // ...
}

func (m *HandoffManager) backgroundLoop() {
    defer close(m.doneCh)  // Close #2 - PANIC if already closed!
    // ...
}
```

**Fix (use sync.Once):**
```go
type HandoffManager struct {
    // ...
    doneOnce sync.Once
}

func (m *HandoffManager) closeDoneCh() {
    m.doneOnce.Do(func() {
        close(m.doneCh)
    })
}

func (m *HandoffManager) Stop() error {
    if m.closed.Swap(true) {
        return ErrManagerClosed
    }

    if !m.started.Load() {
        m.closeDoneCh()
        return nil
    }

    close(m.stopCh)
    <-m.doneCh
    // ...
}

func (m *HandoffManager) backgroundLoop() {
    defer m.closeDoneCh()
    // ...
}
```

---

### H.5 Missing Bounds Checks on Map Access

**Severity:** HIGH - Panic on Missing Data
**File:** `core/vectorgraphdb/hnsw/hnsw.go`
**Lines:** 134, 180, 195, 246, 262-263, 280, 285

**Current Code:**
```go
currDist := 1.0 - CosineSimilarity(vector, h.vectors[currObj], mag, h.magnitudes[currObj])
// No check if currObj exists in both maps!
```

**Fix:**
```go
func (h *Index) getVectorAndMagnitude(id string) ([]float32, float64, bool) {
    vec, vecExists := h.vectors[id]
    mag, magExists := h.magnitudes[id]
    if !vecExists || !magExists {
        return nil, 0, false
    }
    return vec, mag, true
}

// Usage:
vec, mag, ok := h.getVectorAndMagnitude(currObj)
if !ok {
    return nil, fmt.Errorf("node %s missing vector or magnitude", currObj)
}
currDist := 1.0 - CosineSimilarity(vector, vec, queryMag, mag)
```

---

### H.6 O(n³) Substring Search Algorithm

**Severity:** HIGH - Performance Degradation
**File:** `core/chunking/citation_detector.go`
**Lines:** 520-550

**Current Code:**
```go
func longestCommonSubstringLength(s1, s2 string) int {
    for length := minLen; length >= 1; length-- {          // O(n)
        for start := 0; start <= len(s1)-length; start++ { // O(n)
            substr := s1[start : start+length]
            if strings.Contains(s2, substr) {               // O(n)
                // Found!
            }
        }
    }
}
```

**Fix (dynamic programming):**
```go
func longestCommonSubstringLength(s1, s2 string) int {
    if len(s1) == 0 || len(s2) == 0 {
        return 0
    }

    // Use only 2 rows for space efficiency: O(min(n,m)) space
    prev := make([]int, len(s2)+1)
    curr := make([]int, len(s2)+1)
    maxLen := 0

    for i := 1; i <= len(s1); i++ {
        for j := 1; j <= len(s2); j++ {
            if s1[i-1] == s2[j-1] {
                curr[j] = prev[j-1] + 1
                if curr[j] > maxLen {
                    maxLen = curr[j]
                }
            } else {
                curr[j] = 0
            }
        }
        prev, curr = curr, prev
    }

    return maxLen
}
```

**Complexity:** O(n×m) time, O(min(n,m)) space

---

### H.7 WAL Manager TOCTOU Race

**Severity:** HIGH - Race Condition
**File:** `core/session/wal_manager.go`
**Lines:** 252-263

**Current Code:**
```go
func (m *MultiSessionWALManager) UpdateActivity(sessionID string) error {
    m.mu.RLock()
    if m.closed {
        m.mu.RUnlock()
        return ErrWALManagerClosed
    }
    m.mu.RUnlock()  // Released here

    // TOCTOU: m.closed could become true here!

    _, err := m.db.Exec(`UPDATE ...`)
    return err
}
```

**Fix:** Hold lock during database operation or use atomic closed flag with proper error handling in database operations.

---

## MEDIUM SEVERITY ISSUES

(See full analysis for 38 medium-severity issues including O(n²) algorithms, lock contention patterns, and missing validation)

---

## Summary by Subsystem

| Subsystem | Critical | High | Medium | Low | Total |
|-----------|----------|------|--------|-----|-------|
| vectorgraphdb/hnsw | 2 | 4 | 4 | 3 | 13 |
| knowledge/relations | 1 | 4 | 6 | 4 | 15 |
| handoff | 1 | 3 | 11 | 4 | 19 |
| chunking | 1 | 4 | 5 | 4 | 14 |
| Bleve integration | 1 | 3 | 5 | 3 | 12 |
| Persistence/WAL | 0 | 4 | 7 | 6 | 17 |
| **Wave 4 Total** | **6** | **22** | **38** | **24** | **90** |

---

## Original Performance Issues (38 issues)

---

## Executive Summary

While the codebase is spec-compliant with ARCHITECTURE.md, there are significant performance and resource consumption issues in:
- VectorGraphDB (ingest, query, deletion, concurrency)
- Bleve/Document Search (indexing, query, caching)
- Knowledge Graph (extraction, traversal, inference, memory)
- Concurrency/Resource Management (goroutines, memory, locks)

---

## 1. VectorGraphDB Performance Issues

### 1.1 N+1 Query Pattern in Hybrid Query Results Loading

**Severity:** CRITICAL
**Location:** `core/vectorgraphdb/query.go:157-167`

```go
func (qe *QueryEngine) scoreResults(allIDs map[string]bool, vectorScores map[string]float64, graphCounts map[string]int, maxConn int, opts *HybridQueryOptions) []HybridResult {
    results := make([]HybridResult, 0, len(allIDs))
    ns := NewNodeStore(qe.db, nil)  // New store created

    for id := range allIDs {
        result := qe.scoreNode(id, vectorScores, graphCounts, maxConn, opts, ns)  // Calls ns.GetNode(id) EVERY iteration
        if result != nil {
            results = append(results, *result)
        }
    }
    return results
}
```

**Problem:**
- Each iteration calls `qe.scoreNode()` → `ns.GetNode(id)` → `v.db.db.QueryRow()`
- For 100 results: **100 separate database queries**
- Pattern also appears in `search.go:76-91` and `query.go:238`

**Impact:** 100x extra queries per HybridQuery

**Fix:** Batch load nodes with single `WHERE id IN (...)` query

---

### 1.2 O(n) Linear Search in HNSW Layer Insertion

**Severity:** CRITICAL
**Location:** `core/vectorgraphdb/hnsw/layer.go:84-91`

```go
func (l *layer) containsNeighbor(node *layerNode, neighborID string) bool {
    for _, n := range node.neighbors {  // Linear scan through neighbors list
        if n == neighborID {
            return true
        }
    }
    return false
}
```

**Problem:**
- O(n) string comparison on every neighbor during insertion
- For M*2 = 48 neighbors, containsNeighbor scans entire list
- Called during every `addNeighbor()` in layer.go:67-82

**Impact:** O(n²) insertion complexity

**Fix:** Use `map[string]bool` for O(1) lookup

---

### 1.3 Layer-Level Mutex Contention in HNSW Search

**Severity:** HIGH
**Location:** `core/vectorgraphdb/hnsw/hnsw.go:175-209`

```go
func (h *Index) searchLayer(query []float32, queryMag float64, ep string, ef int, level int) []SearchResult {
    // ...
    for i := range candidates {
        neighbors := h.layers[level].getNeighbors(curr.ID)  // LAYER LOCK per iteration
        // ...
    }
}
```

**Problem:**
- Each `getNeighbors()` acquires layer RWMutex (layer.go:44-54)
- SearchLayer may call getNeighbors 100+ times
- No batch neighbor retrieval API

**Impact:** Serializes concurrent searches

**Fix:** Implement batch `getNeighborsMany()` or hold lock once per layer traversal

---

### 1.4 Per-Query NodeStore Instantiation

**Severity:** HIGH
**Location:** Multiple files

- `query.go:159` - `ns := NewNodeStore(qe.db, nil)`
- `query.go:238` - `ns := NewNodeStore(qe.db, nil)`
- `search.go:78` - `ns := NewNodeStore(vs.db, nil)`
- `traversal.go:44-50` - `NewGraphTraverser()` creates new stores

**Problem:**
- Each `NewNodeStore()` allocates new instance without reuse
- High allocation rate under concurrent load
- GC pressure proportional to query rate

**Fix:** Cache and reuse NodeStore instances per query context

---

### 1.5 Deletion Lock Contention

**Severity:** HIGH
**Location:** `core/vectorgraphdb/hnsw/hnsw.go:304-320`

```go
func (h *Index) deleteLocked(id string) {
    for _, l := range h.layers {  // Iterates ALL layers
        if l.hasNode(id) {
            h.removeNodeConnections(id, l)  // Layer-level RWMutex lock
            l.removeNode(id)
        }
    }
    // ...
    if h.entryPoint == id {
        h.selectNewEntryPoint()  // Linear scan all layers if entryPoint deleted
    }
}
```

**Problem:**
- Deletion holds global index lock through entire layer traversal
- Every Delete acquires locks on every layer (up to ~30 layers)
- Blocks concurrent reads/inserts during entire batch deletion

**Impact:** Deleting 1000 nodes = 1000 × 30 layer lock acquisitions

**Fix:** Implement read-write lock fairness, batch deletions with single lock acquisition

---

### 1.6 Missing Database Indexes

**Severity:** MEDIUM
**Location:** `core/vectorgraphdb/schema.sql:168-186`

**Missing Indexes:**
1. No index on `nodes.updated_at` - stale node collection does full table scan
2. No covering index for cross-domain traversal
3. No compound index for edge type + domain filtering

**Fix:** Add indexes:
```sql
CREATE INDEX idx_nodes_updated_at ON nodes(updated_at);
CREATE INDEX idx_edges_type_domain ON edges(edge_type, source_id);
```

---

## 2. Bleve/Document Search Performance Issues

### 2.1 Synchronous Blocking Index Operation

**Severity:** HIGH
**Location:** `core/search/bleve/index_manager.go:182-201`

```go
func (m *IndexManager) Index(ctx context.Context, doc *search.Document) error {
    m.mu.Lock()              // WRITE LOCK ACQUIRED
    defer m.mu.Unlock()      // Lock held for ENTIRE operation
    // ...
    return m.index.Index(doc.ID, doc)  // BLOCKING CALL
}
```

**Problem:**
- Write lock held for entire indexing duration
- All other search/index operations block
- Lock held during Bleve's internal disk writes

**Fix:** Decouple lock holding from indexing; use async indexing queue

---

### 2.2 DeleteByPath with Embedded Search

**Severity:** HIGH
**Location:** `core/search/bleve/index_manager.go:308-334`

```go
func (m *IndexManager) DeleteByPath(ctx context.Context, path string) error {
    m.mu.Lock()
    defer m.mu.Unlock()

    // Create search to find document by path
    result, err := m.index.SearchInContext(ctx, searchReq)  // SEARCH within lock
    // ...
    return m.index.Delete(result.Hits[0].ID)  // DELETE within lock
}
```

**Problem:**
- Synchronous search within write lock
- Two Bleve operations executed serially under lock
- Every delete requires full-text search then delete

**Fix:** Maintain reverse index (path → docID) or use document ID directly

---

### 2.3 No Batch Operation Timeout Protection

**Severity:** MEDIUM
**Location:** `core/search/bleve/index_manager.go:219-271`

```go
if err := m.index.Batch(batch); err != nil {  // NO TIMEOUT
    return fmt.Errorf("commit batch: %w", err)
}
```

**Problem:**
- Batch commits lack timeout protection
- If Bleve commit hangs, all documents lost
- `ctx.Done()` only checked during document additions, not commits

**Fix:** Add timeout wrapper around `Batch()` calls

---

### 2.4 All Fields Loaded on Every Search

**Severity:** MEDIUM
**Location:** `core/search/bleve/index_manager.go:422-426`

```go
func (m *IndexManager) buildBleveRequest(req *search.SearchRequest) *bleve.SearchRequest {
    bleveReq := bleve.NewSearchRequestOptions(query, req.Limit, req.Offset, false)
    bleveReq.Fields = []string{"*"}  // Load ALL fields including full content
    // ...
}
```

**Problem:**
- Retrieves all indexed fields including large `Content` field
- Every search hit deserializes entire document
- 100 results × multi-KB content = excessive memory

**Fix:** Implement selective field loading; lazy-load content on demand

---

### 2.5 Sequential IncrementalIndexer

**Severity:** HIGH
**Location:** `core/search/indexer/incremental.go:134-148`

```go
func (i *IncrementalIndexer) Index(ctx context.Context, changes []FileChange) (*search.IndexingResult, error) {
    for _, change := range changes {  // Sequential loop
        i.processChange(ctx, change, result)  // Single-threaded
    }
}
```

**Problem:**
- Changes processed one-by-one
- Each change acquires write lock
- 100 changes × 10ms = 1 second serialized

**Fix:** Accumulate changes into batches; use concurrent worker pool

---

### 2.6 Search Cache Double Lock Acquisition

**Severity:** MEDIUM
**Location:** `core/search/coordinator/cache.go:106-114`

```go
func (c *SearchCache) Get(key string) (*search.SearchResult, bool) {
    c.mu.RLock()
    // ... checks ...
    c.mu.RUnlock()

    c.promoteEntry(key)  // PROMOTE acquires WRITE lock again!
    return entry.result, true
}
```

**Problem:**
- Cache hits require TWO separate write lock acquisitions
- Defeats read-write lock benefits
- Popular queries suffer lock contention

**Fix:** Acquire write lock once for LRU promotion within `Get()`

---

### 2.7 Cache Key Missing Pagination Parameters

**Severity:** MEDIUM
**Location:** `core/search/coordinator/cache.go:252-263`

```go
func GenerateCacheKey(query string, filters map[string]string) string {
    h := sha256.New()
    h.Write([]byte(query))
    for k, v := range filters {
        h.Write([]byte(k))
        h.Write([]byte(v))
    }
    return hex.EncodeToString(h.Sum(nil))
}
```

**Problem:**
- Cache key includes query and filters but NOT `limit` or `offset`
- Different pagination requests return same cached result
- Wrong results for pagination queries

**Fix:** Include `limit` and `offset` in cache key generation

---

### 2.8 Unbounded Fusion Allocations

**Severity:** MEDIUM
**Location:** `core/search/coordinator/coordinator.go:173-177`

```go
func (c *SearchCoordinator) buildBleveRequest(req *HybridSearchRequest) *search.SearchRequest {
    bleveReq := &search.SearchRequest{
        Limit: req.Limit * 2,  // No validation
    }
}
```

**Problem:**
- `Limit * 2` passed without validation
- Fusion creates intermediate maps for all results
- Full sort even when only top N needed

**Fix:** Cap fusion results before sort; implement early termination

---

### 2.9 No Batch Size Validation

**Severity:** LOW-MEDIUM
**Location:** `core/search/bleve/index_manager.go:236-241`

**Problem:**
- No upper bound on batch size
- `BatchSize=10000` causes massive memory spike
- OOM risk for large codebases

**Fix:** Validate batch size within reasonable range (10-10000)

---

### 2.10 Semaphore Without Backpressure

**Severity:** LOW
**Location:** `core/search/coordinator/coordinator.go:51-52, 106-117`

**Problem:**
- Blocked searches occupy goroutine slots
- No queue management or FIFO fairness
- 1000s of blocked goroutines possible

**Fix:** Implement bounded request queue or fail-fast when full

---

## 3. Knowledge Graph Performance Issues

### 3.1 Regex Compiled Per Query

**Severity:** HIGH
**Location:** `core/knowledge/query/domain_filter.go:675`

```go
func tokenizeQuery(query string) []string {
    re := regexp.MustCompile(`[^a-zA-Z0-9_-]+`)  // COMPILED EVERY CALL
    parts := re.Split(strings.ToLower(query), -1)
}
```

**Problem:**
- Regex compiled on every tokenization call
- Called on every domain filtering operation
- O(n) overhead where n = number of queries

**Fix:** Move to module-level variable:
```go
var tokenizeRegex = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)
```

---

### 3.2 O(n²) Token Matching in Entity Linker

**Severity:** CRITICAL
**Location:** `core/knowledge/entity_linker.go:376-393`

```go
if len(refTokens) > 0 && len(targetTokens) > 0 {
    matchingTokens := 0
    for _, rt := range refTokens {           // OUTER LOOP
        for _, tt := range targetTokens {    // INNER LOOP
            if rt == tt {
                matchingTokens++
                break
            }
        }
    }
}
```

**Problem:**
- O(n²) token matching for every fuzzy match
- Called from `fuzzyResolve()` which iterates ALL indexed entities
- 1000 entities × 5 tokens = 2.5M comparisons per batch

**Fix:** Use set/map for token lookup; cache fuzzy match scores

---

### 3.3 Repeated String Operations Without Caching

**Severity:** MEDIUM
**Location:** `core/knowledge/entity_linker.go:322-370`

```go
func (el *EntityLinker) fuzzyMatchScore(ref, target string) float64 {
    if !el.config.CaseSensitive {
        refNorm = strings.ToLower(ref)      // Called per comparison
        targetNorm = strings.ToLower(target)
    }
    // ... multiple string operations follow
}
```

**Problem:**
- `strings.ToLower()` called per fuzzy match score
- Same strings normalized repeatedly
- Creates new strings on every comparison

**Fix:** Pre-normalize indexed entity names once

---

### 3.4 Levenshtein Distance Without Early Exit

**Severity:** MEDIUM
**Location:** `core/knowledge/entity_linker.go:431-470`

```go
func (el *EntityLinker) levenshteinDistance(a, b string) int {
    matrix := make([][]int, len(a)+1)        // O(n×m) allocation
    for i := range matrix {
        matrix[i] = make([]int, len(b)+1)
    }
    // ... full computation always runs
}
```

**Problem:**
- O(n×m) space and time for every call
- No early exit if distance exceeds threshold
- Matrix allocated on every call

**Fix:** Early return when distance > threshold; use space-efficient algorithm

---

### 3.5 visitKey Ignores Path (Incomplete Cycle Detection)

**Severity:** HIGH
**Location:** `core/knowledge/query/graph_traverser.go:366-371`

```go
func (gt *GraphTraverser) visitKey(path []string, nextID string) string {
    return nextID  // IGNORES THE PATH
}
```

**Problem:**
- visitKey completely ignores path parameter
- Same node visited multiple times via different paths
- No true cycle detection across path branches
- Exponential path explosion in highly connected graphs

**Fix:** Include path hash in visit key or implement proper cycle detection

---

### 3.6 No Activation Caching in Memory Scorer

**Severity:** HIGH
**Location:** `core/knowledge/memory/scorer.go:108`

```go
for i := range weighted {
    memoryScore, err := s.ComputeMemoryScore(ctx, weighted[i].ID, now, explore)
    // Calls memory.Activation(now) - expensive ACT-R calculation
}
```

**Problem:**
- Activation recalculated for every result
- O(n) where n = number of traces per node
- Same `now` parameter - could cache all calculations

**Fix:** Cache activation values by (nodeID, timestamp)

---

### 3.7 No Rule Binding Memoization in Inference Engine

**Severity:** CRITICAL
**Location:** `core/knowledge/inference/rule_evaluator.go:88-132`

```go
func (re *RuleEvaluator) findBindingsRecursive(...) []bindingResult {
    for _, binding := range currentBindings {
        matches := re.matchCondition(condition, edges, binding.variables)  // SCANS ALL EDGES
        for _, match := range matches {
            newBinding := bindingResult{
                matchedEdges: append(cloneEdges(binding.matchedEdges), match.edge),  // CLONES
            }
            newBindings = append(newBindings, newBinding)
        }
    }
    return re.findBindingsRecursive(...)  // RECURSIVE
}
```

**Problem:**
- No memoization - same matches computed repeatedly
- Full edge scan for every binding
- Array cloning for every new binding
- O(k^n × m) complexity where k = edges per condition, n = conditions

**Fix:** Add memoization cache; index edges by source/target

---

### 3.8 ValidAccessTypes Allocates on Every Call

**Severity:** LOW
**Location:** `core/knowledge/memory/actr_types.go:35-42`

```go
func ValidAccessTypes() []AccessType {
    return []AccessType{  // NEW ALLOCATION each call
        AccessRetrieval,
        AccessReinforcement,
        AccessCreation,
        AccessReference,
    }
}
```

**Fix:** Move to module-level constant

---

## 4. Concurrency and Resource Management Issues

### 4.1 Missing Context Propagation in WAL Periodic Sync

**Severity:** HIGH
**Location:** `core/concurrency/wal.go:93-96, 318-330`

```go
func (w *WriteAheadLog) startPeriodicSyncIfNeeded() {
    if w.config.SyncMode == SyncPeriodic {
        go w.periodicSync()  // No context propagation
    }
}
```

**Problem:**
- Goroutine doesn't receive context for cancellation
- Only checks channel, not context
- Won't exit on parent context cancellation

**Fix:** Pass context and select on `ctx.Done()`

---

### 4.2 AdaptiveChannel Goroutine Without Context

**Severity:** HIGH
**Location:** `core/concurrency/adaptive_channel.go:76-86`

```go
func NewAdaptiveChannel[T any](config AdaptiveChannelConfig) *AdaptiveChannel[T] {
    go ac.adaptLoop()  // Spawned without context awareness
    return ac
}
```

**Problem:**
- Long-lived background goroutine
- Doesn't respond to parent context cancellation

**Fix:** Accept context in constructor; select on `ctx.Done()` in adaptLoop

---

### 4.3 ObservationLog Untracked Processor Goroutine

**Severity:** MEDIUM
**Location:** `core/context/observation_log.go:159-170`

```go
func (l *ObservationLog) startProcessor(ctx context.Context) error {
    if l.scope == nil {
        go l.processLoop(ctx)  // Untracked goroutine
        return nil
    }
}
```

**Problem:**
- Untracked goroutine if no scope provided
- Potential leak if context cancellation doesn't happen

**Fix:** Always track goroutine through scope or wait group

---

### 4.4 GoroutineScope Spawns Untracked Goroutine During Shutdown

**Severity:** MEDIUM
**Location:** `core/concurrency/goroutine_scope.go:193-199`

```go
func (s *GoroutineScope) startWaitGroup() <-chan struct{} {
    done := make(chan struct{})
    go func() {  // Untracked goroutine during shutdown!
        s.wg.Wait()
        close(done)
    }()
    return done
}
```

**Fix:** Track shutdown goroutines or use inline wait with timeout

---

### 4.5 UnboundedChannel Overflow Without Limit

**Severity:** HIGH
**Location:** `core/concurrency/unbounded_channel.go:65-73`

```go
uc.overflow = append(uc.overflow, msg)  // Unbounded growth
```

**Problem:**
- Overflow slice grows without bounds
- No maximum size limit
- Memory exhaustion under high load

**Fix:** Add max overflow size; implement backpressure

---

### 4.6 AdaptiveChannel Overflow Without Limit

**Severity:** HIGH
**Location:** `core/concurrency/adaptive_channel.go:198-203`

```go
func (ac *AdaptiveChannel[T]) enqueueOverflow(msg T) {
    ac.overflow = append(ac.overflow, msg)  // No size limit
}
```

**Fix:** Add max overflow size with eviction or rejection policy

---

### 4.7 LLMGate UnboundedQueue Without Limit

**Severity:** HIGH
**Location:** `core/concurrency/llm_gate.go:116-147`

```go
func (q *UnboundedQueue) Push(req *LLMRequest) {
    q.items = append(q.items, req)  // Unbounded
}
```

**Fix:** Add queue size limit with backpressure

---

### 4.8 Mutex Held During File I/O

**Severity:** HIGH
**Location:** `core/context/observation_log.go:216-243`

```go
func (l *ObservationLog) writeToWAL(obs *EpisodeObservation) error {
    l.mu.Lock()
    defer l.mu.Unlock()

    if _, err := l.writer.Write(data); err != nil { /* I/O under lock */ }
    if err := l.writer.Flush(); err != nil { /* I/O under lock */ }
    return l.file.Sync()  // MOST EXPENSIVE - under lock
}
```

**Problem:**
- Every Record() blocks all other operations
- File.Sync() particularly expensive

**Fix:** Move I/O outside lock; use async write queue

---

### 4.9 WAL Sync Under Lock

**Severity:** MEDIUM
**Location:** `core/concurrency/wal.go:337-339`

```go
func (w *WriteAheadLog) trySyncPending() {
    w.mu.Lock()
    _ = w.syncLocked()
    w.mu.Unlock()
}
```

**Problem:**
- Periodic sync (every 100ms) locks entire WAL
- Blocks writes during sync

**Fix:** Use double-buffering or async sync

---

### 4.10 AdaptiveChannel Resize Under Lock

**Severity:** MEDIUM
**Location:** `core/concurrency/adaptive_channel.go:459-476`

```go
func (ac *AdaptiveChannel[T]) resizeLocked(newSize int) {
    newCh := make(chan T, newSize)  // Allocation under lock
    for {
        select {
        case msg := <-ac.ch:  // Channel operations under lock
            newCh <- msg
        // ...
        }
    }
}
```

**Problem:**
- All senders/receivers blocked during resize
- Slow if overflow buffer is large

**Fix:** Use lock-free channel swap or staged resize

---

### 4.11 context.Background() in Pipeline Scheduler

**Severity:** MEDIUM
**Location:** `core/concurrency/scheduler.go:155`

```go
go p.Runner.Start(context.Background())  // Detached from parent
```

**Problem:**
- Pipelines don't respect application-level cancellation

**Fix:** Pass parent context through scheduler

---

### 4.12 context.Background() in LLMGate

**Severity:** MEDIUM
**Location:** `core/concurrency/llm_gate.go:505-509`

```go
func (g *DualQueueGate) newRequestContext() (context.Context, context.CancelFunc) {
    return context.WithTimeout(context.Background(), g.config.RequestTimeout)
}
```

**Problem:**
- Requests don't inherit parent context

**Fix:** Use gate's lifecycle context as parent

---

### 4.13 Channel Replacement Race in AdaptiveChannel

**Severity:** HIGH
**Location:** `core/concurrency/adaptive_channel.go:152-168`

```go
func (ac *AdaptiveChannel[T]) blockSend(ctx context.Context, msg T) error {
    ac.mu.Lock()
    ch := ac.ch  // Save reference
    ac.mu.Unlock()

    select {
    case ch <- msg:  // Send on old reference while new one replaces it
        return nil
    }
}
```

**Problem:**
- Reference to `ac.ch` taken outside lock
- Channel replaced in `resizeLocked()` concurrently
- Messages sent to orphaned channel

**Fix:** Hold lock during send or use atomic channel swap

---

### 4.14 HotCache Eviction Under Lock

**Severity:** MEDIUM
**Location:** `core/context/hot_cache.go:211-219`

```go
func (c *HotCache) evictIfOverLimit() {
    // Called with mu already locked
    for c.currentSize > c.maxSize && c.lruList.Len() > 0 {
        c.removeEntry(entry)  // Multiple list operations under lock
    }
}
```

**Fix:** Batch eviction or use lock-free LRU

---

## Priority Matrix

### CRITICAL (Fix Immediately)
| Issue | Location | Impact |
|-------|----------|--------|
| N+1 query pattern | query.go:157 | 100x DB queries |
| O(n²) token matching | entity_linker.go:378 | 2.5M comparisons |
| No rule memoization | rule_evaluator.go:88 | O(k^n) exponential |
| O(n) HNSW neighbor lookup | layer.go:84 | O(n²) insertion |

### HIGH (Fix Soon)
| Issue | Location | Impact |
|-------|----------|--------|
| Write lock during Index() | index_manager.go:187 | Blocks all ops |
| Sequential IncrementalIndexer | incremental.go:138 | 1s for 100 changes |
| Unbounded overflow queues | unbounded_channel.go:71 | Memory exhaustion |
| Mutex across I/O | observation_log.go:216 | High contention |
| Missing activation cache | scorer.go:108 | O(traces) × O(results) |
| Channel replacement race | adaptive_channel.go:152 | Lost messages |

### MEDIUM (Plan Fix)
| Issue | Location | Impact |
|-------|----------|--------|
| Regex compiled per call | domain_filter.go:675 | O(n) overhead |
| Cache double-lock | cache.go:106 | Lock contention |
| All fields loaded | index_manager.go:426 | Memory overhead |
| Missing DB indexes | schema.sql | Full table scans |
| context.Background() usage | scheduler.go:155 | No cancellation |

### LOW (Track)
| Issue | Location | Impact |
|-------|----------|--------|
| ValidAccessTypes allocation | actr_types.go:35 | Minor GC pressure |
| Multiple time.Now() calls | extraction_pipeline.go | Syscall overhead |
| Semaphore backpressure | coordinator.go:51 | Blocked goroutines |

---

## Recommended Fix Order

1. **Week 1: Critical Query Patterns**
   - Batch node loading in VectorGraphDB
   - Add rule binding memoization
   - Fix O(n²) token matching

2. **Week 2: Lock Contention**
   - Async indexing in Bleve
   - Move I/O outside locks
   - Fix channel replacement race

3. **Week 3: Resource Bounds**
   - Add overflow limits to channels
   - Add activation caching
   - Fix context propagation

4. **Week 4: Optimizations**
   - Add missing indexes
   - Compile regex once
   - Selective field loading

---

## Wave 3 Implementation Analysis - Critical Fixes

This section documents 94 issues identified during comprehensive Wave 3 code review across 6 subsystems: hooks, events, session, context, extractors, and inference engine.

---

## CRITICAL ISSUES (Immediate Fix Required)

### W3C.1 Channel Double-Close Panic in SpeculativePrefetcher.CancelAll()

**Severity:** CRITICAL - Panic
**File:** `core/context/speculative_prefetcher.go`
**Lines:** 520-533

**Current Code:**
```go
func (sp *SpeculativePrefetcher) CancelAll() {
    sp.inflight.Range(func(key, value any) bool {
        future := value.(*TrackedPrefetchFuture)
        if !future.IsDone() {
            err := context.Canceled
            future.err.Store(&err)
            close(future.done)  // PANIC if complete() already closed it
        }
        sp.inflight.Delete(key)
        return true
    })
    sp.inflightCount.Store(0)
}
```

**Problem:**
- `IsDone()` check and `close(future.done)` are not atomic
- Between check and close, another goroutine can call `complete()` which also closes
- Double-close causes panic: "close of closed channel"

**Fix:**
```go
type TrackedPrefetchFuture struct {
    closeOnce sync.Once
    done      chan struct{}
    // ...
}

func (f *TrackedPrefetchFuture) complete(result *AugmentedQuery, err error) {
    if result != nil {
        f.result.Store(result)
    }
    if err != nil {
        f.err.Store(&err)
    }
    f.closeOnce.Do(func() { close(f.done) })
}

func (sp *SpeculativePrefetcher) CancelAll() {
    sp.inflight.Range(func(key, value any) bool {
        future := value.(*TrackedPrefetchFuture)
        err := context.Canceled
        future.err.Store(&err)
        future.closeOnce.Do(func() { close(future.done) })
        sp.inflight.Delete(key)
        return true
    })
}
```

---

### W3C.2 TOCTOU Race in ActivityEventBus.Publish()

**Severity:** CRITICAL - Panic
**File:** `core/events/activity_bus.go`
**Lines:** 148-167

**Current Code:**
```go
func (b *ActivityEventBus) Publish(event *ActivityEvent) {
    b.mu.RLock()
    if b.closed {
        b.mu.RUnlock()
        return
    }
    b.mu.RUnlock()  // Lock released here

    if b.debouncer.ShouldSkip(event) {
        return
    }

    // RACE: bus could be closed here, buffer channel closed
    select {
    case b.buffer <- event:  // PANIC if buffer is closed
    default:
    }
}
```

**Problem:**
- Lock released before channel send
- `Close()` can close `b.buffer` between check and send
- Causes panic: "send on closed channel"

**Fix:**
```go
func (b *ActivityEventBus) Publish(event *ActivityEvent) {
    b.mu.RLock()
    defer b.mu.RUnlock()

    if b.closed {
        return
    }

    if b.debouncer.ShouldSkip(event) {
        return
    }

    select {
    case b.buffer <- event:
    default:
    }
}
```

---

### W3C.3 Multiple Start() Causes WaitGroup Deadlock

**Severity:** CRITICAL - Deadlock
**File:** `core/events/activity_bus.go`
**Lines:** 217-227

**Current Code:**
```go
func (b *ActivityEventBus) Start() {
    b.dispatchMu.Lock()
    defer b.dispatchMu.Unlock()

    if b.closed {
        return
    }

    b.wg.Add(1)  // No check for already-started
    go b.dispatch()
}
```

**Problem:**
- If `Start()` called twice, `wg.Add(1)` called twice
- Two goroutines spawned, but `Close()` calls `wg.Wait()` expecting consistent count
- Can cause deadlock or panic

**Fix:**
```go
type ActivityEventBus struct {
    started atomic.Bool
    // ...
}

func (b *ActivityEventBus) Start() {
    if b.started.Swap(true) {
        return  // Already started
    }

    b.wg.Add(1)
    go b.dispatch()
}
```

---

### W3C.4 Pointer Aliasing Bug in RuleStore.updateCache()

**Severity:** CRITICAL - Data Corruption
**File:** `core/knowledge/inference/rule_store.go`
**Lines:** 72-78

**Current Code:**
```go
func (s *RuleStore) updateCache(rules []InferenceRule) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.cache = make(map[string]*InferenceRule, len(rules))
    for i := range rules {
        s.cache[rules[i].ID] = &rules[i]  // BUG: All pointers point to loop variable
    }
}
```

**Problem:**
- `&rules[i]` takes address of loop variable
- After loop, ALL cache entries point to same memory (last element)
- All lookups return the same rule

**Fix:**
```go
func (s *RuleStore) updateCache(rules []InferenceRule) {
    s.mu.Lock()
    defer s.mu.Unlock()
    s.cache = make(map[string]*InferenceRule, len(rules))
    for i := range rules {
        ruleCopy := rules[i]  // Copy to stable memory
        s.cache[ruleCopy.ID] = &ruleCopy
    }
}
```

---

### W3C.5 ObservationLog Channel Double-Close

**Severity:** CRITICAL - Panic
**File:** `core/context/observation_log.go`
**Lines:** 456-457, 547

**Current Code:**
```go
func (l *ObservationLog) Close() error {
    if l.closed.Swap(true) {
        return nil
    }
    // ...
    close(l.obsChannel)  // No mutex protection
}
```

**Problem:**
- No mutex protection on channel close
- If Close() called twice or concurrently, causes panic

**Fix:**
```go
type ObservationLog struct {
    chanClosed bool
    closeMu    sync.Mutex
    // ...
}

func (l *ObservationLog) Close() error {
    if l.closed.Swap(true) {
        return nil
    }
    l.closeMu.Lock()
    if !l.chanClosed {
        close(l.obsChannel)
        l.chanClosed = true
    }
    l.closeMu.Unlock()
    return nil
}
```

---

### W3C.6 Slice Data Race in Hook Injection

**Severity:** CRITICAL - Data Corruption
**File:** `core/context/hooks/failure_hook.go:259` (and similar hooks)

**Current Code:**
```go
existing.Excerpts = append([]ctxpkg.Excerpt{warningExcerpt}, existing.Excerpts...)
```

**Problem:**
- Multiple hooks execute concurrently on same `AugmentedQuery`
- All modify `Excerpts` slice without synchronization
- Causes data corruption or panics

**Fix:**
```go
newExcerpts := make([]ctxpkg.Excerpt, len(existing.Excerpts)+1)
newExcerpts[0] = warningExcerpt
copy(newExcerpts[1:], existing.Excerpts)
existing.Excerpts = newExcerpts
```

---

## HIGH SEVERITY ISSUES

### W3H.1 Manager.activeID Pointer Race - Use After Free

**Severity:** HIGH - Memory Safety
**File:** `core/session/manager.go`
**Lines:** 305-306

**Current Code:**
```go
func (m *Manager) setActiveSession(id string) {
    m.activeID.Store(&id)  // Storing pointer to stack-local variable
}
```

**Problem:** Stores pointer to stack variable that goes out of scope.

**Fix:**
```go
func (m *Manager) setActiveSession(id string) {
    idCopy := id
    m.activeID.Store(&idCopy)
}
```

---

### W3H.2 InvalidateDependents Infinite Recursion Risk

**Severity:** HIGH - Stack Overflow
**File:** `core/knowledge/inference/invalidation.go`
**Lines:** 194-219

**Problem:** No cycle detection in recursive invalidation.

**Fix:**
```go
func (im *InvalidationManager) InvalidateDependents(ctx context.Context, edgeKey string) error {
    visited := make(map[string]bool)
    return im.invalidateDependentsRecursive(ctx, edgeKey, visited)
}

func (im *InvalidationManager) invalidateDependentsRecursive(
    ctx context.Context,
    edgeKey string,
    visited map[string]bool,
) error {
    if visited[edgeKey] {
        return nil  // Cycle detected
    }
    visited[edgeKey] = true
    // ... rest of implementation
}
```

---

### W3H.3 EventDebouncer TOCTOU Bug

**Severity:** HIGH - Race Condition
**File:** `core/events/activity_bus.go`
**Lines:** 52-70

**Current Code:**
```go
func (d *EventDebouncer) ShouldSkip(event *ActivityEvent) bool {
    signature := d.signature(event)

    d.mu.RLock()
    lastSeen, exists := d.seen[signature]
    d.mu.RUnlock()  // Lock released

    if !exists {
        d.recordEvent(signature)  // Acquires write lock later
        return false
    }
    // ...
}
```

**Problem:** Check-then-act with lock release in between.

**Fix:**
```go
func (d *EventDebouncer) ShouldSkip(event *ActivityEvent) bool {
    signature := d.signature(event)

    d.mu.Lock()
    defer d.mu.Unlock()

    lastSeen, exists := d.seen[signature]

    if !exists {
        d.seen[signature] = time.Now()
        return false
    }

    if time.Since(lastSeen) > d.window {
        d.seen[signature] = time.Now()
        return false
    }

    return true
}
```

---

### W3H.4 Index Comparison Bug in access_hook.go

**Severity:** HIGH - Logic Error
**File:** `core/context/hooks/access_hook.go`
**Lines:** 269, 286

**Current Code:**
```go
if start := strings.Index(line, "]"); start > 0 {  // Bug: should be >= 0
```

**Problem:** Valid index 0 is rejected.

**Fix:**
```go
if start := strings.Index(line, "]"); start >= 0 {
```

---

### W3H.5 Python Block End Detection Fails with Docstrings

**Severity:** HIGH - Correctness
**File:** `core/knowledge/extractors/py_extractor.go`
**Lines:** 261-314

**Problem:** Triple-quoted docstrings incorrectly skipped as comments, causing wrong block boundaries.

---

### W3H.6 TypeScript Brace Counting Ignores Strings/Comments

**Severity:** HIGH - Correctness
**File:** `core/knowledge/extractors/ts_extractor.go`
**Lines:** 410-434

**Problem:** Braces in string literals and comments are counted, causing incorrect block detection.

---

### W3H.7 Double Close Risk in CrossSessionPool.Close()

**Severity:** HIGH - Panic
**File:** `core/session/cross_session_pool.go`
**Lines:** 393-395

**Problem:** Channel double-close if `Close()` called twice.

**Fix:** Add sync.Once guard.

---

## MEDIUM SEVERITY ISSUES

### W3M.1 - EventDebouncer.seen Map Unbounded Growth
**File:** `core/events/activity_bus.go:32-96`
**Issue:** No automatic cleanup of debouncer map entries

### W3M.2 - Unbounded inflight Prefetches Map
**File:** `core/context/speculative_prefetcher.go:161`
**Issue:** Completed futures accumulate indefinitely

### W3M.3 - O(n) Subscriber Scan in Unsubscribe
**File:** `core/events/activity_bus.go:193-214`
**Issue:** Linear scan for removal in each list

### W3M.4 - O(n×m) Parent Class Detection
**File:** `core/knowledge/extractors/py_extractor.go:88-131`
**Issue:** For each function, iterates all classes

### W3M.5 - O(n²) Block End Detection
**File:** `core/knowledge/extractors/py_extractor.go:261-314`
**Issue:** Called for every function, scans to EOF

### W3M.6 - Session.Stats TOCTOU Race
**File:** `core/session/session.go:373-394`
**Issue:** Atomic values read outside lock

### W3M.7 - Context Cancel Leak in SubmitAsync
**File:** `core/session/dual_queue_gate.go:103-122`
**Issue:** Cancel function may never be called

### W3M.8 - Goroutine Leak in Signal Dispatcher
**File:** `core/session/signal_dispatcher.go:106-127`
**Issue:** mergeStopChannels goroutine may block forever

### W3M.9 - Silent nil Bus Errors
**File:** `core/events/publishers.go:29-30`
**Issue:** Returns nil instead of error when bus is nil

### W3M.10 - HotCache O(n) Eviction
**File:** `core/context/hot_cache.go:211-220`
**Issue:** May evict many entries in single Add() call

### W3M.11 - TaskCompletionEviction O(n²) Matching
**File:** `core/context/eviction_task.go:255-262`
**Issue:** Nested loops for completion marker detection

### W3M.12 - ObservationLog Full File Rewrite
**File:** `core/context/observation_log.go:379-402`
**Issue:** Truncate() reads and rewrites entire file

### W3M.13 - Sequence Counter Never Reset
**File:** `core/context/observation_log.go:152, 404`
**Issue:** Sequence grows indefinitely after truncate

### W3M.14 - Materialization Lock During DB I/O
**File:** `core/knowledge/inference/materialization.go:47-68`
**Issue:** Mutex held during slow database operations

### W3M.15 - LIKE Query False Positives
**File:** `core/knowledge/inference/materialization.go:235-265`
**Issue:** `LIKE %key%` matches unintended substrings

---

## Wave 3 Summary Table

| Subsystem | Critical | High | Medium | Low | Total |
|-----------|----------|------|--------|-----|-------|
| core/context/hooks | 2 | 3 | 4 | 3 | 12 |
| core/events | 2 | 3 | 4 | 2 | 11 |
| core/session | 1 | 4 | 9 | 7 | 21 |
| core/context | 2 | 3 | 10 | 4 | 19 |
| core/knowledge/extractors | 0 | 6 | 8 | 0 | 14 |
| core/knowledge/inference | 1 | 3 | 4 | 1 | 9 |
| **TOTAL** | **8** | **22** | **39** | **17** | **86** |

---

## Wave 3 Priority Fixes

### Week 1: Critical Fixes
- W3C.1: SpeculativePrefetcher double-close
- W3C.2: ActivityEventBus TOCTOU
- W3C.3: Start() WaitGroup imbalance
- W3C.4: RuleStore pointer aliasing
- W3C.5: ObservationLog double-close
- W3C.6: Hook slice data race

### Week 2: High Severity
- W3H.1: Manager.activeID pointer race
- W3H.2: InvalidateDependents cycle detection
- W3H.3: EventDebouncer TOCTOU
- W3H.4: Index comparison bugs
- W3H.5-6: Extractor parsing issues

### Week 3: Memory Leaks
- W3M.1-2: Unbounded map/cache growth
- W3M.7-8: Goroutine/context leaks

### Week 4: Performance
- W3M.3-5: Algorithm complexity improvements
- W3M.10-12: I/O and eviction optimizations
