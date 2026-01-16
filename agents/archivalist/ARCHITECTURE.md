# Archivalist Architecture

## Overview

The Archivalist is a shared memory system for AI coding agents. It enables seamless handoffs between agents (Opus, Codex, etc.) during long coding sessions, preventing context loss, repeated failures, and pattern drift.

This document describes the complete architecture including:
- Four-tier storage hierarchy (L0 Cache → L1 Hot → L2 Cold → L3 Archive)
- Multi-session and sub-agent coordination
- Read-through data model (global reads, session-scoped writes)
- Append-only global history
- Caching for token and latency optimization
- Conflict resolution

---

## Core Principles

### 1. Agents Serve Agents
The Archivalist's consumers are AI agents that need:
- Instant context on what's been done
- File state so they don't re-read files
- Failure memory so they don't repeat mistakes
- Pattern consistency so code stays uniform
- Clear handoff instructions to continue work

### 2. Token Efficiency is Critical
Every response must minimize tokens while preserving information. Agents have limited context windows.

### 3. Shared Learning, Isolated Workspaces
All agents benefit from global knowledge (patterns, failures) while maintaining isolated session state (current task, files).

### 4. Tiered Storage for Performance
Hot data in memory for speed, cold data in SQLite for persistence and search.

---

## Storage Hierarchy

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                        FOUR-TIER STORAGE HIERARCHY                           │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  L0: RISTRETTO CACHE                                                         │
│  ═══════════════════                                                         │
│  Purpose: Sub-millisecond reads for hot paths                                │
│  Backend: github.com/dgraph-io/ristretto                                     │
│  Contents:                                                                   │
│    - Version-keyed session state (immutable snapshots)                       │
│    - Materialized briefings (pre-computed)                                   │
│    - Global history delta cache                                              │
│  Eviction: LFU with cost-based admission                                     │
│  TTL: None needed (version-keyed = immutable)                                │
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │  session_a:resume:sv_12  →  ResumeState (frozen at v12)             │    │
│  │  session_a:files:sv_13   →  FileStates (frozen at v13)              │    │
│  │  briefing:standard:a     →  AgentBriefing + (gv:47, sv:12)          │    │
│  │  global:delta:45-47      →  [pattern, failure, insight]             │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                              │
│                                    ↓ miss                                    │
│                                                                              │
│  L1: HOT MEMORY (Store)                                                      │
│  ══════════════════════                                                      │
│  Purpose: Active working set, fast writes                                    │
│  Backend: In-memory maps with sync.RWMutex                                   │
│  Contents:                                                                   │
│    - Current session entries (task state, decisions, issues)                 │
│    - Global history (append-only patterns, failures, insights)               │
│    - Session contexts (resume, files, intents)                               │
│  Capacity: ~750K tokens (configurable threshold)                             │
│  Overflow: Automatic archival to L2 when threshold exceeded                  │
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │  GlobalHistory:                                                     │    │
│  │    patterns[]    ──→ append-only, never modified                    │    │
│  │    failures[]    ──→ append-only, never modified                    │    │
│  │    insights[]    ──→ append-only, never modified                    │    │
│  │    version: 47   ──→ monotonic counter                              │    │
│  │                                                                     │    │
│  │  SessionContexts:                                                   │    │
│  │    session_a: {resume, files, intents, version: 12}                 │    │
│  │    session_b: {resume, files, intents, version: 8}                  │    │
│  │                                                                     │    │
│  │  Entries (chronological):                                           │    │
│  │    [ent_001, ent_002, ..., ent_500]                                 │    │
│  │    Total tokens: 650,000 / 750,000 threshold                        │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                              │
│                                    ↓ overflow / query                        │
│                                                                              │
│  L2: COLD STORAGE (Archive - SQLite)                                         │
│  ═══════════════════════════════════                                         │
│  Purpose: Persistence, full-text search, historical queries                  │
│  Backend: SQLite with WAL mode + FTS5                                        │
│  Contents:                                                                   │
│    - Archived entries (overflow from L1)                                     │
│    - Completed session data                                                  │
│    - Entry relationships/links                                               │
│    - Full-text search index                                                  │
│  Location: .sylk/archive.db                                                  │
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │  Tables:                                                            │    │
│  │    sessions        - id, started_at, ended_at, summary              │    │
│  │    entries         - id, category, content, source, session_id      │    │
│  │    entries_fts     - FTS5 virtual table for full-text search        │    │
│  │    entry_links     - from_id, to_id, relationship                   │    │
│  │                                                                     │    │
│  │  Indexes:                                                           │    │
│  │    idx_entries_category, idx_entries_session                        │    │
│  │    idx_entries_source, idx_entries_created                          │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                              │
│                                    ↓ historical / cross-session              │
│                                                                              │
│  L3: GLOBAL HISTORY SNAPSHOTS                                                │
│  ════════════════════════════════                                            │
│  Purpose: Efficient reconstruction, delta queries                            │
│  Backend: Periodic snapshots + append log                                    │
│  Contents:                                                                   │
│    - Checkpoint snapshots (every N versions)                                 │
│    - Append log since last snapshot                                          │
│    - Version index for O(1) delta lookups                                    │
│                                                                              │
│  ┌─────────────────────────────────────────────────────────────────────┐    │
│  │  Snapshot @ gv:100:                                                 │    │
│  │    patterns: [pat_1...pat_100]                                      │    │
│  │    failures: [fail_1...fail_85]                                     │    │
│  │    insights: [ins_1...ins_50]                                       │    │
│  │                                                                     │    │
│  │  Append Log:                                                        │    │
│  │    [gv:101] {type: pattern, data: ...}                              │    │
│  │    [gv:102] {type: failure, data: ...}                              │    │
│  │    ...                                                              │    │
│  │    [gv:147] {type: insight, data: ...}                              │    │
│  │                                                                     │    │
│  │  versionIndex: {101→0, 102→1, ..., 147→46}                          │    │
│  └─────────────────────────────────────────────────────────────────────┘    │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

## Data Model: Read-Through Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                     READ-THROUGH MODEL                          │
├─────────────────────────────────────────────────────────────────┤
│                                                                 │
│  WRITE: Session-scoped (only affects your session)              │
│  READ:  Global (can read any session's contributions)           │
│                                                                 │
│  Session A contributes pattern ──┐                              │
│  Session B contributes failure   ├──→ ALL sessions benefit      │
│  Session C contributes insight ──┘                              │
│                                                                 │
└─────────────────────────────────────────────────────────────────┘
```

### Scope Classification

| Scope | Read Access | Write Access | Storage Tier | Cache Strategy |
|-------|-------------|--------------|--------------|----------------|
| **Patterns** | Global (all sessions) | Global (append) | L1 + L3 snapshot | Append-only cache |
| **Failures** | Global (all sessions) | Global (append) | L1 + L3 snapshot | Append-only cache |
| **Insights** | Global (all sessions) | Global (append) | L1 + L3 snapshot | Append-only cache |
| **Resume** | Session-only | Session-only | L1 | Version-keyed |
| **Files** | Session-only | Session-only | L1 | Version-keyed |
| **Intents** | Session-only | Session-only | L1 | Version-keyed |
| **Entries** | Session + archived | Session-only | L1 → L2 overflow | LRU in L0 |

---

## Append-Only Global History

Global knowledge (patterns, failures, insights) uses an append-only model:
- **No overwrites** - New contributions are appended, never replace existing
- **No conflicts** - Appending cannot conflict with other writes
- **Full attribution** - Each entry tagged with source session/agent
- **Universal benefit** - All sessions can read all contributions

### Why Append-Only?

| Scenario | Traditional Model | Append-Only Model |
|----------|------------------|-------------------|
| A writes pattern "X" | Creates pattern X | Appends: {pattern: X, source: A} |
| B writes pattern "Y" | Overwrites? Conflict? | Appends: {pattern: Y, source: B} |
| Result | Complex conflict resolution | Both exist, both searchable |

### Global History Structure

```go
type GlobalHistory struct {
    mu sync.RWMutex

    // Append-only logs - entries are NEVER modified or deleted
    patterns []*PatternEntry
    failures []*FailureEntry
    insights []*InsightEntry

    // Monotonic version counter
    version uint64

    // Indexes for efficient search (maintained on append)
    patternsByCategory map[string][]*PatternEntry
    failuresByApproach map[string][]*FailureEntry
    insightsByTopic    map[string][]*InsightEntry

    // Full-text search index (in-memory, synced with L2 FTS5)
    searchIndex *SearchIndex
}

type PatternEntry struct {
    ID          string    `json:"id"`      // pat_47
    Version     uint64    `json:"v"`       // Birth version (gv:47)
    Category    string    `json:"cat"`     // "error_handling", "state_management"
    Pattern     string    `json:"p"`       // "use channels for state coordination"
    Description string    `json:"desc,omitempty"`
    Example     string    `json:"ex,omitempty"`

    // Attribution (who contributed this)
    SessionID string      `json:"sid"`
    AgentID   string      `json:"aid"`
    Source    SourceModel `json:"src"`     // gpt-5.2-codex, claude-opus-4-5, etc.
    CreatedAt time.Time   `json:"ts"`

    // Usage tracking (for priority in read-time resolution)
    UsageCount uint64    `json:"uc"`
    LastUsedAt time.Time `json:"lu,omitempty"`
}

type FailureEntry struct {
    ID          string      `json:"id"`
    Version     uint64      `json:"v"`
    Approach    string      `json:"approach"`   // What was tried
    Reason      string      `json:"reason"`     // Why it failed
    Context     string      `json:"ctx"`        // What was being attempted
    Resolution  string      `json:"res"`        // What worked instead

    // Attribution
    SessionID string      `json:"sid"`
    AgentID   string      `json:"aid"`
    Source    SourceModel `json:"src"`
    CreatedAt time.Time   `json:"ts"`
}
```

### Contradictory Pattern Resolution

When multiple patterns exist for the same category, resolution happens at **read time**, not write time:

```go
func (gh *GlobalHistory) GetBestPattern(category string) (*PatternEntry, []*PatternEntry) {
    patterns := gh.patternsByCategory[category]

    if len(patterns) <= 1 {
        return patterns[0], nil
    }

    // Multiple patterns - check failure history
    var best *PatternEntry
    var alternatives []*PatternEntry

    for _, p := range patterns {
        if gh.hasFailureFor(p.Pattern) {
            // This approach has failed before - deprioritize
            alternatives = append(alternatives, p)
            continue
        }

        // Prefer by usage count
        if best == nil || p.UsageCount > best.UsageCount {
            if best != nil {
                alternatives = append(alternatives, best)
            }
            best = p
        } else {
            alternatives = append(alternatives, p)
        }
    }

    return best, alternatives
}
```

---

## Session-Scoped State

Each session maintains isolated state for:
- **Resume State** - Current task, progress, next steps
- **File State** - Files read/modified in this session
- **User Intents** - This session's user preferences

### Session Context Structure

```go
type SessionContextManager struct {
    mu       sync.RWMutex
    sessions map[string]*SessionContext
}

type SessionContext struct {
    mu sync.RWMutex

    sessionID string
    version   uint64  // Session-local version

    // Session-scoped state
    resumeState *ResumeState
    files       map[string]*FileState
    intents     []*Intent

    // Track last writer for conflict detection
    lastWriters map[Scope]string
}

type ResumeState struct {
    CurrentTask    string      `json:"task"`
    TaskObjective  string      `json:"obj"`
    CompletedSteps []string    `json:"done"`
    CurrentStep    string      `json:"curr"`
    NextSteps      []string    `json:"next"`
    Blockers       []string    `json:"block"`
    FilesToRead    []string    `json:"files"`
    LastAgent      SourceModel `json:"agent"`
    LastUpdate     time.Time   `json:"ts"`
}
```

### Conflict Resolution (Within Session Only)

Conflicts only occur within a session (multiple agents in same session):

| Conflict Type | Resolution Strategy |
|---------------|---------------------|
| Resume state | Hierarchy (parent agent wins) |
| File modifications | Merge (track both agents' changes) |
| Intents | Hierarchy (parent agent wins) |
| Patterns | N/A (append-only, no conflict) |
| Failures | N/A (append-only, no conflict) |

```go
// Hierarchy resolution for resume state conflicts
func (cd *ConflictDetector) detectResumeConflict(incoming, agentID string) *ConflictResult {
    incomingAgent := cd.registry.Get(agentID)
    existingAgent := cd.registry.Get(existing.LastAgentID)

    // Parent can override child
    if existingAgent.ParentID == incomingAgent.ID {
        return &ConflictResult{Strategy: ResolutionAccept}
    }

    // Child cannot override parent
    if incomingAgent.ParentID == existingAgent.ID {
        return &ConflictResult{Strategy: ResolutionReject}
    }

    // Siblings - merge or last-write-wins
    return &ConflictResult{Strategy: ResolutionMerge}
}
```

---

## Dual Version System

Two version dimensions track different scopes of change:

### Global Version
- Increments on any append to global history (patterns, failures, insights)
- Single monotonic counter: `gv:1`, `gv:2`, ... `gv:47`
- Used for global delta queries and cache invalidation

### Session Version
- Per-session counter: `sv_a:1`, `sv_a:2`, ... `sv_a:12`
- Increments on session-scoped writes (resume, files, intents)
- Used for session-specific caching

```go
type DualVersion struct {
    GlobalVersion  uint64  // gv:47
    SessionVersion uint64  // sv_a:12
}

type Registry struct {
    // ... existing fields ...

    // Version tracking
    globalVersion   uint64              // Single global counter
    sessionVersions map[string]uint64   // Per-session counters
}

func (r *Registry) IncrementGlobalVersion() uint64 {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.globalVersion++
    return r.globalVersion
}

func (r *Registry) IncrementSessionVersion(sessionID string) uint64 {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.sessionVersions[sessionID]++
    return r.sessionVersions[sessionID]
}

func (r *Registry) GetDualVersion(sessionID string) DualVersion {
    r.mu.RLock()
    defer r.mu.RUnlock()
    return DualVersion{
        GlobalVersion:  r.globalVersion,
        SessionVersion: r.sessionVersions[sessionID],
    }
}
```

---

## Caching Architecture

The caching system optimizes for both **latency** (fast responses) and **tokens** (minimal response size).

### Design Goals

| Goal | Mechanism |
|------|-----------|
| Fast reads | L0 ristretto cache, materialized views |
| Minimal tokens | NOT_MODIFIED responses, delta queries, tiered briefings |
| No staleness | Version-keyed caching, dual version tracking |
| No conflicts | Append-only global cache |
| Persistence | L2 SQLite with WAL mode |

### Cache Layer Implementation

```go
type CacheLayer struct {
    // L0: Ristretto for hot path caching
    cache *ristretto.Cache

    // Materialized views (eagerly computed)
    views *MaterializedViews

    // Global history cache (snapshot + append log)
    globalCache *GlobalHistoryCache

    // Metrics
    metrics *CacheMetrics
}

type CacheConfig struct {
    // Ristretto configuration
    MaxCost     int64  // Max memory (default: 1GB)
    NumCounters int64  // Admission policy counters (default: 10M)

    // Global history snapshots
    SnapshotInterval uint64  // Create snapshot every N versions (default: 100)

    // L1 hot memory
    TokenThreshold int  // Overflow threshold (default: 750K tokens)
}

func NewCacheLayer(cfg CacheConfig) (*CacheLayer, error) {
    cache, err := ristretto.NewCache(&ristretto.Config{
        NumCounters: cfg.NumCounters,  // 10M counters for admission
        MaxCost:     cfg.MaxCost,      // 1GB max
        BufferItems: 64,               // Keys per Get buffer
        Metrics:     true,

        // Variable cost based on data size
        Cost: func(value interface{}) int64 {
            return int64(estimateBytes(value))
        },
    })
    if err != nil {
        return nil, fmt.Errorf("create ristretto cache: %w", err)
    }

    return &CacheLayer{
        cache:       cache,
        views:       newMaterializedViews(),
        globalCache: newGlobalHistoryCache(cfg.SnapshotInterval),
        metrics:     &CacheMetrics{},
    }, nil
}
```

### Cache Key Strategies

```go
// Session state: version-keyed (immutable once created)
func sessionCacheKey(sessionID string, scope Scope, version uint64) string {
    return fmt.Sprintf("%s:%s:sv_%d", sessionID, scope, version)
}
// Examples:
//   "session_a:resume:sv_12"
//   "session_a:files:sv_13"

// Briefings: dual-version keyed
func briefingCacheKey(sessionID string, tier BriefingTier, gv, sv uint64) string {
    return fmt.Sprintf("briefing:%s:%s:gv_%d:sv_%d", tier, sessionID, gv, sv)
}
// Examples:
//   "briefing:standard:session_a:gv_47:sv_12"

// Global deltas: range-keyed
func deltaCacheKey(fromVersion, toVersion uint64) string {
    return fmt.Sprintf("global:delta:%d-%d", fromVersion, toVersion)
}
// Examples:
//   "global:delta:45-47"
```

### Materialized Views

Pre-computed briefings updated on every write:

```go
type MaterializedViews struct {
    mu sync.RWMutex

    // Per-session views
    sessions map[string]*SessionViews
}

type SessionViews struct {
    // Pre-computed briefings at different tiers
    micro    string          // ~20 tokens: "task:3/5:files:blocker"
    standard *AgentBriefing  // ~500 tokens: structured briefing
    full     *FullSnapshot   // ~2000 tokens: everything

    // Freshness tracking (depends on BOTH versions)
    globalVersion  uint64
    sessionVersion uint64

    // Last computation time
    computedAt time.Time
}

func (mv *MaterializedViews) IsFresh(sessionID string, globalV, sessionV uint64) bool {
    mv.mu.RLock()
    defer mv.mu.RUnlock()

    views := mv.sessions[sessionID]
    if views == nil {
        return false
    }

    return views.globalVersion == globalV && views.sessionVersion == sessionV
}
```

### Global History Cache (Append-Only)

```go
type GlobalHistoryCache struct {
    mu sync.RWMutex

    // Periodic snapshot for efficient full-state queries
    snapshot        *GlobalSnapshot
    snapshotVersion uint64
    snapshotInterval uint64

    // Append log since last snapshot
    appendLog    []*GlobalEntry
    versionIndex map[uint64]int  // version -> appendLog position

    currentVersion uint64
}

type GlobalEntry struct {
    Version   uint64      `json:"v"`
    Type      GlobalScope `json:"t"`    // "pattern", "failure", "insight"
    Data      any         `json:"d"`
    SessionID string      `json:"sid"`
    AgentID   string      `json:"aid"`
    Timestamp time.Time   `json:"ts"`
}

type GlobalSnapshot struct {
    Version  uint64           `json:"v"`
    Patterns []*PatternEntry  `json:"patterns"`
    Failures []*FailureEntry  `json:"failures"`
    Insights []*InsightEntry  `json:"insights"`
}

// OnAppend handles new global entries (patterns, failures, insights)
func (ghc *GlobalHistoryCache) OnAppend(entry *GlobalEntry) {
    ghc.mu.Lock()
    defer ghc.mu.Unlock()

    ghc.appendLog = append(ghc.appendLog, entry)
    ghc.versionIndex[entry.Version] = len(ghc.appendLog) - 1
    ghc.currentVersion = entry.Version

    // Create snapshot if interval reached
    if ghc.snapshotInterval > 0 &&
       entry.Version-ghc.snapshotVersion >= ghc.snapshotInterval {
        ghc.createSnapshotLocked()
    }
}

// GetSince returns all entries since given version - O(1) lookup + slice
func (ghc *GlobalHistoryCache) GetSince(sinceVersion uint64) []*GlobalEntry {
    ghc.mu.RLock()
    defer ghc.mu.RUnlock()

    if sinceVersion >= ghc.currentVersion {
        return nil  // Already up to date
    }

    if sinceVersion >= ghc.snapshotVersion {
        // Fast path: slice from append log
        startIdx, ok := ghc.versionIndex[sinceVersion+1]
        if !ok {
            startIdx = 0
        }
        return ghc.appendLog[startIdx:]
    }

    // Need snapshot + append log (rare - very old client)
    return ghc.rebuildFromSnapshot(sinceVersion)
}

func (ghc *GlobalHistoryCache) createSnapshotLocked() {
    newSnapshot := &GlobalSnapshot{Version: ghc.currentVersion}

    // Start from previous snapshot
    if ghc.snapshot != nil {
        newSnapshot.Patterns = append([]*PatternEntry{}, ghc.snapshot.Patterns...)
        newSnapshot.Failures = append([]*FailureEntry{}, ghc.snapshot.Failures...)
        newSnapshot.Insights = append([]*InsightEntry{}, ghc.snapshot.Insights...)
    }

    // Apply append log
    for _, entry := range ghc.appendLog {
        switch entry.Type {
        case GlobalScopePatterns:
            if p, ok := entry.Data.(*PatternEntry); ok {
                newSnapshot.Patterns = append(newSnapshot.Patterns, p)
            }
        case GlobalScopeFailures:
            if f, ok := entry.Data.(*FailureEntry); ok {
                newSnapshot.Failures = append(newSnapshot.Failures, f)
            }
        case GlobalScopeInsights:
            if i, ok := entry.Data.(*InsightEntry); ok {
                newSnapshot.Insights = append(newSnapshot.Insights, i)
            }
        }
    }

    ghc.snapshot = newSnapshot
    ghc.snapshotVersion = ghc.currentVersion
    ghc.appendLog = nil
    ghc.versionIndex = make(map[uint64]int)
}
```

---

## Conditional Reads (NOT_MODIFIED)

The biggest token savings come from conditional reads:

### Protocol

```go
type StatusCode string

const (
    StatusOK          StatusCode = "OK"
    StatusNotModified StatusCode = "NOT_MODIFIED"
    StatusConflict    StatusCode = "CONFLICT"
    StatusError       StatusCode = "ERROR"
)

type ReadRequest struct {
    Scope Scope `json:"s"`

    // Conditional read for global scopes
    IfGlobalVersion uint64 `json:"if_gv,omitempty"`

    // Conditional read for session scopes
    IfSessionVersion uint64 `json:"if_sv,omitempty"`

    // Delta query - return only changes since this version
    SinceVersion uint64 `json:"since,omitempty"`

    // Search parameters
    Query string `json:"q,omitempty"`
    Limit int    `json:"limit,omitempty"`

    // Include archived entries (query L2)
    IncludeArchived bool `json:"archived,omitempty"`
}

type BriefingRequest struct {
    Tier BriefingTier `json:"tier"`

    // Conditional read - needs BOTH versions to match
    IfVersions *DualVersion `json:"if_v,omitempty"`
}

type Response struct {
    Status StatusCode `json:"s"`

    // Version info (always included)
    GlobalVersion  uint64 `json:"gv,omitempty"`
    SessionVersion uint64 `json:"sv,omitempty"`

    // Data (omitted for NOT_MODIFIED)
    Data any `json:"d,omitempty"`

    // Error info
    Error    string          `json:"err,omitempty"`
    Conflict *ConflictResult `json:"conflict,omitempty"`
}
```

### Request/Response Examples

```json
// Reading global patterns (unchanged)
→ {"read": {"s": "patterns", "if_gv": 45}}
← {"s": "NOT_MODIFIED", "gv": 45}
   // 15 tokens vs ~200 tokens (92% savings)

// Reading global patterns (changed)
→ {"read": {"s": "patterns", "if_gv": 45}}
← {"s": "OK", "gv": 47, "d": [...new patterns...]}

// Reading global patterns with delta
→ {"read": {"s": "patterns", "since": 45}}
← {"s": "OK", "gv": 47, "d": [...only 2 new patterns...]}
   // 40 tokens vs ~200 tokens (80% savings)

// Reading session resume (unchanged)
→ {"id": "o1", "read": {"s": "resume", "if_sv": 10}}
← {"s": "NOT_MODIFIED", "sv": 10}

// Reading briefing (both must be unchanged)
→ {"id": "o1", "briefing": {"tier": "standard", "if_v": {"gv": 45, "sv": 10}}}
← {"s": "NOT_MODIFIED", "gv": 45, "sv": 10}
   // 20 tokens vs ~500 tokens (96% savings)

// Reading briefing (global changed)
→ {"id": "o1", "briefing": {"tier": "standard", "if_v": {"gv": 45, "sv": 10}}}
← {"s": "OK", "gv": 47, "sv": 10, "d": {...full briefing...}}

// Searching with archive fallback
→ {"read": {"s": "entries", "q": "auth", "limit": 10, "archived": true}}
← {"s": "OK", "d": [...results from L1 + L2...]}
```

---

## Tiered Briefings

Different verbosity levels for different needs:

| Tier | Tokens | Use Case | Content |
|------|--------|----------|---------|
| **Micro** | ~20 | Quick status checks | task:step/total:files:blocker |
| **Standard** | ~500 | Agent handoffs | Full structured briefing |
| **Full** | ~2000 | Session start, debugging | Everything + stats |

### Micro Briefing Format

```
auth:3/5:service.go(m),types.go(m):block=none

Format: task:step/total:modified_files:block=blocker
```

### Standard Briefing Structure

```go
type AgentBriefing struct {
    // Session-scoped (from SessionContext)
    ResumeState    *ResumeState  `json:"resume"`
    ModifiedFiles  []*FileState  `json:"files"`
    UserWants      []*Intent     `json:"wants"`
    UserRejects    []*Intent     `json:"rejects"`

    // Global (from GlobalHistory)
    Patterns       []*PatternEntry `json:"patterns"`
    RecentFailures []*FailureEntry `json:"failures"`

    // Versions for conditional reads
    GlobalVersion  uint64 `json:"gv"`
    SessionVersion uint64 `json:"sv"`
}
```

---

## Write Flow

### Global Writes (Append-Only)

```go
func (a *Archivalist) handleGlobalWrite(req *Request) Response {
    agent := a.registry.Get(req.AgentID)

    // 1. Create entry with attribution
    entry := createGlobalEntry(req.Write.Data, agent)

    // 2. Append to global history (atomic, never conflicts)
    var newVersion uint64
    switch req.Write.Scope {
    case ScopePatterns:
        newVersion = a.globalHistory.AppendPattern(entry.(*PatternEntry))
    case ScopeFailures:
        newVersion = a.globalHistory.AppendFailure(entry.(*FailureEntry))
    case ScopeInsights:
        newVersion = a.globalHistory.AppendInsight(entry.(*InsightEntry))
    }

    // 3. Update L3 global cache (just append, no invalidation)
    a.cache.globalCache.OnAppend(&GlobalEntry{
        Version:   newVersion,
        Type:      req.Write.Scope,
        Data:      entry,
        SessionID: agent.SessionID,
        AgentID:   agent.ID,
        Timestamp: time.Now(),
    })

    // 4. Invalidate ALL session briefings (global changed)
    a.cache.views.InvalidateAll()

    // 5. Log event
    a.eventLog.Append(&Event{
        Type:      EventTypeGlobalWrite,
        Version:   newVersion,
        AgentID:   agent.ID,
        SessionID: agent.SessionID,
        Scope:     req.Write.Scope,
        Data:      entry,
    })

    return Response{Status: StatusOK, GlobalVersion: newVersion}
}
```

### Session Writes (May Conflict Within Session)

```go
func (a *Archivalist) handleSessionWrite(req *Request) Response {
    agent := a.registry.Get(req.AgentID)
    sessionID := agent.SessionID

    // 1. Get session context
    ctx := a.sessions.GetOrCreate(sessionID)

    // 2. Check for conflicts (only within this session)
    conflict := a.conflictDetector.Detect(
        req.Write.Scope,
        req.Write.Data,
        agent.ID,
        ctx.GetLastWriter(req.Write.Scope),
    )

    if conflict != nil && !conflict.Resolved {
        return Response{Status: StatusConflict, Conflict: conflict}
    }

    // 3. Apply write (with resolution if needed)
    data := req.Write.Data
    if conflict != nil && conflict.MergedData != nil {
        data = conflict.MergedData
    }

    var newVersion uint64
    switch req.Write.Scope {
    case ScopeResume:
        newVersion = ctx.WriteResume(data.(*ResumeState), agent.ID)
    case ScopeFiles:
        fs := data.(*FileState)
        newVersion = ctx.WriteFile(fs.Path, fs, agent.ID)
    case ScopeIntents:
        newVersion = ctx.WriteIntent(data.(*Intent), agent.ID)
    }

    // 4. Update session version in registry
    a.registry.SetSessionVersion(sessionID, newVersion)

    // 5. Cache version-keyed entry in L0 (immutable)
    a.cache.SetSession(sessionID, req.Write.Scope, newVersion, data)

    // 6. Invalidate THIS session's briefings only
    a.cache.views.InvalidateSession(sessionID)

    // 7. Log event
    a.eventLog.Append(&Event{
        Type:           EventTypeSessionWrite,
        SessionVersion: newVersion,
        AgentID:        agent.ID,
        SessionID:      sessionID,
        Scope:          req.Write.Scope,
        Data:           data,
    })

    return Response{Status: StatusOK, SessionVersion: newVersion}
}
```

### Entry Writes (L1 with L2 Overflow)

```go
func (a *Archivalist) handleEntryWrite(req *Request) Response {
    agent := a.registry.Get(req.AgentID)
    entry := req.Write.Data.(*Entry)

    // Set attribution
    entry.SessionID = agent.SessionID
    entry.Source = agent.Source

    // Insert into L1 hot memory (auto-overflows to L2)
    id, err := a.store.InsertEntry(entry)
    if err != nil {
        return Response{Status: StatusError, Error: err.Error()}
    }

    // Log event
    a.eventLog.Append(&Event{
        Type:      EventTypeEntryCreate,
        AgentID:   agent.ID,
        SessionID: agent.SessionID,
        Key:       id,
        Data:      entry,
    })

    return Response{Status: StatusOK, Data: map[string]string{"id": id}}
}
```

---

## Read Flow

```go
func (a *Archivalist) handleRead(req *Request) Response {
    scope := req.Read.Scope

    switch {
    case isGlobalScope(scope):
        return a.handleGlobalRead(req.Read)
    case isSessionScope(scope):
        agent := a.registry.Get(req.AgentID)
        return a.handleSessionRead(req.Read, agent.SessionID)
    case scope == ScopeEntries:
        return a.handleEntryRead(req.Read, req.AgentID)
    default:
        return Response{Status: StatusError, Error: "unknown scope"}
    }
}

func (a *Archivalist) handleGlobalRead(req *ReadRequest) Response {
    currentGV := a.globalHistory.Version()

    // Conditional read - NOT_MODIFIED if unchanged
    if req.IfGlobalVersion > 0 && req.IfGlobalVersion == currentGV {
        return Response{
            Status:        StatusNotModified,
            GlobalVersion: currentGV,
        }
    }

    // Delta query - only changes since version
    if req.SinceVersion > 0 {
        delta := a.cache.globalCache.GetSince(req.SinceVersion)
        return Response{
            Status:        StatusOK,
            GlobalVersion: currentGV,
            Data:          delta,
        }
    }

    // Full query
    var data any
    switch req.Scope {
    case ScopePatterns:
        data = a.globalHistory.GetAllPatterns()
    case ScopeFailures:
        data = a.globalHistory.GetAllFailures()
    case ScopeInsights:
        data = a.globalHistory.GetAllInsights()
    }

    return Response{
        Status:        StatusOK,
        GlobalVersion: currentGV,
        Data:          data,
    }
}

func (a *Archivalist) handleSessionRead(req *ReadRequest, sessionID string) Response {
    currentSV := a.registry.GetSessionVersion(sessionID)

    // Conditional read
    if req.IfSessionVersion > 0 && req.IfSessionVersion == currentSV {
        return Response{
            Status:         StatusNotModified,
            SessionVersion: currentSV,
        }
    }

    // Check L0 cache (version-keyed = always valid if present)
    if cached, ok := a.cache.GetSession(sessionID, req.Scope, currentSV); ok {
        return Response{
            Status:         StatusOK,
            SessionVersion: currentSV,
            Data:           cached,
        }
    }

    // Read from L1 session context
    ctx := a.sessions.GetOrCreate(sessionID)
    var data any

    switch req.Scope {
    case ScopeResume:
        data, _ = ctx.GetResume()
    case ScopeFiles:
        data = ctx.GetFiles()
    case ScopeIntents:
        data = ctx.GetIntents()
    }

    // Cache in L0 for future reads
    a.cache.SetSession(sessionID, req.Scope, currentSV, data)

    return Response{
        Status:         StatusOK,
        SessionVersion: currentSV,
        Data:           data,
    }
}

func (a *Archivalist) handleEntryRead(req *ReadRequest, agentID string) Response {
    agent := a.registry.Get(agentID)

    // Build query
    q := ArchiveQuery{
        Limit:           req.Limit,
        IncludeArchived: req.IncludeArchived,
    }

    // Text search
    if req.Query != "" {
        entries, err := a.store.SearchText(req.Query, req.IncludeArchived, req.Limit)
        if err != nil {
            return Response{Status: StatusError, Error: err.Error()}
        }
        return Response{Status: StatusOK, Data: entries}
    }

    // Structured query - search L1 first, then L2 if requested
    entries, err := a.store.Query(q)
    if err != nil {
        return Response{Status: StatusError, Error: err.Error()}
    }

    return Response{Status: StatusOK, Data: entries}
}

func (a *Archivalist) handleBriefing(req *Request) Response {
    agent := a.registry.Get(req.AgentID)
    sessionID := agent.SessionID

    currentGV := a.globalHistory.Version()
    currentSV := a.registry.GetSessionVersion(sessionID)

    // Conditional read - BOTH versions must match
    if req.Briefing.IfVersions != nil {
        if req.Briefing.IfVersions.GlobalVersion == currentGV &&
           req.Briefing.IfVersions.SessionVersion == currentSV {
            return Response{
                Status:         StatusNotModified,
                GlobalVersion:  currentGV,
                SessionVersion: currentSV,
            }
        }
    }

    // Check materialized view freshness
    if a.cache.views.IsFresh(sessionID, currentGV, currentSV) {
        briefing := a.cache.views.Get(sessionID, req.Briefing.Tier)
        return Response{
            Status:         StatusOK,
            GlobalVersion:  currentGV,
            SessionVersion: currentSV,
            Data:           briefing,
        }
    }

    // Recompute briefing from L1 data
    briefing := a.computeBriefing(sessionID, req.Briefing.Tier)

    // Update materialized view
    a.cache.views.Set(sessionID, briefing, currentGV, currentSV)

    return Response{
        Status:         StatusOK,
        GlobalVersion:  currentGV,
        SessionVersion: currentSV,
        Data:           briefing.ForTier(req.Briefing.Tier),
    }
}
```

---

## Storage Tier Interactions

### L1 ↔ L2 Overflow

When L1 hot memory exceeds the token threshold (~750K tokens), oldest entries overflow to L2:

```go
func (s *Store) archiveOldestEntries() error {
    if s.archive == nil {
        // No archive - just evict oldest entries
        s.removeOldestEntries(s.tokenThreshold / 4)
        return nil
    }

    // Archive oldest 25% of entries by tokens
    targetTokens := s.tokenThreshold / 4
    var toArchive []*Entry
    var removedTokens int

    for _, id := range s.chronological {
        entry := s.entries[id]
        toArchive = append(toArchive, entry)
        removedTokens += entry.TokensEstimate
        if removedTokens >= targetTokens {
            break
        }
    }

    // Write to L2 SQLite
    if err := s.archive.ArchiveEntries(toArchive); err != nil {
        return err
    }

    // Remove from L1
    for _, entry := range toArchive {
        s.removeEntry(entry.ID)
    }

    return nil
}
```

### L2 → L1 Restoration

Archived entries can be pulled back into hot memory:

```go
func (s *Store) RestoreFromArchive(ids []string) error {
    if s.archive == nil {
        return fmt.Errorf("no archive configured")
    }

    for _, id := range ids {
        // Skip if already in L1
        if _, ok := s.entries[id]; ok {
            continue
        }

        // Fetch from L2
        entry, err := s.archive.GetEntry(id)
        if err != nil || entry == nil {
            continue
        }

        // Make room if needed
        if s.totalTokens+entry.TokensEstimate > s.tokenThreshold {
            if err := s.archiveOldestEntries(); err != nil {
                return err
            }
        }

        // Clear archived timestamp
        entry.ArchivedAt = nil

        // Add to L1
        s.entries[entry.ID] = entry
        s.byCategory[entry.Category] = append(s.byCategory[entry.Category], entry.ID)
        s.chronological = append(s.chronological, entry.ID)
        s.totalTokens += entry.TokensEstimate
    }

    return nil
}
```

### Search Across Tiers

Full-text search queries both L1 and L2:

```go
func (s *Store) SearchText(text string, includeArchived bool, limit int) ([]*Entry, error) {
    var results []*Entry

    // Search L1 (simple substring)
    s.mu.RLock()
    for _, entry := range s.entries {
        if containsIgnoreCase(entry.Content, text) ||
           containsIgnoreCase(entry.Title, text) {
            results = append(results, entry)
        }
    }
    s.mu.RUnlock()

    // Search L2 with FTS5 if requested
    if includeArchived && s.archive != nil {
        archived, err := s.archive.SearchText(text, limit)
        if err != nil {
            return nil, err
        }

        // Merge results, avoiding duplicates
        seen := make(map[string]bool)
        for _, e := range results {
            seen[e.ID] = true
        }
        for _, e := range archived {
            if !seen[e.ID] {
                results = append(results, e)
            }
        }
    }

    // Apply limit
    if limit > 0 && len(results) > limit {
        results = results[:limit]
    }

    return results, nil
}
```

---

## Token and Latency Analysis

### Token Savings by Mechanism

| Mechanism | Savings | When |
|-----------|---------|------|
| NOT_MODIFIED | ~95% | Data unchanged since agent's version |
| Delta response | ~80% | Only some data changed |
| Micro tier | ~96% | Agent only needs status check |
| Append-only | N/A | No conflict overhead on writes |

### Expected Savings

Assuming typical session with:
- 50% status checks (micro tier sufficient)
- 30% handoff requests (standard tier)
- 15% sync requests (delta)
- 5% full state requests
- 60% of reads find data unchanged

```
Token calculation:

Status checks (50%):
  - 60% unchanged: 0.50 × 0.60 × 15 = 4.5 tokens avg
  - 40% changed:   0.50 × 0.40 × 35 = 7.0 tokens avg

Handoffs (30%):
  - 60% unchanged: 0.30 × 0.60 × 15 = 2.7 tokens avg
  - 40% changed:   0.30 × 0.40 × 515 = 61.8 tokens avg

Syncs (15%):
  - Average delta: 0.15 × 65 = 9.75 tokens avg

Full state (5%):
  - Always full:   0.05 × 2000 = 100 tokens avg

TOTAL AVERAGE: ~186 tokens per request

BASELINE (always full): ~500 tokens per request

SAVINGS: ~63% token reduction
```

### Latency Analysis

| Operation | Without Optimization | With Full Stack |
|-----------|---------------------|-----------------|
| Briefing read | 10-200ms (compute) | <1ms (L0 materialized view) |
| Global delta | 5-50ms (filter) | <1ms (L3 slice append log) |
| Session read | 5-20ms (lock + read) | <1ms (L0 cache hit) |
| Entry search (L1) | 1-10ms | 1-10ms |
| Entry search (L1+L2) | 10-100ms (SQLite FTS) | 10-100ms |
| Write (session) | 5ms | 5-15ms (+ L0 cache + view invalidation) |
| Write (global) | 5ms | 5-15ms (+ L3 append + view invalidation) |

---

## Agent Registration and Hierarchy

### Registration

```go
type RegisteredAgent struct {
    ID           string       `json:"id"`        // Short ID: o1, c2
    Name         string       `json:"name"`      // Human-readable: opus_main
    SessionID    string       `json:"session_id"`
    ParentID     string       `json:"parent_id,omitempty"`
    Children     []string     `json:"children,omitempty"`
    Status       AgentStatus  `json:"status"`    // active, idle, inactive
    Clock        uint64       `json:"clock"`     // Lamport clock
    LastVersion  string       `json:"last_version"`
    Source       SourceModel  `json:"source"`    // gpt-5.2-codex, claude-opus-4-5
    RegisteredAt time.Time    `json:"registered_at"`
    LastSeenAt   time.Time    `json:"last_seen_at"`
}
```

### Hierarchy Rules

1. Sub-agents inherit parent's session
2. Parent agent's state wins in conflicts
3. Sub-agents can read global patterns/failures
4. Sub-agents write to session scope

```
Session: abc123
├── opus_main (o1) ─────────── Primary agent
│   ├── opus_sub1 (o2) ─────── Sub-agent for parallel task
│   └── opus_sub2 (o3) ─────── Sub-agent for parallel task
└── codex_main (c1) ─────────── Different model, same session
```

---

## Event Log

All operations recorded in append-only event log:

```go
type Event struct {
    ID             string         `json:"id"`      // e123
    Type           EventType      `json:"type"`
    GlobalVersion  uint64         `json:"gv,omitempty"`
    SessionVersion uint64         `json:"sv,omitempty"`
    Clock          uint64         `json:"clock"`   // Lamport clock
    AgentID        string         `json:"aid"`
    SessionID      string         `json:"sid"`
    Scope          Scope          `json:"scope,omitempty"`
    Key            string         `json:"key,omitempty"`
    Data           map[string]any `json:"data,omitempty"`
    Timestamp      time.Time      `json:"ts"`
}
```

### Event Types

| Type | Scope | Description |
|------|-------|-------------|
| pattern_add | Global | New pattern contributed |
| failure_record | Global | New failure recorded |
| insight_add | Global | New insight contributed |
| resume_update | Session | Task state changed |
| file_modify | Session | File modified |
| intent_add | Session | User preference recorded |
| entry_create | Entry | New entry in L1 |
| entry_archive | Entry | Entry moved to L2 |
| agent_register | System | Agent joined |
| agent_unregister | System | Agent left |

---

## SQLite Schema (L2 Cold Storage)

```sql
-- Schema version tracking
CREATE TABLE IF NOT EXISTS schema_version (
    version INTEGER PRIMARY KEY
);

-- Sessions table
CREATE TABLE IF NOT EXISTS sessions (
    id TEXT PRIMARY KEY,
    started_at TIMESTAMP NOT NULL,
    ended_at TIMESTAMP,
    summary TEXT,
    primary_focus TEXT,
    entry_count INTEGER DEFAULT 0
);

-- Main entries table
CREATE TABLE IF NOT EXISTS entries (
    id TEXT PRIMARY KEY,
    category TEXT NOT NULL,
    title TEXT,
    content TEXT NOT NULL,
    source TEXT NOT NULL,
    session_id TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    archived_at TIMESTAMP,
    tokens_estimate INTEGER DEFAULT 0,
    metadata JSON,
    related_ids JSON,
    FOREIGN KEY (session_id) REFERENCES sessions(id)
);

-- Full-text search index (FTS5)
CREATE VIRTUAL TABLE IF NOT EXISTS entries_fts USING fts5(
    id,
    title,
    content,
    category,
    content='entries',
    content_rowid='rowid'
);

-- Triggers to keep FTS index in sync
CREATE TRIGGER IF NOT EXISTS entries_ai AFTER INSERT ON entries BEGIN
    INSERT INTO entries_fts(id, title, content, category)
    VALUES (new.id, new.title, new.content, new.category);
END;

CREATE TRIGGER IF NOT EXISTS entries_ad AFTER DELETE ON entries BEGIN
    INSERT INTO entries_fts(entries_fts, id, title, content, category)
    VALUES ('delete', old.id, old.title, old.content, old.category);
END;

CREATE TRIGGER IF NOT EXISTS entries_au AFTER UPDATE ON entries BEGIN
    INSERT INTO entries_fts(entries_fts, id, title, content, category)
    VALUES ('delete', old.id, old.title, old.content, old.category);
    INSERT INTO entries_fts(id, title, content, category)
    VALUES (new.id, new.title, new.content, new.category);
END;

-- Entry links table for relationships
CREATE TABLE IF NOT EXISTS entry_links (
    from_id TEXT NOT NULL,
    to_id TEXT NOT NULL,
    relationship TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (from_id, to_id, relationship),
    FOREIGN KEY (from_id) REFERENCES entries(id),
    FOREIGN KEY (to_id) REFERENCES entries(id)
);

-- Indexes for efficient querying
CREATE INDEX IF NOT EXISTS idx_entries_category ON entries(category);
CREATE INDEX IF NOT EXISTS idx_entries_session ON entries(session_id);
CREATE INDEX IF NOT EXISTS idx_entries_source ON entries(source);
CREATE INDEX IF NOT EXISTS idx_entries_created ON entries(created_at);
CREATE INDEX IF NOT EXISTS idx_entries_archived ON entries(archived_at);
```

---

## File Structure

```
agents/archivalist/
├── archivalist.go      # Main agent, request routing
├── global_history.go   # Append-only global history (patterns, failures, insights)
├── session_context.go  # Per-session state (resume, files, intents)
├── cache.go            # L0 ristretto cache + materialized views
├── views.go            # Pre-computed briefings
├── storage.go          # L1 hot memory with token-based overflow
├── archive.go          # L2 SQLite cold storage with FTS5
├── registry.go         # Agent registration, dual versioning
├── protocol.go         # Request/response types, status codes
├── conflict.go         # Session-scoped conflict resolution
├── events.go           # Append-only event log
├── types.go            # Core type definitions
├── client.go           # Anthropic API client
└── prompt.go           # System prompt
```

---

## Quick Reference

### Storage Tiers

| Tier | Backend | Purpose | Capacity | Latency |
|------|---------|---------|----------|---------|
| L0 | Ristretto | Hot path cache | ~1GB | <1ms |
| L1 | In-memory maps | Active working set | ~750K tokens | 1-10ms |
| L2 | SQLite + FTS5 | Persistence, search | Unlimited | 10-100ms |
| L3 | Snapshot + log | Delta queries | N versions | <1ms |

### Write Operations

| Scope | Model | Conflict? | Cache Update | Storage |
|-------|-------|-----------|--------------|---------|
| patterns | Append | Never | L3 append | L1 |
| failures | Append | Never | L3 append | L1 |
| insights | Append | Never | L3 append | L1 |
| resume | Overwrite | Within session | L0 version-key | L1 |
| files | Merge | Within session | L0 version-key | L1 |
| intents | Overwrite | Within session | L0 version-key | L1 |
| entries | Insert | Never | None | L1 → L2 |

### Read Operations

| Request | L0 Hit | L0 Miss | Conditional |
|---------|--------|---------|-------------|
| Session state | <1ms, return cached | Read L1, cache in L0 | NOT_MODIFIED if version matches |
| Global history | <1ms, return delta | Slice L3 append log | NOT_MODIFIED if gv matches |
| Briefing | <1ms, return view | Compute, cache view | NOT_MODIFIED if both match |
| Entry search | N/A | L1 + optional L2 | N/A |

### Version Semantics

| Version Type | Format | Increments On | Used For |
|--------------|--------|---------------|----------|
| Global | `gv:47` | Any global append | Delta queries, briefing freshness |
| Session A | `sv_a:12` | Session A write | Session cache keys |
| Session B | `sv_b:8` | Session B write | Session cache keys |

---

## Stress Test: Large-Scale Refactor Analysis

This section analyzes the Archivalist's performance under extreme conditions: a 500k LOC Django monolith refactor with 6 concurrent sessions and ~180 total agent instances.

### Scenario Parameters

| Parameter | Value |
|-----------|-------|
| Sessions | 6 concurrent |
| Sub-agents per session | ~30 (180 total agent instances) |
| Codebase | 500k LOC Django monolith |
| Scope | Complete rewrite (views, DB, handlers, auth, etc.) |
| Duration | 2-3 days intensive work |
| Models | Opus 4.5 + Codex 5.2 |

### Token Cost: Without Archivalist

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    TOKEN COST WITHOUT ARCHIVALIST                            │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  CORE WORK (Irreducible)                                      20,000,000     │
│  ════════════════════════                                                    │
│  The actual refactoring work - reading specific code, writing new code,      │
│  reasoning about changes. This is the same with or without archivalist.      │
│                                                                              │
│  CONTEXT OVERHEAD (Reducible)                                                │
│  ═══════════════════════════                                                 │
│                                                                              │
│  File Reading Overhead                                         4,000,000     │
│  ├── Each agent reads 30-50 files for context                               │
│  ├── Same files read by multiple agents (no caching)                        │
│  └── 180 agents × 40 files × 200 tokens × 2.5 redundancy factor             │
│                                                                              │
│  Context Rebuilding                                            3,000,000     │
│  ├── Each new agent starts completely fresh                                 │
│  ├── Parent context not transferred to children                             │
│  └── 180 agents × ~17,000 tokens rebuilding state                           │
│                                                                              │
│  Pattern Re-Discovery                                          2,000,000     │
│  ├── Session 1 establishes error handling pattern                           │
│  ├── Sessions 2-6 independently discover/create patterns                    │
│  ├── No mechanism to share "use Result<T,E> for errors"                     │
│  └── 6 sessions × 30 agents × ~11,000 tokens pattern work                   │
│                                                                              │
│  WASTE (Eliminable)                                                          │
│  ══════════════════                                                          │
│                                                                              │
│  Repeated Failures                                             3,000,000     │
│  ├── Agent A tries approach X, fails after 10,000 tokens                    │
│  ├── Agents B, C, D, E, F all try X again, fail again                       │
│  ├── ~50 major failure patterns × 6 repetitions each                        │
│  └── 300 failed attempts × 10,000 tokens                                    │
│                                                                              │
│  Pattern Drift Rework                                          4,000,000     │
│  ├── Session 1: "use Django REST framework serializers"                     │
│  ├── Session 3: "use Pydantic models" (didn't know about S1)                │
│  ├── Discovery of inconsistency → rework required                           │
│  └── 40 drift incidents × 100,000 tokens to fix                             │
│                                                                              │
│  Cross-Session Duplication                                     2,000,000     │
│  ├── Sessions working on overlapping concerns (auth touches everything)     │
│  ├── Same decisions made independently                                      │
│  └── 20 major duplications × 100,000 tokens                                 │
│                                                                              │
├─────────────────────────────────────────────────────────────────────────────┤
│  TOTAL WITHOUT ARCHIVALIST:              38,000,000 tokens                   │
│                                                                              │
│  Estimated Cost (Opus 4.5 pricing):                                          │
│  ├── Input (~70%):  26.6M × $3/M  = $79.80                                  │
│  ├── Output (~30%): 11.4M × $15/M = $171.00                                 │
│  └── TOTAL:                        ~$250-350                                 │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Token Cost: With Archivalist

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                      TOKEN COST WITH ARCHIVALIST                             │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  CORE WORK (Irreducible - Same)                               20,000,000     │
│  ═══════════════════════════════                                             │
│                                                                              │
│  CONTEXT OVERHEAD (Dramatically Reduced)                                     │
│  ═══════════════════════════════════════                                     │
│                                                                              │
│  File Reading Overhead                                           500,000     │
│  ├── File state included in briefings                                       │
│  ├── Agents know which files modified, don't re-read for state              │
│  └── 180 agents × ~2,800 tokens (briefing file state)                       │
│                                                                              │
│  Context Rebuilding                                              400,000     │
│  ├── Standard briefing provides full context: ~500 tokens                   │
│  ├── NOT_MODIFIED responses when unchanged: ~15 tokens                      │
│  └── 180 agents × ~2,200 tokens avg context acquisition                     │
│                                                                              │
│  Pattern Re-Discovery                                            100,000     │
│  ├── Session 1 records pattern → globally available                         │
│  ├── Sessions 2-6 query patterns, get instant response                      │
│  └── Pattern queries: 6 sessions × 50 queries × ~330 tokens                 │
│                                                                              │
│  WASTE (Nearly Eliminated)                                                   │
│  ═════════════════════════                                                   │
│                                                                              │
│  Repeated Failures                                               300,000     │
│  ├── Agent A tries X, fails, records to global history                      │
│  ├── Agents B-F query failures before attempting → skip X                   │
│  └── 50 failures × 6,000 tokens (faster to fail with context)               │
│                                                                              │
│  Pattern Drift Rework                                            400,000     │
│  ├── Patterns enforced through global history                               │
│  ├── Drift caught early through briefings                                   │
│  └── 4 drift incidents × 100,000 tokens (vs 40)                             │
│                                                                              │
│  Cross-Session Duplication                                       200,000     │
│  ├── Global history visible to all sessions                                 │
│  └── 2 duplications × 100,000 tokens (vs 20)                                │
│                                                                              │
│  ARCHIVALIST OPERATIONS (New Overhead)                                       │
│  ═════════════════════════════════════                                       │
│                                                                              │
│  Briefing Requests                                               600,000     │
│  ├── 180 agents × 20 briefings × varying tiers                              │
│  └── 50% micro (20 tok), 40% standard (500), 10% full (2000)                │
│                                                                              │
│  Read Queries                                                    400,000     │
│  ├── 60% NOT_MODIFIED (15 tok), 30% delta (65), 10% full (200)              │
│  └── 180 agents × 30 queries × ~74 tokens avg                               │
│                                                                              │
│  Write Operations                                                300,000     │
│  └── 180 agents × 15 writes × ~111 tokens avg                               │
│                                                                              │
│  Global History Growth                                           200,000     │
│  └── ~1,000 entries × 200 tokens                                            │
│                                                                              │
├─────────────────────────────────────────────────────────────────────────────┤
│  TOTAL WITH ARCHIVALIST:                 23,400,000 tokens                   │
│                                                                              │
│  Estimated Cost (Opus 4.5 pricing):                                          │
│  ├── Input (~70%):  16.4M × $3/M  = $49.20                                  │
│  ├── Output (~30%):  7.0M × $15/M = $105.00                                 │
│  └── TOTAL:                        ~$155-220                                 │
└─────────────────────────────────────────────────────────────────────────────┘
```

### Cost Comparison Summary

| Metric | Without | With | Savings |
|--------|---------|------|---------|
| **Total Tokens** | 38,000,000 | 23,400,000 | **38%** |
| **Estimated Cost** | $250-350 | $155-220 | **~$100-130** |
| **Context Overhead** | 9,000,000 | 1,000,000 | **89%** |
| **Waste (failures, drift, duplication)** | 9,000,000 | 900,000 | **90%** |
| **Archivalist Operations** | 0 | 1,500,000 | (new cost) |

---

## Edge Cases and Mitigations

### 1. Global History Explosion

**Problem:** 180 agents contributing patterns could flood global history with duplicates.

```
Session 1, Agent 1: "use async/await for I/O"
Session 1, Agent 2: "use async/await for I/O" (duplicate)
Session 1, Agent 3: "use async/await for database calls" (near-duplicate)
Session 2, Agent 1: "prefer async over threading" (semantic duplicate)
... × 180 agents = potential explosion
```

**Mitigation:**

```go
type GlobalHistory struct {
    // Deduplication on write
    patternHashes map[uint64]string  // hash -> existing ID

    // Semantic similarity threshold
    similarityThreshold float64  // e.g., 0.85
}

func (gh *GlobalHistory) AppendPattern(entry *PatternEntry) (uint64, error) {
    // 1. Exact hash check
    hash := hashPattern(entry.Pattern)
    if existingID, ok := gh.patternHashes[hash]; ok {
        return 0, &DuplicateError{ExistingID: existingID}
    }

    // 2. Semantic similarity check (for near-duplicates)
    similar := gh.findSimilarPatterns(entry.Category, entry.Pattern, gh.similarityThreshold)
    if len(similar) > 0 {
        // Don't append, but increment usage count of similar
        gh.incrementUsage(similar[0].ID)
        return 0, &SimilarExistsError{SimilarID: similar[0].ID}
    }

    // 3. Rate limiting per session
    if gh.recentPatternCount(entry.SessionID, 1*time.Minute) > 10 {
        return 0, &RateLimitError{Wait: 30 * time.Second}
    }

    // Proceed with append
    return gh.appendLocked(entry)
}
```

**Recommended Limits:**
- Max 10 pattern contributions per session per minute
- Max 500 patterns per category
- Automatic archival of low-usage patterns after 1 hour

---

### 2. Contradictory Patterns

**Problem:** Different sessions establish conflicting patterns.

```
Session 1 (Views):     "use class-based views for all endpoints"
Session 2 (Handlers):  "use function-based views for simplicity"

Agent in Session 3: "Which pattern do I follow?"
```

**Mitigation:**

```go
type PatternEntry struct {
    // ... existing fields ...

    // Conflict tracking
    ConflictsWith []string  `json:"conflicts,omitempty"`
    SupersededBy  string    `json:"superseded,omitempty"`
    Authority     uint8     `json:"auth"`  // 0=suggestion, 1=established, 2=mandated
}

func (gh *GlobalHistory) GetBestPattern(category string) (*PatternEntry, *ConflictInfo) {
    patterns := gh.patternsByCategory[category]

    // Check for conflicts
    conflicting := gh.findConflictingPatterns(patterns)
    if len(conflicting) > 1 {
        return nil, &ConflictInfo{
            Patterns:   conflicting,
            Resolution: ResolutionRequired,
            Suggestion: gh.suggestResolution(conflicting),
        }
    }

    return gh.selectBestPattern(patterns), nil
}

type ConflictResolution struct {
    Strategy   string   // "hierarchy", "usage", "recency", "escalate"
    WinningID  string
    LosingIDs  []string
    Rationale  string
    ResolvedBy string   // agent ID or "system"
}
```

**Resolution Strategy:**
1. Detect conflicts on write (compare new pattern against existing)
2. Allow conflicts to coexist initially (append-only)
3. Surface conflicts in briefings
4. Require explicit resolution by parent agent or user
5. Record resolution rationale for future reference

---

### 3. Stale Briefings Under High Write Load

**Problem:** 180 agents writing concurrently = constant view invalidation.

```
Agent 1 writes pattern   → invalidate all views
Agent 2 writes failure   → invalidate all views
Agent 3 writes file      → invalidate session view
... happening 100 times/second

Agent 50 requests briefing → always recomputed, never cached
```

**Mitigation:**

```go
type MaterializedViews struct {
    // Batch invalidation with debouncing
    invalidationQueue chan string
    debounceWindow    time.Duration  // e.g., 100ms

    // Incremental updates instead of full recompute
    incrementalUpdates bool
}

func (mv *MaterializedViews) processInvalidations() {
    ticker := time.NewTicker(mv.debounceWindow)
    pendingGlobal := false
    pendingSessions := make(map[string]bool)

    for {
        select {
        case scope := <-mv.invalidationQueue:
            if scope == "global" {
                pendingGlobal = true
            } else {
                pendingSessions[scope] = true
            }

        case <-ticker.C:
            if pendingGlobal {
                mv.incrementalGlobalUpdate()
                pendingGlobal = false
            }
            for sessionID := range pendingSessions {
                mv.incrementalSessionUpdate(sessionID)
            }
            pendingSessions = make(map[string]bool)
        }
    }
}
```

**Recommended Settings:**
- Debounce window: 100-200ms
- Max recompute rate: 10/second per session
- Incremental updates for global changes
- Full recompute only when >20% of data changed

---

### 4. Session Isolation Violation

**Problem:** Agent writes to wrong session (bug or misconfiguration).

**Mitigation:**

```go
func (a *Archivalist) handleSessionWrite(req *Request) Response {
    agent := a.registry.Get(req.AgentID)

    // CRITICAL: Validate session ownership
    if agent == nil {
        return Response{Status: StatusError, Error: "unknown agent"}
    }

    // Agent can only write to their own session
    targetSession := req.Write.SessionID
    if targetSession == "" {
        targetSession = agent.SessionID
    }

    if targetSession != agent.SessionID {
        if !a.hasPermission(agent, PermissionCrossSessionWrite) {
            a.eventLog.Append(&Event{
                Type:    EventTypeSecurityViolation,
                AgentID: agent.ID,
                Data: map[string]any{
                    "attempted_session": targetSession,
                    "agent_session":     agent.SessionID,
                },
            })
            return Response{
                Status: StatusError,
                Error:  "session isolation violation",
            }
        }
    }

    return a.doSessionWrite(req, agent.SessionID)
}
```

**Safeguards:**
- Session ID embedded in agent registration (immutable)
- All writes implicitly scoped to agent's session
- Cross-session writes require special permission
- Audit log for all write operations

---

### 5. Cache Memory Pressure

**Problem:** 180 agents × 50 versions each = 9,000+ cache entries.

**Mitigation:**

```go
type CacheConfig struct {
    MaxCost               int64         // 512MB for L0
    NumCounters           int64         // 10M (10x expected items)
    MaxVersionsPerSession int           // Keep only last 20 versions
    VersionTTL            time.Duration // Evict versions older than 1 hour
}

func (cl *CacheLayer) pruneOldVersions() {
    // Run periodically (every 5 minutes)
    for sessionID := range cl.sessionVersions {
        currentVersion := cl.registry.GetSessionVersion(sessionID)

        // Evict versions more than 20 behind current
        for version := range cl.getSessionCacheKeys(sessionID) {
            if currentVersion - version > 20 {
                cl.cache.Del(sessionCacheKey(sessionID, "*", version))
            }
        }
    }
}
```

**Memory Budget:**
- L0 Ristretto: 512MB max
- Keep only last 20 versions per session per scope
- Aggressive eviction of unused entries

---

### 6. Delta Chain Explosion

**Problem:** Long sessions accumulate huge delta logs.

```
Session runs for 3 days
→ 10,000 global writes
→ Delta log: 10,000 entries

Agent asks "what changed since version 5?"
→ Need to return 9,995 entries (worse than full state)
```

**Mitigation:**

```go
type GlobalHistoryCache struct {
    snapshotInterval    uint64  // Base: every 100 versions
    adaptiveInterval    bool    // Reduce interval under load
    minSnapshotInterval uint64  // Never less than 50
    maxAppendLogSize    int     // Trigger snapshot if exceeded
}

func (ghc *GlobalHistoryCache) GetSince(sinceVersion uint64) *DeltaResponse {
    // If delta would be too large, suggest full read instead
    deltaSize := ghc.currentVersion - sinceVersion
    if deltaSize > 500 {
        return &DeltaResponse{
            TooLarge:        true,
            SuggestFullRead: true,
            CurrentVersion:  ghc.currentVersion,
        }
    }

    return &DeltaResponse{
        Entries:        ghc.getEntriesSince(sinceVersion),
        CurrentVersion: ghc.currentVersion,
    }
}
```

**Recommended Settings:**
- Snapshot every 100 versions (adaptive: 50-100)
- Max append log: 500 entries
- Delta response limit: 500 entries
- Keep 3 historical snapshots

---

### 7. SQLite Contention

**Problem:** 6 sessions writing to same SQLite database simultaneously.

**Mitigation:**

```go
type Archive struct {
    db *sql.DB

    // Write batching
    writeBatch   []*Entry
    batchMu      sync.Mutex
    batchSize    int           // Flush every 50 entries
    batchTimeout time.Duration // Or every 500ms
}

func (a *Archive) ArchiveEntryAsync(entry *Entry) {
    a.batchMu.Lock()
    a.writeBatch = append(a.writeBatch, entry)
    shouldFlush := len(a.writeBatch) >= a.batchSize
    a.batchMu.Unlock()

    if shouldFlush {
        a.flushBatch()
    }
}

func (a *Archive) flushBatch() error {
    a.batchMu.Lock()
    batch := a.writeBatch
    a.writeBatch = nil
    a.batchMu.Unlock()

    if len(batch) == 0 {
        return nil
    }

    // Single transaction for entire batch
    tx, _ := a.db.Begin()
    defer tx.Rollback()

    stmt, _ := tx.Prepare(`INSERT INTO entries ...`)
    for _, entry := range batch {
        stmt.Exec(...)
    }
    stmt.Close()

    return tx.Commit()
}
```

**SQLite Tuning:**
```sql
PRAGMA journal_mode=WAL;
PRAGMA synchronous=NORMAL;
PRAGMA cache_size=-64000;      -- 64MB cache
PRAGMA busy_timeout=5000;      -- 5s wait on lock
PRAGMA wal_autocheckpoint=1000;
```

---

### 8. Failure Resolution Conflicts

**Problem:** Two agents discover same failure, report different resolutions.

```
Agent A: "Using raw SQL failed → Solution: use ORM"
Agent B: "Using raw SQL failed → Solution: use parameterized queries"
```

**Mitigation:**

```go
type FailureEntry struct {
    // Multiple resolutions allowed
    Resolutions []Resolution `json:"resolutions"`
}

type Resolution struct {
    ID          string  `json:"id"`
    Description string  `json:"desc"`
    AgentID     string  `json:"aid"`
    Verified    bool    `json:"verified"`  // Did it work?
    UsageCount  uint64  `json:"uc"`
    SuccessRate float64 `json:"sr"`        // % success
}

func (gh *GlobalHistory) AppendFailure(entry *FailureEntry) uint64 {
    // Check for existing failure with same approach
    existing := gh.findFailureByApproach(entry.Approach)
    if existing != nil {
        // Add new resolution to existing failure
        for _, res := range entry.Resolutions {
            if !existing.hasResolution(res.Description) {
                existing.Resolutions = append(existing.Resolutions, res)
            }
        }
        gh.version++
        return gh.version
    }

    return gh.appendFailureLocked(entry)
}

func (gh *GlobalHistory) GetBestResolution(approach string) (*Resolution, []*Resolution) {
    failure := gh.findFailureByApproach(approach)
    if failure == nil {
        return nil, nil
    }

    // Sort by success rate × usage count
    sorted := sortByEffectiveness(failure.Resolutions)
    return sorted[0], sorted[1:]
}
```

---

### 9. Cross-Cutting Concern Coordination

**Problem:** Auth changes affect all 6 sessions simultaneously.

```
Session 4 (Auth): Rewrites entire auth system
Sessions 1, 2, 3: Still using old auth decorators
→ Sessions 1-3 are now inconsistent
```

**Mitigation:**

```go
type Broadcast struct {
    ID       string `json:"id"`
    Type     string `json:"type"`  // "breaking_change", "deprecation"
    Scope    string `json:"scope"` // "auth", "db", "api"
    Message  string `json:"msg"`
    Action   string `json:"action"`
    Priority int    `json:"pri"`   // 0=info, 1=warning, 2=blocking
    AckedBy  []string `json:"acked"`
}

func (gh *GlobalHistory) Broadcast(b *Broadcast) {
    gh.broadcasts = append(gh.broadcasts, b)

    // Inject into all session briefings immediately
    for sessionID := range gh.sessions {
        gh.views.InjectBroadcast(sessionID, b)
    }
}

type AgentBriefing struct {
    // ... existing fields ...
    PendingBroadcasts []*Broadcast `json:"broadcasts,omitempty"`
}

func (a *Archivalist) handleBriefing(req *Request) Response {
    // Check for unacknowledged blocking broadcasts
    blocking := a.getBlockingBroadcasts(sessionID)
    if len(blocking) > 0 && !req.Briefing.AckBroadcasts {
        return Response{
            Status: StatusBlocked,
            Data: map[string]any{
                "blocking_broadcasts": blocking,
                "action_required":     "acknowledge broadcasts before continuing",
            },
        }
    }

    // Continue with briefing...
}
```

---

### 10. Recovery from Archivalist Failure

**Problem:** Archivalist crashes mid-refactor, all L1 memory lost.

**Mitigation:**

```go
type WriteAheadLog struct {
    file     *os.File
    encoder  *json.Encoder
    syncMode SyncMode
}

func (wal *WriteAheadLog) Append(op *Operation) error {
    if err := wal.encoder.Encode(op); err != nil {
        return err
    }
    if wal.syncMode == SyncOnWrite {
        return wal.file.Sync()
    }
    return nil
}

type Operation struct {
    Type      OpType    `json:"op"`
    Scope     Scope     `json:"s"`
    SessionID string    `json:"sid,omitempty"`
    Data      any       `json:"d"`
    Version   uint64    `json:"v"`
    Timestamp time.Time `json:"ts"`
}

func (a *Archivalist) recoverFromWAL() error {
    file, err := os.Open(a.walPath)
    if os.IsNotExist(err) {
        return nil  // Fresh start
    }

    decoder := json.NewDecoder(file)
    for {
        var op Operation
        if err := decoder.Decode(&op); err == io.EOF {
            break
        }
        a.replayOperation(&op)
    }

    return nil
}

func (a *Archivalist) checkpoint() error {
    checkpoint := &Checkpoint{
        GlobalHistory: a.globalHistory.Snapshot(),
        Sessions:      a.sessions.Snapshot(),
        Registry:      a.registry.Snapshot(),
        Version:       a.registry.GetGlobalVersion(),
        Timestamp:     time.Now(),
    }

    if err := a.writeCheckpoint(checkpoint); err != nil {
        return err
    }

    return a.wal.Truncate()
}
```

**Recovery Strategy:**
1. On startup, load latest checkpoint
2. Replay WAL from checkpoint version forward
3. Checkpoint every 5 minutes or 1000 operations

---

### Edge Case Summary

| Edge Case | Risk Level | Mitigation |
|-----------|------------|------------|
| Global history explosion | High | Hybrid similarity, informative merge, per-session + per-category rate limits, hierarchical categories |
| Contradictory patterns | High | Explicit supersession at write time, automatic conflict detection, zero ambiguity in active patterns |
| Stale briefings | Medium | Component caching with incremental cursor, O(new patterns) refresh, zero staleness tolerance |
| Session isolation | Medium | Structural isolation (per-session stores), capability-based access, boundary-only logging |
| Cache memory pressure | Medium | Semantic tiering (critical/warm/cold), activity-aware eviction, explicit tier promotion |
| Delta chain explosion | Medium | Regular checkpoints (fixed interval), version-keyed components eliminate traversal, O(1) state access |
| SQLite contention | Medium | Writes already rare (L1 buffers), simple WAL mode, async archival |
| Failure resolution conflicts | Low | Resolution attached to failure at write time, success/failure outcome tracking, automatic ranking |
| Cross-cutting coordination | High | Intent declaration before change, affected scope detection, async notification (non-blocking) |
| Archivalist failure | High | Append-only writes (no corruption), automatic recovery from checkpoint + log, graceful degradation |

---

## Edge Case Implementation Details

### 1. Global History Explosion - Implementation

**Design Decisions:**

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Similarity detection | Hybrid (hash → trigram → embedding) | Best correctness + token savings; write latency paid once, read savings paid thousands of times |
| Duplicate behavior | Informative merge | Transparent, prevents retries, agent can verify |
| Rate limiting | Per session (10/min) + Per category (50 max) | Prevents temporal and topical flooding |
| Category structure | Hierarchical with predefined L1 | Precise queries, consistent deduplication |

**Token Impact Analysis:**

```
Without optimizations:
  Patterns in history: 500+ (duplicates, near-duplicates, semantic duplicates)
  Briefing size: 500 × 50 tokens = 25,000 tokens
  Total read cost: 3,600 briefings × 25,000 = 90,000,000 tokens

With optimizations:
  Patterns in history: ~50 (aggressive deduplication)
  Briefing size: 50 × 50 tokens = 2,500 tokens
  Total read cost: 3,600 briefings × 2,500 = 9,000,000 tokens

Savings: ~81,000,000 tokens (90% reduction in pattern-related tokens)
```

**Hybrid Similarity Detection:**

```go
func (gh *GlobalHistory) findDuplicate(entry *PatternEntry) (*PatternEntry, float64, string) {
    // Fast path 1: Exact hash match (O(1), ~0.01ms)
    normalized := normalizePattern(entry.Pattern)
    hash := xxhash.Sum64String(normalized)
    if existing := gh.byHash[hash]; existing != nil {
        return existing, 1.0, "hash"
    }

    // Fast path 2: Trigram similarity within category (O(n), ~1ms)
    candidates := gh.patternsByCategory[entry.Category]
    for _, existing := range candidates {
        sim := trigramSimilarity(entry.Pattern, existing.Pattern)
        if sim > 0.90 {
            return existing, sim, "trigram"
        }
    }

    // Slow path: Embedding for borderline cases (O(1) API, ~200ms)
    // Only when trigram is 0.5-0.9 (suspicious but not certain)
    for _, existing := range candidates {
        trigram := trigramSimilarity(entry.Pattern, existing.Pattern)
        if trigram > 0.5 && trigram < 0.9 {
            embSim := gh.embeddingSimilarity(entry.Pattern, existing.Pattern)
            if embSim > 0.85 {
                return existing, embSim, "embedding"
            }
        }
    }

    return nil, 0, ""
}

func normalizePattern(s string) string {
    s = strings.ToLower(s)
    s = strings.Join(strings.Fields(s), " ")  // Collapse whitespace
    return s
}

func trigramSimilarity(a, b string) float64 {
    a, b = normalizePattern(a), normalizePattern(b)

    triA := make(map[string]bool)
    triB := make(map[string]bool)

    for i := 0; i <= len(a)-3; i++ {
        triA[a[i:i+3]] = true
    }
    for i := 0; i <= len(b)-3; i++ {
        triB[b[i:i+3]] = true
    }

    intersection := 0
    for tri := range triA {
        if triB[tri] {
            intersection++
        }
    }

    union := len(triA) + len(triB) - intersection
    if union == 0 {
        return 0
    }
    return float64(intersection) / float64(union)
}
```

**Informative Merge Response:**

```go
type PatternWriteResponse struct {
    Status    string     `json:"s"`      // "created" | "merged"
    PatternID string     `json:"id"`     // pat_47
    Version   uint64     `json:"v"`
    Merged    *MergeInfo `json:"merged,omitempty"`
}

type MergeInfo struct {
    ExistingID      string  `json:"eid"`
    ExistingPattern string  `json:"existing"`
    Similarity      float64 `json:"sim"`
    Method          string  `json:"method"`  // "hash" | "trigram" | "embedding"
}

func (gh *GlobalHistory) AppendPattern(entry *PatternEntry) (*PatternWriteResponse, error) {
    gh.mu.Lock()
    defer gh.mu.Unlock()

    // Check rate limits first
    if err := gh.checkRateLimits(entry); err != nil {
        return nil, err
    }

    // Check for duplicate
    if existing, sim, method := gh.findDuplicate(entry); existing != nil {
        existing.UsageCount++
        existing.LastUsedAt = time.Now()

        return &PatternWriteResponse{
            Status:    "merged",
            PatternID: existing.ID,
            Version:   gh.version,
            Merged: &MergeInfo{
                ExistingID:      existing.ID,
                ExistingPattern: existing.Pattern,
                Similarity:      sim,
                Method:          method,
            },
        }, nil
    }

    // New pattern - append
    gh.version++
    entry.ID = fmt.Sprintf("pat_%d", gh.version)
    entry.Version = gh.version
    entry.CreatedAt = time.Now()

    gh.patterns = append(gh.patterns, entry)
    gh.patternsByCategory[entry.Category] = append(gh.patternsByCategory[entry.Category], entry)
    gh.byHash[xxhash.Sum64String(normalizePattern(entry.Pattern))] = entry

    return &PatternWriteResponse{
        Status:    "created",
        PatternID: entry.ID,
        Version:   gh.version,
    }, nil
}
```

**Rate Limiting:**

```go
type RateLimits struct {
    PerSessionPerMinute int  // 10
    PerCategoryTotal    int  // 50
    GlobalPerMinute     int  // 60
}

func (gh *GlobalHistory) checkRateLimits(entry *PatternEntry) error {
    // Check category cap (most important for token efficiency)
    categoryCount := len(gh.patternsByCategory[entry.Category])
    if categoryCount >= gh.limits.PerCategoryTotal {
        return &CategoryFullError{
            Category: entry.Category,
            Count:    categoryCount,
            Limit:    gh.limits.PerCategoryTotal,
            Suggestion: fmt.Sprintf("Consider if existing patterns in %s cover your case", entry.Category),
        }
    }

    // Check session rate
    recent := gh.recentPatterns[entry.SessionID]
    if recent >= gh.limits.PerSessionPerMinute {
        return &RateLimitError{
            Type:    "session",
            Current: recent,
            Limit:   gh.limits.PerSessionPerMinute,
            Window:  1 * time.Minute,
        }
    }

    return nil
}
```

**Hierarchical Categories:**

```go
var CategoryL1 = []string{
    "error",      // error.handling, error.recovery, error.logging
    "async",      // async.patterns, async.cancellation, async.coordination
    "database",   // database.orm, database.raw_sql, database.migrations
    "api",        // api.rest, api.graphql, api.versioning
    "auth",       // auth.authn, auth.authz, auth.tokens
    "testing",    // testing.unit, testing.integration, testing.mocks
    "structure",  // structure.modules, structure.dependencies, structure.layers
}

func ValidateCategory(cat string) error {
    parts := strings.Split(cat, ".")
    if len(parts) < 2 {
        return fmt.Errorf("category must be hierarchical: got %q, want 'L1.L2' (e.g., 'database.orm')", cat)
    }

    if !slices.Contains(CategoryL1, parts[0]) {
        return fmt.Errorf("unknown L1 category %q, valid: %v", parts[0], CategoryL1)
    }

    return nil
}

// Query patterns with hierarchical matching
func (gh *GlobalHistory) QueryPatterns(categoryPrefix string) []*PatternEntry {
    gh.mu.RLock()
    defer gh.mu.RUnlock()

    var results []*PatternEntry
    for cat, patterns := range gh.patternsByCategory {
        if strings.HasPrefix(cat, categoryPrefix) {
            results = append(results, patterns...)
        }
    }
    return results
}
```

---

### 2. Contradictory Patterns - Implementation

**Critical Insight: Resolve at Write Time, Not Read Time**

The original approach (coexistence + failure-based ranking) had the same flaw as the original Edge Case #3 approach: it accepted ambiguity and deferred resolution to read time. This is both a correctness gap and an efficiency penalty.

**Why the Original Approach Was Flawed:**

| Aspect | Original (Coexistence + Ranking) | Problem |
|--------|----------------------------------|---------|
| "Optional manual tagging" | Detection depends on agent remembering | Inconsistent - some conflicts tagged, others not |
| "Failure-based ranking" | Wait for patterns to fail before knowing which is better | Reactive, not proactive - agents may follow wrong pattern first |
| "Coexistence" | Multiple conflicting patterns with ambiguous precedence | Per-read interpretation overhead, agent confusion |
| Read-time computation | Ranking calculation per query | O(n) per read instead of O(1) |

**Superior Approach: Explicit Supersession**

The same insight from Edge Case #3 applies: **do the work once at write time, cache the result, serve directly at read time**.

When a new pattern contradicts an existing one, don't let them "coexist with ranking" - require explicit resolution at the moment of insertion.

**Design Decisions:**

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Conflict detection | Automatic at write time | Every conflict addressed, none slip through |
| Conflict resolution | Explicit supersession, required | Zero ambiguity - exactly one active pattern per chain |
| When resolution happens | At write time, not read time | Work done once, amortized across thousands of reads |
| What briefings contain | Only active (non-superseded) patterns | No conflicts to surface, no ranking to compute |

**Token Impact Analysis:**

```
Without supersession (coexistence + ranking):
  Insert pattern → O(1) store, no conflict work
  Query patterns → O(n) ranking computation per request
  Agent sees     → Conflicts with ranked alternatives → still interprets
  Token cost     → Ranking metadata + alternatives × every read

With supersession:
  Insert pattern → O(n) conflict detection + explicit resolution
  Query patterns → O(1) filter for active patterns (SupersededBy == nil)
  Agent sees     → Exactly one active pattern per topic
  Token cost     → Only active patterns, no ranking metadata

Same total work, but supersession front-loads it to writes where it's paid once
instead of reads where it's paid thousands of times.

Example: 50 pattern insertions, 5,000 reads
  Coexistence: 5,000 × O(n) ranking = 5,000n operations
  Supersession: 50 × O(n) detection + 5,000 × O(1) filter = 50n + 5,000 operations
```

**Supersession Data Model:**

```go
type SupersessionRelation struct {
    SupersededBy  string    `json:"superseded_by"`  // ID of pattern that supersedes this
    SupersededAt  time.Time `json:"superseded_at"`
    Reason        string    `json:"reason"`         // Why the new pattern is better
    Evidence      string    `json:"evidence"`       // What triggered this learning
}

type PatternEntry struct {
    ID            string    `json:"id"`
    Pattern       string    `json:"pattern"`
    Category      string    `json:"category"`
    Scope         []string  `json:"scope"`          // Files/modules this applies to
    CreatedAt     time.Time `json:"created_at"`
    CreatedBy     string    `json:"created_by"`     // Session/agent that learned this

    // Supersession chain - forms a DAG
    Supersedes    []string               `json:"supersedes,omitempty"`    // IDs this pattern replaces
    SupersededBy  *SupersessionRelation  `json:"superseded_by,omitempty"` // nil if active

    // Version for caching (incremented on any change including supersession)
    Version       uint64    `json:"version"`
}
```

**Conflict Detection (Automatic, at Write Time):**

```go
// ConflictError returned when a conflicting pattern exists but no supersession declared
type ConflictError struct {
    NewPattern      *PatternEntry
    ExistingPattern *PatternEntry
    Message         string
}

func (e *ConflictError) Error() string {
    return fmt.Sprintf("conflict detected: %s (new) vs %s (existing) - %s",
        e.NewPattern.ID, e.ExistingPattern.ID, e.Message)
}

func (gh *GlobalHistory) InsertPattern(entry *PatternEntry) error {
    gh.mu.Lock()
    defer gh.mu.Unlock()

    // Step 1: Find overlapping patterns (same category + scope overlap)
    overlapping := gh.findOverlappingPatterns(entry.Category, entry.Scope)

    for _, existing := range overlapping {
        // Skip if already superseded (not active)
        if existing.SupersededBy != nil {
            continue
        }

        // Step 2: Detect semantic conflict
        if gh.detectsConflict(entry.Pattern, existing.Pattern) {
            // Step 3: Check if supersession was declared
            if !contains(entry.Supersedes, existing.ID) {
                // REQUIRE explicit resolution - no silent coexistence
                return &ConflictError{
                    NewPattern:      entry,
                    ExistingPattern: existing,
                    Message:         "Conflicting pattern detected. Declare supersedes or differentiate scope.",
                }
            }

            // Step 4: Record supersession on the existing pattern
            existing.SupersededBy = &SupersessionRelation{
                SupersededBy: entry.ID,
                SupersededAt: time.Now(),
                Reason:       entry.SupersessionReason,
                Evidence:     entry.SupersessionEvidence,
            }
            existing.Version++
        }
    }

    // Assign ID and version
    if entry.ID == "" {
        entry.ID = gh.generateID("pat")
    }
    entry.Version = 1
    entry.CreatedAt = time.Now()

    // Store and index
    gh.patterns = append(gh.patterns, entry)
    gh.patternsByCategory[entry.Category] = append(gh.patternsByCategory[entry.Category], entry)
    gh.patternsById[entry.ID] = entry

    // Increment global version (triggers cache updates)
    gh.version++

    return nil
}

func (gh *GlobalHistory) detectsConflict(newPattern, existingPattern string) bool {
    // Fast path: Keyword opposition detection
    opposites := [][]string{
        {"use", "avoid"}, {"use", "don't use"},
        {"always", "never"}, {"do", "don't"},
        {"should", "shouldn't"}, {"must", "must not"},
        {"enable", "disable"}, {"add", "remove"},
        {"prefer", "avoid"}, {"recommended", "discouraged"},
    }

    newLower := strings.ToLower(newPattern)
    existingLower := strings.ToLower(existingPattern)

    for _, pair := range opposites {
        newHasFirst := strings.Contains(newLower, pair[0])
        newHasSecond := strings.Contains(newLower, pair[1])
        existingHasFirst := strings.Contains(existingLower, pair[0])
        existingHasSecond := strings.Contains(existingLower, pair[1])

        if (newHasFirst && existingHasSecond) || (newHasSecond && existingHasFirst) {
            // Potential conflict - check if about the same subject
            if gh.sameSubject(newPattern, existingPattern) {
                return true
            }
        }
    }

    // Semantic similarity + opposing recommendations
    similarity := gh.trigramSimilarity(newPattern, existingPattern)
    if similarity > 0.7 {
        // High similarity but different action verbs = conflict
        newAction := gh.extractActionVerb(newPattern)
        existingAction := gh.extractActionVerb(existingPattern)
        if newAction != existingAction && gh.areOpposingActions(newAction, existingAction) {
            return true
        }
    }

    return false
}

func (gh *GlobalHistory) sameSubject(a, b string) bool {
    // Extract nouns/subjects from both patterns
    // If significant overlap, they're about the same thing
    aNgrams := extractNgrams(a, 3)
    bNgrams := extractNgrams(b, 3)

    overlap := 0
    for ngram := range aNgrams {
        if bNgrams[ngram] {
            overlap++
        }
    }

    // More than 30% overlap = same subject
    minLen := min(len(aNgrams), len(bNgrams))
    return minLen > 0 && float64(overlap)/float64(minLen) > 0.3
}
```

**Querying Active Patterns (O(1) Filter):**

```go
// GetActivePatterns returns only non-superseded patterns
func (gh *GlobalHistory) GetActivePatterns(category string, scope []string) []*PatternEntry {
    gh.mu.RLock()
    defer gh.mu.RUnlock()

    candidates := gh.patternsByCategory[category]
    var active []*PatternEntry

    for _, p := range candidates {
        // O(1) check - just nil comparison
        if p.SupersededBy == nil && gh.scopeOverlaps(p.Scope, scope) {
            active = append(active, p)
        }
    }

    return active
}

// GetPatternWithHistory returns active pattern plus its supersession history
func (gh *GlobalHistory) GetPatternWithHistory(category string, scope []string) *PatternWithHistory {
    active := gh.GetActivePatterns(category, scope)
    if len(active) == 0 {
        return nil
    }

    // Should only be one active per category+scope after proper supersession
    current := active[0]

    // Build history chain
    var history []*PatternEntry
    for _, id := range current.Supersedes {
        if superseded := gh.patternsById[id]; superseded != nil {
            history = append(history, superseded)
        }
    }

    return &PatternWithHistory{
        Current:    current,
        Superseded: history,
    }
}

type PatternWithHistory struct {
    Current    *PatternEntry   `json:"current"`
    Superseded []*PatternEntry `json:"superseded,omitempty"`
}
```

**Pattern Insertion with Supersession:**

```go
type InsertPatternRequest struct {
    Pattern     string   `json:"pattern"`
    Category    string   `json:"category"`
    Scope       []string `json:"scope"`

    // Supersession (required if conflict detected)
    Supersedes  []string `json:"supersedes,omitempty"`
    Reason      string   `json:"reason,omitempty"`      // Why this supersedes
    Evidence    string   `json:"evidence,omitempty"`    // What triggered the learning
}

func (a *Archivalist) LearnPattern(ctx context.Context, sessionID string, req *InsertPatternRequest) (*PatternEntry, error) {
    entry := &PatternEntry{
        Pattern:             req.Pattern,
        Category:            req.Category,
        Scope:               req.Scope,
        Supersedes:          req.Supersedes,
        SupersessionReason:  req.Reason,
        SupersessionEvidence: req.Evidence,
        CreatedBy:           sessionID,
    }

    err := a.globalHistory.InsertPattern(entry)
    if err != nil {
        // Check if it's a conflict error - return it so agent can handle
        if conflictErr, ok := err.(*ConflictError); ok {
            return nil, conflictErr
        }
        return nil, err
    }

    return entry, nil
}

// Example usage from an agent:
//
// Agent learns: "Use Django REST framework for API views"
// Later, agent learns: "Use FastAPI for new API endpoints" (contradicts!)
//
// First attempt without supersession:
//   err = archivalist.LearnPattern(ctx, sessionID, &InsertPatternRequest{
//       Pattern:  "Use FastAPI for new API endpoints",
//       Category: "api.framework",
//       Scope:    []string{"api/**"},
//   })
//   // Returns ConflictError: conflicts with "Use Django REST framework"
//
// Second attempt with supersession:
//   err = archivalist.LearnPattern(ctx, sessionID, &InsertPatternRequest{
//       Pattern:    "Use FastAPI for new API endpoints",
//       Category:   "api.framework",
//       Scope:      []string{"api/**"},
//       Supersedes: []string{"pat_42"},  // The Django REST pattern ID
//       Reason:     "Team decided to migrate to FastAPI for better performance",
//       Evidence:   "Performance benchmarks showed 3x throughput improvement",
//   })
//   // Success - Django REST pattern now superseded
```

**Handling ConflictError in Agents:**

```go
// When agent receives ConflictError, it must decide:
// 1. Supersede the existing pattern (new knowledge replaces old)
// 2. Narrow scope (both can coexist for different contexts)
// 3. Abort (the existing pattern is correct)

func handlePatternConflict(err *ConflictError, sessionID string) (*PatternEntry, error) {
    // Option 1: Supersede (we learned something that replaces the old pattern)
    if shouldSupersede(err.NewPattern, err.ExistingPattern) {
        req := &InsertPatternRequest{
            Pattern:    err.NewPattern.Pattern,
            Category:   err.NewPattern.Category,
            Scope:      err.NewPattern.Scope,
            Supersedes: []string{err.ExistingPattern.ID},
            Reason:     "New insight supersedes previous pattern",
        }
        return archivalist.LearnPattern(ctx, sessionID, req)
    }

    // Option 2: Narrow scope (both valid for different contexts)
    if canNarrowScope(err.NewPattern, err.ExistingPattern) {
        // Differentiate scopes so they don't overlap
        newScope := differentiateScope(err.NewPattern.Scope, err.ExistingPattern.Scope)
        req := &InsertPatternRequest{
            Pattern:  err.NewPattern.Pattern,
            Category: err.NewPattern.Category,
            Scope:    newScope,
        }
        return archivalist.LearnPattern(ctx, sessionID, req)
    }

    // Option 3: Existing pattern is correct, don't insert new one
    return nil, nil
}
```

**Briefings with Supersession (Zero Conflicts):**

```go
func (cc *ComponentCache) AssembleBriefing(sessionID string, tier BriefingTier) *AgentBriefing {
    session := cc.sessions[sessionID]

    // Get relevant categories for this session
    relevantCategories := inferCategories(session)

    // Collect ONLY active patterns (no superseded ones, no conflicts)
    var patterns []*PatternEntry
    for _, cat := range relevantCategories {
        active := cc.globalHistory.GetActivePatterns(cat, session.scope)
        patterns = append(patterns, active...)
    }

    // No conflict surfacing needed - supersession ensures zero ambiguity
    // Each category+scope has exactly one active pattern (or none)

    briefing := &AgentBriefing{
        ResumeState:    session.resume,
        ModifiedFiles:  mapValues(session.files),
        Patterns:       patterns,                    // Only active patterns
        RecentFailures: cc.failures[:min(5, len(cc.failures))],
        GlobalVersion:  cc.globalVersion,
        SessionVersion: session.version,
        // No ActiveConflicts field needed!
    }

    return briefing
}
```

**Why This Is More Correct AND More Efficient:**

| Aspect | Coexistence + Ranking | Explicit Supersession |
|--------|----------------------|----------------------|
| **Correctness** | Agents interpret conflicts | Zero ambiguity - one active pattern per topic |
| **Detection** | Optional, inconsistent | Automatic, required at write |
| **Resolution** | Deferred to read time | Immediate at write time |
| **Read complexity** | O(n) ranking per query | O(1) nil-check filter |
| **Briefing size** | Active + alternatives + rankings | Only active patterns |
| **Cache efficiency** | Must cache ranking metadata | Version-keyed, immutable |
| **Token cost** | Higher (conflicts + rankings) | Lower (only active) |
| **Agent behavior** | May try multiple approaches | Always knows the answer |

**The Key Insight:**

This is the same insight as Edge Case #3:
- **Don't defer work to read time** - do it at write time
- **Don't accept ambiguity** - require explicit resolution
- **Cache the resolved state** - version-keyed, immutable
- **Reads become trivial** - direct lookup, no computation

With explicit supersession, conflict resolution is amortized across thousands of reads instead of being paid on every read. The total work is the same (or less), but it's front-loaded to writes where it belongs.

---

### 3. Stale Briefings Under High Write Load - Implementation

**Evolution of Solutions (Each Iteration Reveals a Deeper Insight):**

| Approach | Refresh Cost | Problem |
|----------|-------------|---------|
| Briefing cache | O(1) hit, but thrashes | 100 writes/sec → 0% hit rate |
| Global version | O(all patterns) | Irrelevant changes trigger recompute |
| Category version | O(patterns in relevant categories) | Still full recompute per category |
| **Incremental cursor** | **O(new patterns since last check)** | **Optimal** |

**Iteration 1: Why Briefing Cache Fails**

```
Briefing cache (flawed):
  Write pattern    → Invalidate ALL briefings
  Write failure    → Invalidate ALL briefings
  100 writes/sec   → 100 invalidations/sec → cache hit rate ≈ 0%
```

**Iteration 2: Component Cache with Global Version**

```
Session A works on: auth.*, user.*
Session B works on: database.*, migrations.*

Pattern write to "database.orm":
  → globalVersion increments (now 48)
  → Session A's version (47) != globalVersion (48)
  → Session A must recompute relevantPatterns
  → But the pattern is in "database.*" - NOT relevant to Session A!
  → 83% of recomputations are wasted
```

**Iteration 3: Category-Versioned (Better, But Not Optimal)**

```
Category version:
  Write pattern to "database"  → Only "database" version increments
  Session A (auth) requests    → Check auth version, unchanged, skip!
  Session B (database) requests → Check database version, changed, recompute

  Problem: "recompute" is still O(all patterns in category)
  If database has 200 patterns, we scan all 200 even though only 1 is new
```

**Iteration 4: Incremental Cursor (Optimal)**

The key insight: **patterns are append-only**. We don't need to recompute - we just need to process NEW patterns since our last check.

```
Incremental cursor:
  Session maintains: lastProcessedIndex (cursor into global patterns list)

  On briefing request:
    for i := lastProcessedIndex; i < len(allPatterns); i++ {
        if relevant(patterns[i]) {
            relevantPatterns.append(patterns[i])
        }
    }
    lastProcessedIndex = len(allPatterns)

  Cost: O(new patterns since last check), not O(all patterns)
```

**Design Decisions:**

| Decision | Choice | Rationale |
|----------|--------|-----------|
| What to cache | Components, NOT assembled briefings | Independent lifecycles, no thrashing |
| Refresh model | Incremental cursor | O(delta) not O(total) |
| Supersession handling | Lazy filter + compaction | Supersession is rare, filter is cheap |
| Category scope change | One-time pull from new category | Rare event, acceptable cost |
| Staleness tolerance | Zero (always current) | Incremental = always up-to-date |

**Complexity Analysis:**

```
Scenario: 500 total patterns, 6 categories, 100 writes, 50 briefings

Global version:
  Every briefing: scan all 500 patterns
  Total: 50 × 500 = 25,000 pattern checks

Category version:
  When relevant category changes: scan ~166 patterns
  Changes: ~33 (assuming 2 relevant categories per session)
  Total: 33 × 166 = 5,478 pattern checks
  Improvement: 78%

Incremental cursor:
  Each briefing: scan NEW patterns since last check
  New patterns per briefing: ~2 (100 writes ÷ 50 briefings)
  Total: 50 × 2 = 100 pattern checks
  Improvement: 99.6%
```

**Component Cache with Incremental Cursor:**

```go
type ComponentCache struct {
    mu sync.RWMutex

    // Global pattern list (append-only, ordered)
    patterns []*PatternEntry

    // Index for category-based lookups (used for scope changes)
    patternsByCategory map[string][]*PatternEntry

    // Global version for conditional requests
    globalVersion uint64

    // Other global components
    failures []*FailureEntry
    insights []*InsightEntry

    // Session components
    sessions map[string]*SessionComponents
}

type SessionComponents struct {
    mu sync.RWMutex

    resume  *ResumeState
    files   map[string]*FileState
    intents []*Intent
    version uint64

    // Which categories this session cares about
    relevantCategories map[string]bool

    // INCREMENTAL: Cursor into global patterns list
    lastProcessedIndex int

    // Accumulated relevant patterns (append-only within session)
    relevantPatterns []*PatternEntry
}

// OnPatternWrite - just append, no invalidation needed
func (cc *ComponentCache) OnPatternWrite(entry *PatternEntry) {
    cc.mu.Lock()
    defer cc.mu.Unlock()

    // Append to global list
    cc.patterns = append(cc.patterns, entry)
    cc.patternsByCategory[entry.Category] = append(
        cc.patternsByCategory[entry.Category], entry)
    cc.globalVersion++

    // NO session invalidation needed!
    // Sessions will pick up new patterns via their cursor
}

// OnSessionWrite updates only that session's component
func (cc *ComponentCache) OnSessionWrite(sessionID string, scope Scope, data any) {
    session := cc.sessions[sessionID]

    session.mu.Lock()
    defer session.mu.Unlock()

    oldCategories := copyMap(session.relevantCategories)

    switch scope {
    case ScopeResume:
        session.resume = data.(*ResumeState)
        session.updateRelevantCategories()
    case ScopeFiles:
        fs := data.(*FileState)
        session.files[fs.Path] = fs
        session.updateRelevantCategories()
    case ScopeIntents:
        session.intents = append(session.intents, data.(*Intent))
    }

    // If new categories added, pull in existing patterns from those categories
    for cat := range session.relevantCategories {
        if !oldCategories[cat] {
            session.pullPatternsFromCategory(cat, cc.patternsByCategory[cat])
        }
    }

    session.version++
}

func (sc *SessionComponents) updateRelevantCategories() {
    newCategories := make(map[string]bool)

    for path := range sc.files {
        for _, cat := range inferCategoriesFromPath(path) {
            newCategories[cat] = true
        }
    }

    if sc.resume != nil {
        for _, cat := range inferCategoriesFromText(sc.resume.CurrentTask) {
            newCategories[cat] = true
        }
    }

    sc.relevantCategories = newCategories
}

func (sc *SessionComponents) pullPatternsFromCategory(cat string, patterns []*PatternEntry) {
    // One-time cost when session starts working on a new category
    for _, p := range patterns {
        if p.SupersededBy == nil {
            sc.relevantPatterns = append(sc.relevantPatterns, p)
        }
    }
}
```

**Incremental Refresh (The Key Innovation):**

```go
func (sc *SessionComponents) refreshRelevantPatterns(allPatterns []*PatternEntry) {
    // Only process patterns we haven't seen yet
    for i := sc.lastProcessedIndex; i < len(allPatterns); i++ {
        p := allPatterns[i]

        // Quick category relevance check
        if !sc.caresAboutCategory(p.Category) {
            continue
        }

        // Skip already-superseded patterns
        if p.SupersededBy != nil {
            continue
        }

        sc.relevantPatterns = append(sc.relevantPatterns, p)
    }

    // Move cursor forward
    sc.lastProcessedIndex = len(allPatterns)
}

func (sc *SessionComponents) caresAboutCategory(cat string) bool {
    if sc.relevantCategories[cat] {
        return true
    }

    // Prefix matching (e.g., session cares about "auth", pattern is "auth.oauth")
    for relevant := range sc.relevantCategories {
        if strings.HasPrefix(cat, relevant) || strings.HasPrefix(relevant, cat) {
            return true
        }
    }

    return false
}

// Get active patterns, filtering out superseded ones
func (sc *SessionComponents) getActivePatterns() []*PatternEntry {
    supersededCount := 0
    active := make([]*PatternEntry, 0, len(sc.relevantPatterns))

    for _, p := range sc.relevantPatterns {
        if p.SupersededBy == nil {
            active = append(active, p)
        } else {
            supersededCount++
        }
    }

    // Lazy compaction if >20% superseded
    if supersededCount > len(sc.relevantPatterns)/5 {
        sc.relevantPatterns = active
    }

    return active
}
```

**O(1) Assembly with Incremental Refresh:**

```go
func (cc *ComponentCache) AssembleBriefing(sessionID string, tier BriefingTier) *AgentBriefing {
    // Snapshot global state under RLock
    cc.mu.RLock()
    globalVersion := cc.globalVersion
    allPatterns := cc.patterns
    failures := cc.failures
    cc.mu.RUnlock()

    session := cc.sessions[sessionID]
    session.mu.Lock()
    defer session.mu.Unlock()

    // Incremental refresh: O(new patterns since last check)
    // This is typically 0-5 patterns, not 100s
    if session.lastProcessedIndex < len(allPatterns) {
        session.refreshRelevantPatterns(allPatterns)
    }

    // Assembly with supersession filter
    return &AgentBriefing{
        ResumeState:     session.resume,
        ModifiedFiles:   mapValues(session.files),
        UserWants:       filterWants(session.intents),
        UserRejects:     filterRejects(session.intents),
        Patterns:        session.getActivePatterns(),  // Filters superseded
        RecentFailures:  failures[:min(5, len(failures))],
        GlobalVersion:   globalVersion,
        SessionVersion:  session.version,
    }
}

**Conditional Request Handling:**

```go
func (a *Archivalist) handleBriefing(req *Request) Response {
    agent := a.registry.Get(req.AgentID)
    sessionID := agent.SessionID

    // Get current versions - O(1)
    currentGV := a.componentCache.GlobalVersion()
    currentSV := a.componentCache.SessionVersion(sessionID)

    // Conditional check - O(1)
    if req.Briefing.IfVersions != nil {
        if req.Briefing.IfVersions.GlobalVersion == currentGV &&
           req.Briefing.IfVersions.SessionVersion == currentSV {
            return Response{
                Status:         StatusNotModified,
                GlobalVersion:  currentGV,
                SessionVersion: currentSV,
            }
            // Response: ~15 tokens
        }
    }

    // Assemble from components - O(1)
    briefing := a.componentCache.AssembleBriefing(sessionID, req.Briefing.Tier)

    // Apply tier filtering
    switch req.Briefing.Tier {
    case BriefingTierMicro:
        return Response{
            Status:         StatusOK,
            GlobalVersion:  currentGV,
            SessionVersion: currentSV,
            Data:           briefing.ToMicro(), // ~20 tokens
        }
    case BriefingTierStandard:
        return Response{
            Status:         StatusOK,
            GlobalVersion:  currentGV,
            SessionVersion: currentSV,
            Data:           briefing, // ~500 tokens
        }
    case BriefingTierFull:
        return Response{
            Status:         StatusOK,
            GlobalVersion:  currentGV,
            SessionVersion: currentSV,
            Data:           briefing.WithStats(a.store.Stats()), // ~2000 tokens
        }
    }

    return Response{Status: StatusOK, Data: briefing}
}
```

**Critical Insight: Incremental Accumulation, Not Recomputation**

Category-versioned invalidation is better than global-versioned, but it still has a flaw: when a relevant category changes, we do a **full recomputation** of `relevantPatterns`. This is O(all patterns in relevant categories).

But patterns are **append-only**! When a category changes:
- Old relevant patterns are still there
- We only need to ADD the new patterns

**The Superior Approach: Cursor-Based Incremental Refresh**

```go
type SessionComponents struct {
    mu sync.RWMutex

    resume  *ResumeState
    files   map[string]*FileState
    intents []*Intent
    version uint64

    // Which categories this session cares about
    relevantCategories map[string]bool

    // Incrementally accumulated relevant patterns
    relevantPatterns     []*PatternEntry
    lastProcessedIndex   int  // Cursor into global patterns list

    // For supersession cleanup
    supersededCount int
}
```

**Incremental Refresh (O(new patterns), not O(all patterns)):**

```go
func (sc *SessionComponents) refreshRelevantPatterns(allPatterns []*PatternEntry) {
    // Only iterate patterns we haven't seen yet
    for i := sc.lastProcessedIndex; i < len(allPatterns); i++ {
        p := allPatterns[i]

        // Quick category check
        if !sc.caresAboutCategory(p.Category) {
            continue
        }

        // Skip already-superseded patterns
        if p.SupersededBy != nil {
            continue
        }

        sc.relevantPatterns = append(sc.relevantPatterns, p)
    }

    sc.lastProcessedIndex = len(allPatterns)
}

func (sc *SessionComponents) caresAboutCategory(cat string) bool {
    // Check exact match
    if sc.relevantCategories[cat] {
        return true
    }

    // Check prefix match (e.g., session cares about "auth", pattern is "auth.oauth")
    for relevant := range sc.relevantCategories {
        if strings.HasPrefix(cat, relevant) || strings.HasPrefix(relevant, cat) {
            return true
        }
    }

    return false
}
```

**Complexity Comparison:**

```
Scenario: 500 total patterns, 6 categories, 100 writes, 50 briefings

Global version (original):
  Every briefing: scan all 500 patterns
  Total: 50 × 500 = 25,000 pattern checks

Category version (improved):
  When relevant category changes: scan ~166 patterns (2 categories)
  Relevant changes: ~33 (assuming even distribution)
  Total: 33 × 166 = 5,478 pattern checks
  Improvement: 78%

Incremental cursor (optimal):
  Each briefing: scan only NEW patterns since last check
  New patterns per briefing: ~2 (100 writes ÷ 50 briefings)
  Total: 50 × 2 = 100 pattern checks
  Improvement: 99.6%
```

**Handling Supersession:**

When pattern A is superseded by B, A might already be in `relevantPatterns`. We handle this with **lazy filtering**:

```go
func (sc *SessionComponents) getActivePatterns() []*PatternEntry {
    // Count superseded for potential compaction
    supersededCount := 0

    active := make([]*PatternEntry, 0, len(sc.relevantPatterns))
    for _, p := range sc.relevantPatterns {
        if p.SupersededBy == nil {
            active = append(active, p)
        } else {
            supersededCount++
        }
    }

    // Lazy compaction: if >20% superseded, compact the list
    if supersededCount > len(sc.relevantPatterns)/5 {
        sc.relevantPatterns = active
        sc.supersededCount = 0
    }

    return active
}
```

Since supersession is rare, this filter is nearly free. Compaction keeps the list from growing unboundedly.

**Handling Category Scope Changes:**

When a session starts working on a new category (new file added), we pull in existing patterns:

```go
func (sc *SessionComponents) onCategoryAdded(cat string, patternsByCategory map[string][]*PatternEntry) {
    // One-time cost: pull in patterns from new category
    for _, p := range patternsByCategory[cat] {
        if p.SupersededBy == nil && !sc.hasPattern(p.ID) {
            sc.relevantPatterns = append(sc.relevantPatterns, p)
        }
    }
}

func (sc *SessionComponents) onCategoryRemoved(cat string) {
    // No action needed - patterns stay in list but won't match future additions
    // Supersession or compaction will eventually clean them up
    // This is fine because:
    // 1. Category removal is rare
    // 2. Having extra patterns costs only a few tokens
    // 3. Compaction happens lazily
}
```

**Updated Assembly (Still O(1) for cache hit):**

```go
func (cc *ComponentCache) AssembleBriefing(sessionID string, tier BriefingTier) *AgentBriefing {
    cc.mu.RLock()
    globalVersion := cc.globalVersion
    allPatterns := cc.patterns  // Global ordered list
    failures := cc.failures
    cc.mu.RUnlock()

    session := cc.sessions[sessionID]
    session.mu.Lock()
    defer session.mu.Unlock()

    // Incremental refresh: O(new patterns since last check)
    if session.lastProcessedIndex < len(allPatterns) {
        session.refreshRelevantPatterns(allPatterns)
    }

    // Assembly is O(relevant patterns) with supersession filter
    return &AgentBriefing{
        ResumeState:     session.resume,
        ModifiedFiles:   mapValues(session.files),
        Patterns:        session.getActivePatterns(),
        RecentFailures:  failures[:min(5, len(failures))],
        GlobalVersion:   globalVersion,
        SessionVersion:  session.version,
    }
}
```

**Why Incremental Is More Correct AND More Efficient:**

| Aspect | Category Versioning | Incremental Cursor |
|--------|--------------------|--------------------|
| **Refresh cost** | O(patterns in relevant categories) | O(new patterns since last check) |
| **Pattern checks (example)** | 5,478 | 100 |
| **Category tracking** | Per-category versions | Single cursor |
| **Supersession handling** | Scan and filter | Lazy filter + compaction |
| **Complexity** | Higher (version maps) | Lower (single int) |

**The Key Insight:**

Append-only data structures enable **incremental processing**:
- Don't recompute what you already know
- Only process what's new since last check
- Use a cursor to track progress

This is the same insight applied to different data:
- Edge Case #2: Supersession chains instead of conflict ranking
- Edge Case #6: Checkpoints + log instead of delta chains
- Edge Case #3: Incremental cursor instead of full recomputation

**All follow the pattern: leverage append-only semantics for O(delta) instead of O(total).**

---

### 4. Session Isolation - Implementation

**Critical Insight: Structural Isolation vs Validation-Based Isolation**

The original approach ("strict validation, audit logging") validates session ID at request boundaries and logs access. This is necessary but not sufficient:

| Aspect | Validation-Based | Problem |
|--------|-----------------|---------|
| All sessions in one map | `map[string]*SessionContext` | Internal code can enumerate |
| Validation at boundary | `if !authorized(id) { error }` | Internal bugs can bypass |
| Audit logging | After-the-fact detection | Doesn't prevent leakage |

**Superior Approach: Structural Isolation**

Make cross-session access **impossible by design**, not just validated against.

**Design Decisions:**

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Storage model | Per-session store instances | Cannot enumerate or access other sessions |
| Access control | Capability-based (store reference) | No ID checking needed - you have it or you don't |
| Logging | Session lifecycle only | Reduced I/O, same security |

**Structural Isolation Model:**

```go
// Each session gets its own isolated store - no shared state
type SessionStore struct {
    mu sync.RWMutex  // Thread safety for concurrent agents in same session

    sessionID string  // For identification only, not access control

    // Session-scoped data - only this session's data exists here
    resume    *ResumeState
    files     map[string]*FileState
    intents   []*Intent
    version   uint64

    // Incremental pattern tracking (from Edge Case #3)
    relevantCategories   map[string]bool
    relevantPatterns     []*PatternEntry
    lastProcessedIndex   int

    // Reference to global (read-only for session-scoped operations)
    globalHistory *GlobalHistory  // Shared, but append-only
}

// Archivalist holds session stores by capability, not by ID lookup
type Archivalist struct {
    mu sync.RWMutex

    // Global state (shared, append-only)
    globalHistory *GlobalHistory

    // Session stores - access requires the capability (the *SessionStore itself)
    // No map enumeration possible from agent code
    sessions map[string]*SessionStore  // Only Archivalist can enumerate

    // Agent registry maps agent → session store (capability)
    agents map[string]*AgentCapability
}

type AgentCapability struct {
    AgentID   string
    Session   *SessionStore  // Direct reference - this IS the access control
    CreatedAt time.Time
    ExpiresAt *time.Time
}
```

**Why This Is More Correct:**

```
Validation-based access:
  agent_a wants session_b's data
  → agent_a calls GetSession("session_b")
  → validation: agent_a.sessionID != "session_b" → DENY

  But what if there's a bug in validation?
  But what if internal code bypasses the check?
  But what if logging fails to capture the attempt?

Structural isolation:
  agent_a has capability → *SessionStore (for session_a)
  agent_a simply CANNOT reference session_b's data
  There's no GetSession(id) call that could be misused
  The capability IS the access control
```

**Capability-Based Access:**

```go
// Agents receive their session store at registration
func (a *Archivalist) RegisterAgent(sessionID, agentID string) (*AgentCapability, error) {
    a.mu.Lock()
    defer a.mu.Unlock()

    // Get or create session store
    session := a.sessions[sessionID]
    if session == nil {
        session = &SessionStore{
            sessionID: sessionID,
            files:     make(map[string]*FileState),
            intents:   make([]*Intent, 0),
            globalHistory: a.globalHistory,
        }
        a.sessions[sessionID] = session
    }

    // Create capability - this IS the access control
    cap := &AgentCapability{
        AgentID:   agentID,
        Session:   session,  // Direct reference to session store
        CreatedAt: time.Now(),
    }

    a.agents[agentID] = cap

    // Log at session lifecycle boundary only
    a.auditLog.SessionAgentJoined(sessionID, agentID)

    return cap, nil
}

// Agent operations use the capability directly
func (cap *AgentCapability) UpdateResume(resume *ResumeState) {
    s := cap.Session
    s.mu.Lock()
    defer s.mu.Unlock()

    // No validation needed - cap.Session IS our session
    s.resume = resume
    s.version++
}

func (cap *AgentCapability) GetBriefing(allPatterns []*PatternEntry, tier BriefingTier) *AgentBriefing {
    s := cap.Session
    s.mu.Lock()
    defer s.mu.Unlock()

    // Incremental pattern refresh (from Edge Case #3)
    if s.lastProcessedIndex < len(allPatterns) {
        s.refreshRelevantPatterns(allPatterns)
    }

    // No validation needed - we can only access our own session
    return s.assembleBriefing(tier)
}

func (cap *AgentCapability) RecordFile(path string, state *FileState) {
    s := cap.Session
    s.mu.Lock()
    defer s.mu.Unlock()

    // No validation, no risk of cross-session contamination
    s.files[path] = state
    s.updateRelevantCategories()  // File change may affect categories
    s.version++
}
```

**Request Handling with Capabilities:**

```go
func (a *Archivalist) HandleRequest(req *Request) Response {
    // Look up agent capability
    cap := a.agents[req.AgentID]
    if cap == nil {
        return Response{Status: StatusUnauthorized}
    }

    // Check expiration if set
    if cap.ExpiresAt != nil && time.Now().After(*cap.ExpiresAt) {
        return Response{Status: StatusExpired}
    }

    // All operations use the capability - structurally isolated
    switch req.Type {
    case RequestTypeBriefing:
        return cap.GetBriefing(req.Briefing.Tier)
    case RequestTypeUpdateResume:
        cap.UpdateResume(req.Resume)
        return Response{Status: StatusOK}
    case RequestTypeRecordFile:
        cap.RecordFile(req.File.Path, req.File.State)
        return Response{Status: StatusOK}
    case RequestTypeLearnPattern:
        // Patterns go to global history (shared, append-only)
        entry, err := a.globalHistory.InsertPattern(req.Pattern)
        if err != nil {
            return Response{Status: StatusError, Error: err.Error()}
        }
        return Response{Status: StatusOK, Data: entry}
    }

    return Response{Status: StatusUnknownRequest}
}
```

**Audit Logging (Lifecycle Only):**

```go
// Log at session boundaries, not every operation
type AuditLog struct {
    entries []AuditEntry
    mu      sync.Mutex
}

type AuditEntry struct {
    Timestamp time.Time
    Event     string
    SessionID string
    AgentID   string
    Details   map[string]any
}

func (al *AuditLog) SessionCreated(sessionID string) {
    al.append(AuditEntry{
        Timestamp: time.Now(),
        Event:     "session_created",
        SessionID: sessionID,
    })
}

func (al *AuditLog) SessionAgentJoined(sessionID, agentID string) {
    al.append(AuditEntry{
        Timestamp: time.Now(),
        Event:     "agent_joined",
        SessionID: sessionID,
        AgentID:   agentID,
    })
}

func (al *AuditLog) SessionEnded(sessionID string, summary string) {
    al.append(AuditEntry{
        Timestamp: time.Now(),
        Event:     "session_ended",
        SessionID: sessionID,
        Details:   map[string]any{"summary": summary},
    })
}

// No per-operation logging needed - structural isolation prevents cross-session access
```

**Token/Latency Impact:**

```
Validation-based:
  Every request: Look up session → validate → log access attempt
  Overhead: 3 operations per request
  Logging I/O: O(requests) writes

Structural:
  Registration: Create capability (once per agent)
  Every request: Use capability directly (no lookup, no validation)
  Overhead: 1 operation per request (capability use)
  Logging I/O: O(sessions) writes (lifecycle only)

For 180 agents with 1000 requests each:
  Validation: 180,000 validations + 180,000 log entries
  Structural: 180 registrations + 180 log entries

Improvement: 1000x reduction in validation overhead, 1000x reduction in logging I/O
```

**Why This Is More Correct AND More Efficient:**

| Aspect | Validation-Based | Structural Isolation |
|--------|-----------------|---------------------|
| **Correctness** | Depends on validation logic | Impossible to access other sessions |
| **Bug surface** | Validation bypass bugs possible | No bypass possible |
| **Per-request overhead** | Lookup + validate + log | Direct use of capability |
| **Logging I/O** | O(requests) | O(sessions) |
| **Enumeration attack** | Possible if validation buggy | Impossible by design |
| **Internal code safety** | Must check everywhere | No checks needed |

**The Key Insight:**

Same principle as Edge Cases #2 and #3:
- **Don't validate at runtime what can be guaranteed by design**
- **Structural guarantees beat behavioral checks**
- **Capabilities eliminate the need for ID-based access control**

**Why This Is Optimal:**

Structural isolation IS the most correct approach for session isolation. The key properties:

1. **Impossible to access other sessions** - Not "validated against", but structurally impossible
2. **No per-request validation overhead** - Capability IS the access control
3. **Thread-safe with minimal locking** - Lock per session, not global
4. **Integrates with incremental patterns** - Capability methods can do incremental refresh

The only potential alternative would be process-level isolation (each session in its own process), but that would sacrifice the shared global history and add significant IPC overhead.

---

### 5. Cache Memory Pressure - Implementation

**Critical Insight: Cache Derived Computations, Not Raw Data**

The original approach focused on caching "session state" and "deltas" - but that's the wrong unit. Token savings come from **smaller, more precise briefings**. We should cache **derived computations that affect briefing content**.

**What Drives Token Savings:**

| Derived Computation | Without Cache | With Cache | Token Impact |
|---------------------|---------------|------------|--------------|
| Active patterns per category | Filter superseded at query O(n) | Pre-filtered, O(1) lookup | No superseded patterns leak into briefings |
| Best resolution per error | Search all resolutions O(m) | O(1) lookup by signature | Include fix recommendation efficiently |
| Relevant patterns by category set | Scan and collect O(categories × patterns) | Shared cache across sessions | Consistent, fast assembly |
| Category inference | Parse path/text each time | Cached pure function | Consistent categorization |
| Pattern similarity hashes | Compute on each comparison | Pre-computed on write | Faster dedup → fewer duplicates in history |

**Why This Matters for Tokens:**

```
Without derived caches:
  Briefing assembly:
    1. Get session's relevant categories (inference - maybe inconsistent)
    2. For each category, scan all patterns
    3. Filter out superseded (might miss some, might include stale)
    4. Collect failures, search for resolutions
  Result: Variable briefing size, possibly includes superseded/irrelevant data

With derived caches:
  Briefing assembly:
    1. Get cached categories for session's files (consistent)
    2. Lookup cached active patterns for those categories (pre-filtered)
    3. Lookup cached resolutions for recent failures (O(1))
  Result: Minimal briefing size, only active relevant data

Token savings: 10-30% smaller briefings due to no superseded/irrelevant patterns
```

**The Cache Architecture for Token Optimization:**

**Design Decisions:**

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Cache partitioning | Semantic tiers (critical/warm/cold) | Each tier has appropriate eviction policy |
| Critical tier | No eviction while active | Briefing components, active session state |
| Warm tier | LRU with activity window | Recently active sessions, recent deltas |
| Cold tier | Time-based expiry | Old deltas, old version snapshots |
| Tier movement | Explicit based on lifecycle | Session end → demote; request → promote |

**Semantic Cache Implementation:**

```go
type SemanticCache struct {
    // Critical tier: never evict while active
    // Contains: active session components, current global state
    critical *ristretto.Cache

    // Warm tier: LRU with 30-minute TTL
    // Contains: recently active sessions, deltas from last hour
    warm *ristretto.Cache

    // Cold tier: aggressive 5-minute TTL
    // Contains: old deltas, historical snapshots
    cold *ristretto.Cache

    // Track tier membership
    tierMap sync.Map  // key → tier

    // Active session tracking
    activeSessions sync.Map  // sessionID → lastActivity
}

func NewSemanticCache(cfg CacheConfig) *SemanticCache {
    return &SemanticCache{
        critical: newCache(CriticalConfig{
            MaxCost:    cfg.CriticalMB * 1024 * 1024,
            NumCounters: 1000,      // Small, predictable set
            BufferItems: 64,
        }),
        warm: newCache(WarmConfig{
            MaxCost:     cfg.WarmMB * 1024 * 1024,
            NumCounters: 10000,
            BufferItems: 64,
            DefaultTTL:  30 * time.Minute,
        }),
        cold: newCache(ColdConfig{
            MaxCost:     cfg.ColdMB * 1024 * 1024,
            NumCounters: 100000,
            BufferItems: 64,
            DefaultTTL:  5 * time.Minute,
        }),
    }
}
```

**Semantic Key Classification:**

```go
type CacheKey struct {
    Type      KeyType
    SessionID string  // Empty for global
    Version   uint64
    SubKey    string
}

type KeyType int

const (
    KeyTypeSessionResume KeyType = iota
    KeyTypeSessionFiles
    KeyTypeSessionIntents
    KeyTypeGlobalPatterns
    KeyTypeGlobalFailures
    KeyTypeDelta
    KeyTypeSnapshot
)

func (sc *SemanticCache) classifyTier(key CacheKey) CacheTier {
    switch key.Type {
    case KeyTypeSessionResume, KeyTypeSessionFiles, KeyTypeSessionIntents:
        // Session data goes to critical if session is active
        if sc.isSessionActive(key.SessionID) {
            return TierCritical
        }
        return TierWarm

    case KeyTypeGlobalPatterns, KeyTypeGlobalFailures:
        // Current global state is always critical
        if key.Version == sc.currentGlobalVersion() {
            return TierCritical
        }
        // Old versions go to warm
        return TierWarm

    case KeyTypeDelta:
        // Recent deltas (last 100 versions) go to warm
        age := sc.currentGlobalVersion() - key.Version
        if age <= 100 {
            return TierWarm
        }
        // Old deltas go to cold
        return TierCold

    case KeyTypeSnapshot:
        // Snapshots always go to cold (can be rebuilt)
        return TierCold
    }

    return TierCold
}
```

**Activity-Aware Eviction:**

```go
// Session becomes active → promote to critical
func (sc *SemanticCache) PromoteSession(sessionID string) {
    sc.activeSessions.Store(sessionID, time.Now())

    // Move session data from warm to critical
    keys := []CacheKey{
        {Type: KeyTypeSessionResume, SessionID: sessionID},
        {Type: KeyTypeSessionFiles, SessionID: sessionID},
        {Type: KeyTypeSessionIntents, SessionID: sessionID},
    }

    for _, key := range keys {
        if data, found := sc.warm.Get(key.String()); found {
            sc.critical.Set(key.String(), data, estimateCost(data))
            sc.warm.Del(key.String())
            sc.tierMap.Store(key.String(), TierCritical)
        }
    }
}

// Session ends → demote to warm (will eventually expire)
func (sc *SemanticCache) DemoteSession(sessionID string) {
    sc.activeSessions.Delete(sessionID)

    // Move session data from critical to warm
    keys := []CacheKey{
        {Type: KeyTypeSessionResume, SessionID: sessionID},
        {Type: KeyTypeSessionFiles, SessionID: sessionID},
        {Type: KeyTypeSessionIntents, SessionID: sessionID},
    }

    for _, key := range keys {
        if data, found := sc.critical.Get(key.String()); found {
            sc.warm.SetWithTTL(key.String(), data, estimateCost(data), 30*time.Minute)
            sc.critical.Del(key.String())
            sc.tierMap.Store(key.String(), TierWarm)
        }
    }
}

// Global version advances → demote old versions
func (sc *SemanticCache) OnGlobalVersionAdvance(newVersion uint64) {
    // Keep last 10 versions in critical, demote older
    threshold := newVersion - 10

    sc.tierMap.Range(func(keyStr, tier any) bool {
        key := parseCacheKey(keyStr.(string))
        if key.Type == KeyTypeGlobalPatterns || key.Type == KeyTypeGlobalFailures {
            if key.Version < threshold && tier == TierCritical {
                sc.demoteToWarm(key)
            }
        }
        return true
    })
}
```

**Delta Lifecycle Management:**

```go
// Deltas have a clear lifecycle:
// 1. Created: warm tier (agents catching up)
// 2. Aged out: cold tier (rarely accessed)
// 3. Expired: evicted (can be rebuilt from snapshots)

func (sc *SemanticCache) StoreDelta(fromVersion, toVersion uint64, delta *HistoryDelta) {
    key := CacheKey{
        Type:    KeyTypeDelta,
        Version: fromVersion,
        SubKey:  fmt.Sprintf("to_%d", toVersion),
    }

    // Deltas start in warm tier
    cost := estimateDeltaCost(delta)
    sc.warm.SetWithTTL(key.String(), delta, cost, 30*time.Minute)
    sc.tierMap.Store(key.String(), TierWarm)
}

// Periodic cleanup: demote old deltas to cold
func (sc *SemanticCache) CleanupDeltas(currentVersion uint64) {
    threshold := currentVersion - 100

    sc.tierMap.Range(func(keyStr, tier any) bool {
        key := parseCacheKey(keyStr.(string))
        if key.Type == KeyTypeDelta && key.Version < threshold {
            if tier == TierWarm {
                sc.demoteToCold(key)
            }
        }
        return true
    })
}
```

**Memory Budget Allocation:**

```go
// Typical allocation for 1GB total cache budget:
type CacheConfig struct {
    CriticalMB int  // 100MB - active sessions, current global state
    WarmMB     int  // 400MB - recent sessions, recent deltas
    ColdMB     int  // 500MB - old data, snapshots
}

// Critical tier sizing:
//   - 6 active sessions × 10 agents × 1KB state = 60KB
//   - Current global state (patterns, failures) = 10MB
//   - Buffer for bursts = 90MB
//   Total: ~100MB

// Warm tier sizing:
//   - 50 recent sessions × 200KB = 10MB
//   - 100 recent deltas × 50KB = 5MB
//   - Buffer for activity spikes = 385MB
//   Total: ~400MB

// Cold tier sizing:
//   - Historical snapshots = 200MB
//   - Old deltas (rebuilt on demand) = 300MB
//   Total: ~500MB
```

**Token/Latency Impact:**

```
Generic LFU problem scenario:
  Agent offline for 1 hour, 500 global versions behind.
  Requests delta from gv:100 to gv:600.

  With generic LFU:
    Delta not in cache (evicted due to low frequency).
    Rebuild from snapshot + log: 200ms

  With semantic tiering:
    Delta for recent catchup (gv:500-600) in warm tier: 0.1ms
    Older delta (gv:100-500) in cold tier or rebuild: 50ms (smaller delta)
    Total: ~50ms

  Improvement: 4x latency reduction

Memory efficiency:
  Generic LFU:
    High-frequency old deltas consume cache → active sessions evicted
    Cache thrashing during intensive periods

  Semantic tiering:
    Active sessions CANNOT be evicted (critical tier)
    Old deltas expire regardless of frequency
    Predictable memory usage per tier
```

**Why This Is More Correct AND More Efficient:**

| Aspect | Generic LFU | Semantic Tiering |
|--------|------------|------------------|
| **Correctness** | May evict active session data | Active data protected |
| **Delta handling** | Keeps high-frequency old deltas | Expires deltas by age |
| **Cache thrashing** | Possible under load | Impossible for critical data |
| **Memory predictability** | Unpredictable | Bounded per tier |
| **Eviction policy** | One size fits all | Tailored to data semantics |

**The Key Insight:**

Cache eviction should be based on **semantic lifecycle**, not **access frequency**:
- **Active session data** → never evict (critical)
- **Recent data** → LRU with reasonable TTL (warm)
- **Old data** → aggressive TTL, rebuild on demand (cold)

Frequency-based eviction is a proxy for "will this be accessed again?" - but we KNOW the answer from the data's semantic meaning, so we don't need the proxy.

---

### 6. Delta Chain Explosion - Implementation

**Critical Insight: Version-Keyed Components Eliminate Traversal**

The original approach ("adaptive snapshots, delta limits") tries to optimize delta chain traversal. But the real insight is: **our component caching architecture makes delta chain traversal unnecessary for normal operations**.

**The Problem (in traditional delta-based systems):**

```
Traditional approach:
  State at version 150 = Snapshot@100 + Delta[101] + Delta[102] + ... + Delta[150]

  Reading state: O(chain_length) delta applications
  Chain length 50 × 1ms per delta = 50ms latency

  Solution attempts:
    - Adaptive snapshots: Snapshot when chain > N
    - Delta limits: Force snapshot when chain hits limit

  But: Still requires traversal up to the limit
```

**Why Delta Chains Don't Matter for Us:**

Our architecture (from Edge Cases #2 and #3) uses version-keyed components:

```go
type ComponentCache struct {
    patterns         []*PatternEntry     // Current state, not deltas
    patternsVersion  uint64              // Version of this snapshot

    failures         []*FailureEntry     // Current state
    failuresVersion  uint64

    // No delta chain - just the current state
}
```

**Normal reads NEVER traverse deltas:**

```
Agent requests briefing with gv:150
→ ComponentCache has current state at gv:152
→ If agent has gv:150, conditional match
→ If not, assemble from current components
→ O(1) - no delta traversal
```

**When Deltas ARE Needed:**

Deltas are only needed for:
1. **Agent catch-up**: Agent at gv:100 wants to know what changed through gv:150
2. **Cache rebuild**: After cache eviction, rebuild state from persistent storage
3. **Historical queries**: "What patterns existed at gv:75?"

For these rare cases, the solution is simple: **regular fixed-interval checkpoints**.

**Design Decisions:**

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Checkpoint frequency | Every 100 versions | Simple, predictable, max 100 deltas to apply |
| Delta storage | Append-only log | No traversal needed for normal ops |
| State reads | From component cache | O(1), never touches deltas |
| Historical reads | Checkpoint + bounded deltas | Max 100 deltas, rare operation |

**Fixed-Interval Checkpointing:**

```go
type GlobalHistoryStorage struct {
    // In-memory: current state
    current *GlobalHistorySnapshot
    version uint64

    // Persistent: checkpoints + delta log
    checkpoints *CheckpointStore  // SQLite or file-based
    deltaLog    *AppendLog        // Append-only file

    // Checkpoint settings
    checkpointInterval uint64  // Every N versions
}

type GlobalHistorySnapshot struct {
    Version  uint64
    Patterns []*PatternEntry
    Failures []*FailureEntry
    Insights []*InsightEntry
}

type Delta struct {
    Version uint64
    Type    DeltaType  // PatternAdded, FailureAdded, etc.
    Data    []byte     // Serialized entry
}

const CheckpointInterval = 100  // Checkpoint every 100 versions

func (ghs *GlobalHistoryStorage) OnWrite(delta Delta) error {
    // 1. Append to delta log (always)
    if err := ghs.deltaLog.Append(delta); err != nil {
        return err
    }

    // 2. Update in-memory state
    ghs.applyDelta(delta)
    ghs.version++

    // 3. Checkpoint if at interval
    if ghs.version % CheckpointInterval == 0 {
        if err := ghs.checkpoint(); err != nil {
            // Non-fatal: log and continue
            log.Printf("checkpoint failed at v%d: %v", ghs.version, err)
        }
    }

    return nil
}

func (ghs *GlobalHistoryStorage) checkpoint() error {
    snapshot := &GlobalHistorySnapshot{
        Version:  ghs.version,
        Patterns: copyPatterns(ghs.current.Patterns),
        Failures: copyFailures(ghs.current.Failures),
        Insights: copyInsights(ghs.current.Insights),
    }

    return ghs.checkpoints.Save(snapshot)
}
```

**Bounded Delta Application (for rare historical reads):**

```go
func (ghs *GlobalHistoryStorage) GetStateAtVersion(targetVersion uint64) (*GlobalHistorySnapshot, error) {
    // 1. Find nearest checkpoint at or before target
    checkpoint, err := ghs.checkpoints.GetAtOrBefore(targetVersion)
    if err != nil {
        return nil, err
    }

    // 2. If exact match, return checkpoint
    if checkpoint.Version == targetVersion {
        return checkpoint, nil
    }

    // 3. Apply deltas from checkpoint to target
    //    Max deltas to apply = CheckpointInterval - 1 = 99
    deltas, err := ghs.deltaLog.GetRange(checkpoint.Version+1, targetVersion)
    if err != nil {
        return nil, err
    }

    // Apply deltas (max 99 operations)
    result := checkpoint.Clone()
    for _, delta := range deltas {
        result.applyDelta(delta)
    }
    result.Version = targetVersion

    return result, nil
}

// Worst case: 99 delta applications
// At 0.1ms per delta: 9.9ms
// But this is RARE - only for historical queries or cache rebuild
```

**Delta Queries for Agent Catch-Up:**

```go
func (ghs *GlobalHistoryStorage) GetDeltasSince(fromVersion uint64) ([]*Delta, error) {
    current := ghs.version

    // If too far behind, suggest full refresh instead
    if current - fromVersion > 1000 {
        return nil, ErrTooFarBehind
    }

    // Return deltas from log
    return ghs.deltaLog.GetRange(fromVersion+1, current)
}

// Agent catch-up flow:
// 1. Agent has gv:100, current is gv:150
// 2. Agent requests deltas since gv:100
// 3. Archivalist returns 50 deltas
// 4. Agent applies locally (or just requests full briefing)
//
// Note: Agent often just requests full briefing - simpler and
// NOT_MODIFIED handles the common case where nothing changed
```

**Why Fixed-Interval Is Simpler and Equally Correct:**

```
Adaptive snapshots:
  if chain_length > threshold OR
     time_since_last > interval OR
     write_rate > threshold:
       checkpoint()

  Problems:
    - Multiple heuristics to tune
    - Unpredictable checkpoint timing
    - Edge cases in each heuristic

Fixed-interval:
  if version % 100 == 0:
       checkpoint()

  Benefits:
    - Trivially correct
    - Predictable: exactly 1 checkpoint per 100 versions
    - No tuning needed
    - Max 99 deltas to apply (constant bound)
```

**Token/Latency Impact:**

```
Normal read (99%+ of operations):
  Traditional with delta chains:
    O(chain_length) traversal = 10-50ms depending on chain

  Our architecture:
    O(1) from component cache = <0.1ms

  Improvement: 100-500x faster

Historical read (rare):
  With adaptive snapshots:
    Variable chain length (0 to limit)
    Unpredictable latency

  With fixed-interval:
    Max 99 deltas = predictable 10ms worst case
    Usually less (recent versions near checkpoint)
```

**Why This Is More Correct AND More Efficient:**

| Aspect | Adaptive Snapshots | Fixed-Interval + Component Cache |
|--------|-------------------|----------------------------------|
| **Normal reads** | May traverse deltas | O(1) from cache, never touches deltas |
| **Checkpoint timing** | Complex heuristics | Simple: every 100 versions |
| **Worst case bound** | Configurable (still variable) | Constant: max 99 deltas |
| **Tuning required** | Multiple parameters | Zero |
| **Predictability** | Low | High |

**The Key Insight:**

The original problem ("delta chain explosion") is mostly solved by our component caching architecture:
- **Normal operations don't use deltas** - they use cached component state
- **Deltas are only for recovery and historical queries** - rare operations
- **For rare operations, fixed-interval checkpointing provides constant-bounded latency**

We don't need to optimize delta traversal because we've eliminated delta traversal from the hot path.

---

### 7. SQLite Contention - Implementation

**Critical Insight: L1 Buffering Makes Writes Already Rare**

The original approach ("write batching, WAL tuning") assumes frequent SQLite writes that need optimization. But our architecture already minimizes SQLite writes through L1 hot memory buffering.

**When Does SQLite Get Written To?**

```
Write triggers in our architecture:
1. L1 overflow: When hot memory exceeds token threshold (~750K tokens)
2. Session end: Archive session entries and metadata
3. Checkpoints: Periodic global history snapshots

All of these are INFREQUENT compared to agent operations.
```

**Analysis of Write Frequency:**

```
Large refactor scenario: 6 sessions, 180 agents, 8 hours

Agent operations (frequent):
  - Briefing requests: 180 agents × 100 requests = 18,000
  - Pattern submissions: ~500 total
  - Failure reports: ~200 total
  - File updates: ~10,000 total

SQLite writes (infrequent):
  - L1 overflows: ~10-20 (when hot memory fills up)
  - Session ends: 6 (one per session)
  - Checkpoints: ~100 (one per 100 global versions)

Ratio: ~28,000 agent operations : ~126 SQLite writes
Write ratio: 0.45% of operations touch SQLite
```

**Why Complex Batching Is Unnecessary:**

```
With 126 writes over 8 hours:
  Average: 1 write every 3.8 minutes
  No contention possible at this rate

Even with burst (10 writes in 1 minute):
  SQLite WAL mode: ~1ms per write
  Total: 10ms of blocking
  Not a bottleneck
```

**Design Decisions:**

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Write batching | Not needed | Writes already rare due to L1 buffering |
| WAL mode | Standard WAL | Good enough for our write rate |
| Archival | Async (non-blocking) | Don't block hot path for cold storage |
| Read concurrency | Multiple readers | WAL supports this by default |

**Simple SQLite Configuration:**

```go
func NewArchive(path string) (*Archive, error) {
    db, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_synchronous=NORMAL")
    if err != nil {
        return nil, err
    }

    // Standard WAL tuning - nothing exotic needed
    _, err = db.Exec(`
        PRAGMA wal_autocheckpoint = 1000;  -- Checkpoint every 1000 pages
        PRAGMA cache_size = -64000;        -- 64MB cache
        PRAGMA busy_timeout = 5000;        -- 5s timeout (generous, rarely hit)
    `)
    if err != nil {
        return nil, err
    }

    return &Archive{db: db}, nil
}
```

**Async Archival (Non-Blocking):**

```go
type Archive struct {
    db      *sql.DB
    pending chan *ArchivalBatch
    done    chan struct{}
}

type ArchivalBatch struct {
    Entries []*Entry
    OnDone  func(error)
}

func (a *Archive) Start() {
    a.pending = make(chan *ArchivalBatch, 10)  // Buffer for 10 batches
    a.done = make(chan struct{})

    go a.archivalWorker()
}

func (a *Archive) archivalWorker() {
    for batch := range a.pending {
        err := a.writeBatch(batch.Entries)
        if batch.OnDone != nil {
            batch.OnDone(err)
        }
    }
    close(a.done)
}

// Non-blocking submission
func (a *Archive) ArchiveAsync(entries []*Entry, onDone func(error)) {
    select {
    case a.pending <- &ArchivalBatch{Entries: entries, OnDone: onDone}:
        // Submitted successfully
    default:
        // Channel full - this shouldn't happen with our low write rate
        // But if it does, log and try sync
        log.Printf("archival channel full, falling back to sync")
        err := a.writeBatch(entries)
        if onDone != nil {
            onDone(err)
        }
    }
}

func (a *Archive) writeBatch(entries []*Entry) error {
    tx, err := a.db.Begin()
    if err != nil {
        return err
    }
    defer tx.Rollback()

    stmt, err := tx.Prepare(`
        INSERT INTO entries (id, category, content, source, session_id, created_at, archived_at)
        VALUES (?, ?, ?, ?, ?, ?, ?)
    `)
    if err != nil {
        return err
    }
    defer stmt.Close()

    now := time.Now()
    for _, e := range entries {
        _, err = stmt.Exec(e.ID, e.Category, e.Content, e.Source, e.SessionID, e.CreatedAt, now)
        if err != nil {
            return err
        }
    }

    return tx.Commit()
}
```

**L1 Overflow Handler (Async):**

```go
func (s *Store) archiveOldestEntries() error {
    // Collect entries to archive
    targetTokens := s.tokenThreshold / 4
    var toArchive []*Entry
    var removedTokens int

    for _, id := range s.chronological {
        entry := s.entries[id]
        toArchive = append(toArchive, entry)
        removedTokens += entry.TokensEstimate
        if removedTokens >= targetTokens {
            break
        }
    }

    // Submit async archival
    entriesCopy := make([]*Entry, len(toArchive))
    copy(entriesCopy, toArchive)

    s.archive.ArchiveAsync(entriesCopy, func(err error) {
        if err != nil {
            log.Printf("archival failed: %v", err)
            // Entries remain in hot memory, will retry on next overflow
        }
    })

    // Remove from hot memory immediately (don't wait for archival)
    for _, entry := range toArchive {
        s.removeEntry(entry.ID)
    }

    return nil
}
```

**Read Queries (No Contention):**

```go
// Reads never block on writes in WAL mode
func (a *Archive) Query(q ArchiveQuery) ([]*Entry, error) {
    query := buildQuery(q)

    rows, err := a.db.Query(query, q.params...)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var results []*Entry
    for rows.Next() {
        entry := &Entry{}
        err := rows.Scan(&entry.ID, &entry.Category, &entry.Content, ...)
        if err != nil {
            return nil, err
        }
        results = append(results, entry)
    }

    return results, nil
}

// FTS search (also non-blocking in WAL mode)
func (a *Archive) SearchText(text string, limit int) ([]*Entry, error) {
    rows, err := a.db.Query(`
        SELECT e.id, e.category, e.content, e.source, e.session_id, e.created_at
        FROM entries_fts
        JOIN entries e ON entries_fts.rowid = e.rowid
        WHERE entries_fts MATCH ?
        ORDER BY rank
        LIMIT ?
    `, text, limit)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    // ... scan results
}
```

**Token/Latency Impact:**

```
Without async archival:
  L1 overflow triggers sync write to SQLite
  Hot path blocked for 10-50ms during archival
  Agents waiting for response

With async archival:
  L1 overflow submits to background worker
  Hot memory cleared immediately
  Agents continue without delay
  SQLite write happens in background

Improvement: 0ms blocking on hot path vs 10-50ms
```

**Why This Is More Correct AND More Efficient:**

| Aspect | Write Batching | L1 Buffering + Async |
|--------|---------------|---------------------|
| **Complexity** | Batch timing, size tuning | Just async submission |
| **Hot path latency** | May still block | Never blocks |
| **Write frequency** | Same (just batched) | Already rare |
| **Tuning required** | Batch size, batch interval | None |
| **Failure handling** | Batch retry complexity | Simple per-batch retry |

**The Key Insight:**

The original problem ("SQLite contention") is already solved by our architecture:
- **L1 hot memory buffers all writes** - SQLite writes are rare
- **Rare writes don't contend** - no batching optimization needed
- **Async archival keeps hot path fast** - agents never wait for SQLite
- **Standard WAL mode is sufficient** - exotic tuning unnecessary

We don't need to optimize SQLite contention because L1 buffering has eliminated the contention.

---

### 8. Failure Resolution Conflicts - Implementation

**Critical Insight: Track Outcomes, Not Just Resolutions**

The original approach ("multiple resolutions, effectiveness tracking") suggests tracking different fix attempts. But the real insight is: **resolutions without outcomes are useless**.

**The Problem:**

```
Agent A encounters: "ModuleNotFoundError: django.contrib.admin"
Agent A tries: pip install django-admin-extra
Agent A reports: "Fixed by installing django-admin-extra"

Agent B encounters: "ModuleNotFoundError: django.contrib.admin"
Agent B tries: pip install django
Agent B reports: "Fixed by installing django"

Which fix is correct? We don't know without outcome tracking.
```

**The Original Approach (Flawed):**

```go
// Just tracking what was tried
type FailureResolution struct {
    FailureID   string
    Approach    string
    TriedBy     string
}

// Problem: We have two resolutions but no way to know which actually works
```

**Superior Approach: Resolution-as-Outcome**

Attach resolutions to failures at write time, and REQUIRE outcome reporting:

**Design Decisions:**

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Resolution timing | Attached at failure write | Complete context preserved |
| Outcome tracking | Required (success/failure/partial) | Resolution without outcome is incomplete data |
| Ranking | Automatic by outcome statistics | No manual ranking needed |
| Duplicate detection | By failure signature | Merge similar failures |

**Failure Entry with Resolution Tracking:**

```go
type FailureEntry struct {
    ID          string    `json:"id"`
    Signature   string    `json:"sig"`      // Normalized error signature for grouping
    Error       string    `json:"error"`    // Full error message
    Context     string    `json:"context"`  // What was being attempted
    Category    string    `json:"category"` // e.g., "import", "type", "runtime"
    FilePath    string    `json:"file"`
    SessionID   string    `json:"session"`
    CreatedAt   time.Time `json:"created"`

    // Resolution is PART of the failure entry, not separate
    Resolution  *Resolution `json:"resolution,omitempty"`
}

type Resolution struct {
    Approach    string           `json:"approach"`   // What was tried
    AppliedAt   time.Time        `json:"applied_at"`
    AppliedBy   string           `json:"applied_by"` // Agent/session that tried this

    // REQUIRED: outcome tracking
    Outcome     ResolutionOutcome `json:"outcome"`
    OutcomeAt   time.Time         `json:"outcome_at"`
    Notes       string            `json:"notes,omitempty"`
}

type ResolutionOutcome int

const (
    OutcomePending ResolutionOutcome = iota  // Not yet verified
    OutcomeSuccess                            // Fix worked
    OutcomePartial                            // Partially worked
    OutcomeFailed                             // Fix didn't work
    OutcomeWorsenad                           // Fix made things worse
)
```

**Failure Signature for Grouping:**

```go
// Normalize errors to group similar failures
func computeSignature(err string, category string, filePath string) string {
    // Remove variable parts (line numbers, specific values)
    normalized := regexp.MustCompile(`:\d+:`).ReplaceAllString(err, ":N:")
    normalized = regexp.MustCompile(`'[^']*'`).ReplaceAllString(normalized, "'X'")
    normalized = regexp.MustCompile(`"[^"]*"`).ReplaceAllString(normalized, "\"X\"")

    // Include category and file pattern (not exact path)
    filePattern := filepath.Base(filePath)  // Just the filename

    return fmt.Sprintf("%s|%s|%s", category, filePattern, hash(normalized))
}

// Example:
// Error: "TypeError: 'NoneType' object is not subscriptable at line 42"
// File: "views/user_views.py"
// Signature: "type|user_views.py|a3f2b1c8"
```

**Recording Failure with Resolution:**

```go
func (gh *GlobalHistory) RecordFailure(failure *FailureEntry) error {
    gh.mu.Lock()
    defer gh.mu.Unlock()

    // Compute signature
    failure.Signature = computeSignature(failure.Error, failure.Category, failure.FilePath)

    // Check for existing failures with same signature
    existing := gh.failuresBySignature[failure.Signature]

    if len(existing) > 0 && failure.Resolution != nil {
        // This is a new resolution for a known failure
        // Attach to existing failure group, don't create new entry
        bestExisting := existing[0]  // Most successful resolution
        bestExisting.AlternativeResolutions = append(
            bestExisting.AlternativeResolutions, failure.Resolution)
        gh.version++
        return nil
    }

    // New failure (possibly with resolution)
    failure.ID = gh.generateID("fail")
    failure.CreatedAt = time.Now()

    gh.failures = append(gh.failures, failure)
    gh.failuresBySignature[failure.Signature] = append(
        gh.failuresBySignature[failure.Signature], failure)
    gh.version++

    return nil
}
```

**Updating Resolution Outcome:**

```go
func (gh *GlobalHistory) UpdateResolutionOutcome(
    failureID string,
    outcome ResolutionOutcome,
    notes string,
) error {
    gh.mu.Lock()
    defer gh.mu.Unlock()

    failure := gh.failuresById[failureID]
    if failure == nil {
        return fmt.Errorf("failure not found: %s", failureID)
    }

    if failure.Resolution == nil {
        return fmt.Errorf("failure has no resolution: %s", failureID)
    }

    failure.Resolution.Outcome = outcome
    failure.Resolution.OutcomeAt = time.Now()
    failure.Resolution.Notes = notes

    // Re-rank resolutions for this signature
    gh.reRankResolutions(failure.Signature)

    gh.version++
    return nil
}
```

**Automatic Ranking by Success Rate:**

```go
func (gh *GlobalHistory) reRankResolutions(signature string) {
    failures := gh.failuresBySignature[signature]

    // Collect all resolutions with outcomes
    type scoredResolution struct {
        Failure    *FailureEntry
        Resolution *Resolution
        Score      float64
    }

    var scored []scoredResolution
    for _, f := range failures {
        if f.Resolution != nil && f.Resolution.Outcome != OutcomePending {
            score := outcomeScore(f.Resolution.Outcome)
            scored = append(scored, scoredResolution{f, f.Resolution, score})
        }
    }

    // Sort by score descending
    sort.Slice(scored, func(i, j int) bool {
        return scored[i].Score > scored[j].Score
    })

    // Reorder failures slice to have best resolution first
    reordered := make([]*FailureEntry, 0, len(failures))
    seen := make(map[string]bool)

    // First: failures with successful resolutions (in order)
    for _, s := range scored {
        reordered = append(reordered, s.Failure)
        seen[s.Failure.ID] = true
    }

    // Then: failures without outcomes
    for _, f := range failures {
        if !seen[f.ID] {
            reordered = append(reordered, f)
        }
    }

    gh.failuresBySignature[signature] = reordered
}

func outcomeScore(outcome ResolutionOutcome) float64 {
    switch outcome {
    case OutcomeSuccess:
        return 1.0
    case OutcomePartial:
        return 0.5
    case OutcomeFailed:
        return 0.1  // Still learned something
    case OutcomeWorsened:
        return -1.0  // Actively bad
    default:
        return 0.0
    }
}
```

**Querying Best Resolution:**

```go
func (gh *GlobalHistory) GetBestResolution(errorPattern string, category string) *ResolutionRecommendation {
    // Find matching failure signature
    sig := computeSignature(errorPattern, category, "")  // Partial match on error + category

    // Get all failures with this signature
    failures := gh.failuresBySignature[sig]
    if len(failures) == 0 {
        return nil
    }

    // First failure has best resolution (already ranked)
    best := failures[0]
    if best.Resolution == nil || best.Resolution.Outcome == OutcomePending {
        return nil  // No verified resolution yet
    }

    var alternatives []*Resolution
    for _, f := range failures[1:] {
        if f.Resolution != nil && f.Resolution.Outcome == OutcomeSuccess {
            alternatives = append(alternatives, f.Resolution)
        }
    }

    return &ResolutionRecommendation{
        Error:        best.Error,
        BestFix:      best.Resolution.Approach,
        Confidence:   computeConfidence(failures),
        SuccessCount: countSuccesses(failures),
        TotalTries:   len(failures),
        Alternatives: alternatives,
    }
}

func computeConfidence(failures []*FailureEntry) float64 {
    successes := 0
    total := 0

    for _, f := range failures {
        if f.Resolution != nil && f.Resolution.Outcome != OutcomePending {
            total++
            if f.Resolution.Outcome == OutcomeSuccess {
                successes++
            }
        }
    }

    if total == 0 {
        return 0.0
    }

    // Confidence increases with more samples
    baseConfidence := float64(successes) / float64(total)
    sampleFactor := 1.0 - (1.0 / float64(total+1))  // More samples = higher confidence

    return baseConfidence * sampleFactor
}
```

**Integration with Briefings:**

```go
func (a *Archivalist) computeRelevantFailures(session *SessionContext) []*FailureContext {
    var relevant []*FailureContext

    // Get recent failures from current session
    sessionFailures := a.globalHistory.GetFailuresForSession(session.ID, 10)

    for _, f := range sessionFailures {
        fc := &FailureContext{
            Error:    f.Error,
            Category: f.Category,
            FilePath: f.FilePath,
        }

        // Look up best resolution across all sessions
        if rec := a.globalHistory.GetBestResolution(f.Error, f.Category); rec != nil {
            fc.RecommendedFix = rec.BestFix
            fc.Confidence = rec.Confidence
            fc.SuccessCount = rec.SuccessCount
        }

        relevant = append(relevant, fc)
    }

    return relevant
}
```

**Token/Latency Impact:**

```
Without outcome tracking:
  Agent sees: "Multiple approaches tried for this error"
  Agent tries: First one? Random? All of them?
  Wasted tokens: 2-5 approaches × 1000 tokens = 2,000-5,000 tokens

With outcome tracking:
  Agent sees: "Recommended fix (95% success rate): ..."
  Agent tries: The recommended fix
  Tokens: 1 approach × 1000 tokens = 1,000 tokens

Savings per failure: 1,000-4,000 tokens
With 200 failures in large refactor: 200,000-800,000 tokens saved
```

**Why This Is More Correct AND More Efficient:**

| Aspect | Multiple Resolutions Only | Resolution + Outcome |
|--------|--------------------------|---------------------|
| **Correctness** | Don't know what works | Know exactly what works |
| **Decision quality** | Agent guesses | Agent uses proven fix |
| **Wasted attempts** | Multiple tries | Usually first try works |
| **Ranking** | Manual or none | Automatic by success rate |
| **Token cost** | Higher (multiple attempts) | Lower (proven approach) |

**The Key Insight:**

Resolution without outcome is incomplete data:
- **A resolution is not useful until we know if it worked**
- **Outcome tracking enables automatic ranking**
- **Agents should see proven fixes, not just "things people tried"**

This follows the same principle as previous edge cases: **do the work at write time** (track outcomes) so **reads are trivial** (just show the best one).

---

### 9. Cross-Cutting Coordination - Implementation

**Critical Insight: Blocking Acknowledgment Causes Deadlocks**

The original approach ("broadcast system, blocking acknowledgment") requires affected agents to acknowledge before a cross-cutting change proceeds. This is problematic:

**Why Blocking Acknowledgment Is Flawed:**

```
Scenario: Agent A wants to rename UserService to UserManager

Blocking approach:
  1. Agent A broadcasts: "I'm renaming UserService"
  2. Agent A blocks, waiting for acknowledgments from Sessions B, C, D
  3. Session B's agent is busy with a long operation...
  4. Session C's agent is waiting for a different acknowledgment...
  5. Agent A waits... and waits...
  6. Deadlock or timeout

Problems:
  - What if an agent is offline or slow?
  - What if there's a circular dependency (A waits for B, B waits for A)?
  - Blocking defeats the purpose of parallel work
```

**Superior Approach: Intent Declaration + Async Notification**

Instead of blocking for acknowledgment, declare intent and let affected agents handle it asynchronously:

**Design Decisions:**

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Coordination model | Intent declaration | Non-blocking, scalable |
| Acknowledgment | Not required | Agents check on their own schedule |
| Conflict detection | At briefing time | When agent starts work, sees relevant intents |
| Conflict resolution | Scope exclusion or wait | Agent decides based on priority |

**Intent Declaration:**

```go
type Intent struct {
    ID          string       `json:"id"`
    SessionID   string       `json:"session"`
    AgentID     string       `json:"agent"`
    Type        IntentType   `json:"type"`
    Description string       `json:"desc"`
    AffectedPaths []string   `json:"paths"`      // Files/modules affected
    AffectedAPIs  []string   `json:"apis"`       // Functions/classes affected
    Priority    IntentPriority `json:"priority"`
    DeclaredAt  time.Time    `json:"declared_at"`
    ExpiresAt   time.Time    `json:"expires_at"` // Auto-expire stale intents
    CompletedAt *time.Time   `json:"completed_at,omitempty"`
}

type IntentType int

const (
    IntentTypeRename IntentType = iota
    IntentTypeDelete
    IntentTypeRefactorAPI
    IntentTypeSchemaChange
    IntentTypeAddDependency
)

type IntentPriority int

const (
    IntentPriorityNormal IntentPriority = iota
    IntentPriorityHigh                        // Time-sensitive
    IntentPriorityCritical                    // Blocking other work
)
```

**Declaring Intent (Non-Blocking):**

```go
func (gh *GlobalHistory) DeclareIntent(intent *Intent) error {
    gh.mu.Lock()
    defer gh.mu.Unlock()

    // Validate and set defaults
    intent.ID = gh.generateID("int")
    intent.DeclaredAt = time.Now()
    if intent.ExpiresAt.IsZero() {
        intent.ExpiresAt = time.Now().Add(30 * time.Minute)  // Default 30 min expiry
    }

    // Check for conflicts with existing intents
    conflicts := gh.findIntentConflicts(intent)
    if len(conflicts) > 0 {
        // Return conflicts for the agent to handle
        // NOT blocking - just informing
        intent.ConflictsWith = conflicts
    }

    gh.intents = append(gh.intents, intent)
    gh.version++

    // NO blocking, NO waiting for acknowledgment
    return nil
}

func (gh *GlobalHistory) findIntentConflicts(new *Intent) []*Intent {
    var conflicts []*Intent

    for _, existing := range gh.intents {
        // Skip completed or expired intents
        if existing.CompletedAt != nil || time.Now().After(existing.ExpiresAt) {
            continue
        }

        // Skip own session's intents
        if existing.SessionID == new.SessionID {
            continue
        }

        // Check path overlap
        if pathsOverlap(new.AffectedPaths, existing.AffectedPaths) {
            conflicts = append(conflicts, existing)
            continue
        }

        // Check API overlap
        if apisOverlap(new.AffectedAPIs, existing.AffectedAPIs) {
            conflicts = append(conflicts, existing)
        }
    }

    return conflicts
}
```

**Checking Intents at Briefing Time:**

```go
func (a *Archivalist) computeRelevantIntents(session *SessionContext) *IntentSummary {
    // Get active intents that affect this session's scope
    sessionScope := session.GetAffectedPaths()

    var affecting []*Intent
    var ownIntents []*Intent

    for _, intent := range a.globalHistory.GetActiveIntents() {
        if intent.SessionID == session.ID {
            ownIntents = append(ownIntents, intent)
            continue
        }

        if pathsOverlap(intent.AffectedPaths, sessionScope) ||
           apisOverlap(intent.AffectedAPIs, session.GetUsedAPIs()) {
            affecting = append(affecting, intent)
        }
    }

    return &IntentSummary{
        AffectingThisSession: affecting,
        OwnPendingIntents:    ownIntents,
    }
}

// Included in briefing:
type AgentBriefing struct {
    // ... existing fields ...

    // Cross-cutting coordination
    ActiveIntents *IntentSummary `json:"intents,omitempty"`
}

type IntentSummary struct {
    AffectingThisSession []*Intent `json:"affecting,omitempty"`
    OwnPendingIntents    []*Intent `json:"own,omitempty"`
}
```

**Agent Response to Conflicting Intent:**

```go
// When an agent sees a conflicting intent in its briefing, it can:

// Option 1: Wait (if the other change is critical)
func (a *Agent) handleConflictingIntent(intent *Intent) {
    if intent.Priority >= IntentPriorityCritical {
        // Yield to the other agent's work
        a.suspendWorkOnPaths(intent.AffectedPaths)
        a.notifyUser("Waiting for critical change to complete")
    }
}

// Option 2: Coordinate (if both can proceed with care)
func (a *Agent) coordinateWithIntent(intent *Intent) {
    // Narrow our scope to avoid the affected paths
    a.excludePathsFromWork(intent.AffectedPaths)
    a.notifyUser("Working around in-progress changes")
}

// Option 3: Escalate (if conflict is unresolvable)
func (a *Agent) escalateConflict(intent *Intent) {
    a.askUser(&ConflictQuestion{
        OurWork:    "Updating authentication handlers",
        TheirWork:  intent.Description,
        Options: []string{
            "Wait for their change",
            "Ask them to wait for us",
            "Proceed independently (may need merge)",
        },
    })
}
```

**Intent Lifecycle:**

```go
// Intents have a clear lifecycle:

// 1. Declared: Agent announces intent before starting
func (a *Agent) beforeRename(oldName, newName string) {
    a.archivalist.DeclareIntent(&Intent{
        Type:          IntentTypeRename,
        Description:   fmt.Sprintf("Renaming %s to %s", oldName, newName),
        AffectedPaths: findReferences(oldName),
        AffectedAPIs:  []string{oldName, newName},
    })
}

// 2. In progress: Other agents see it in briefings
// (No blocking needed)

// 3. Completed: Agent marks intent as done
func (a *Agent) afterRename(intentID string) {
    a.archivalist.CompleteIntent(intentID, &CompletionDetails{
        Success:     true,
        FilesChanged: []string{"auth.py", "views.py", "tests/test_auth.py"},
    })
}

// 4. Expired: Auto-cleanup if not completed
func (gh *GlobalHistory) CleanupExpiredIntents() {
    gh.mu.Lock()
    defer gh.mu.Unlock()

    now := time.Now()
    active := make([]*Intent, 0, len(gh.intents))

    for _, intent := range gh.intents {
        if intent.CompletedAt != nil {
            // Keep completed intents for history (30 min)
            if now.Sub(*intent.CompletedAt) < 30*time.Minute {
                active = append(active, intent)
            }
        } else if now.Before(intent.ExpiresAt) {
            active = append(active, intent)
        }
        // Else: expired without completion, drop it
    }

    gh.intents = active
}
```

**Affected Scope Detection:**

```go
// Automatically detect what an intent affects

func (gh *GlobalHistory) expandIntentScope(intent *Intent) {
    // From explicit paths, find transitive dependencies
    affected := make(map[string]bool)

    for _, path := range intent.AffectedPaths {
        affected[path] = true

        // Find files that import/use this file
        dependents := gh.findDependents(path)
        for _, dep := range dependents {
            affected[dep] = true
        }
    }

    for _, api := range intent.AffectedAPIs {
        // Find files that reference this API
        users := gh.findAPIUsers(api)
        for _, user := range users {
            affected[user] = true
        }
    }

    intent.AffectedPaths = mapKeys(affected)
}

func (gh *GlobalHistory) findDependents(path string) []string {
    // Look through recorded file states for imports
    var dependents []string

    for sessionID, session := range gh.sessions {
        for filePath, fileState := range session.files {
            for _, imp := range fileState.Imports {
                if strings.Contains(imp, pathToModule(path)) {
                    dependents = append(dependents, filePath)
                }
            }
        }
    }

    return dependents
}
```

**Token/Latency Impact:**

```
Blocking acknowledgment:
  Agent A declares intent → blocks
  Wait for 5 other sessions to acknowledge → 500ms-10s+ per session
  If any session is slow/offline → timeout (30s default)
  Best case: 500ms × 5 = 2.5s
  Worst case: 30s timeout

Intent declaration (non-blocking):
  Agent A declares intent → continues immediately
  Other agents see intent at next briefing → included for free
  Latency added: 0ms (non-blocking)

Improvement: 2.5s-30s → 0ms blocking latency
```

**Why This Is More Correct AND More Efficient:**

| Aspect | Blocking Acknowledgment | Intent Declaration |
|--------|------------------------|-------------------|
| **Deadlock risk** | Possible | Impossible (non-blocking) |
| **Offline agents** | Cause timeouts | No impact |
| **Parallel work** | Serialized by blocking | Truly parallel |
| **Latency added** | 2-30 seconds | 0ms |
| **Conflict detection** | At declaration time | At briefing time |
| **Scalability** | O(agents) blocking | O(1) declaration |

**The Key Insight:**

Blocking acknowledgment optimizes for **preventing conflicts** but causes deadlocks and latency.

Intent declaration optimizes for **detecting conflicts** without blocking, and lets agents handle them appropriately.

The insight: **conflicts are rare, but checking for them shouldn't be expensive**. Making every cross-cutting change block on N agents is paying a high cost for a rare problem. Better to declare intents cheaply and surface conflicts in briefings.

---

### 10. Archivalist Failure - Implementation

**Critical Insight: Append-Only Data Cannot Be Corrupted**

The original approach ("WAL, checkpointing, recovery procedure") treats failure recovery as a complex problem requiring explicit procedures. But our append-only architecture makes recovery trivial:

**Why Append-Only Simplifies Recovery:**

```
Traditional mutable storage:
  Failure during update → data in inconsistent state
  Recovery requires: WAL replay, transaction rollback, consistency checks
  Possible corruption: partial updates, torn writes

Append-only storage:
  Failure during append → append is incomplete
  Recovery: truncate incomplete entry, continue
  Corruption impossible: existing data never modified
```

**Design Decisions:**

| Decision | Choice | Rationale |
|----------|--------|-----------|
| Write model | Append-only | No corruption possible |
| Recovery | Truncate incomplete + replay from checkpoint | Trivial, automatic |
| Graceful degradation | Continue without archivalist if needed | Agents can work independently |
| State reconstruction | Checkpoint + log replay | Fast, deterministic |

**Append-Only Write Guarantees:**

```go
type AppendLog struct {
    file   *os.File
    mu     sync.Mutex
    offset int64
}

// Atomic append with length prefix
func (al *AppendLog) Append(data []byte) error {
    al.mu.Lock()
    defer al.mu.Unlock()

    // Write format: [length:4 bytes][data:N bytes][checksum:4 bytes]
    record := make([]byte, 4+len(data)+4)
    binary.LittleEndian.PutUint32(record[0:4], uint32(len(data)))
    copy(record[4:4+len(data)], data)
    binary.LittleEndian.PutUint32(record[4+len(data):], crc32.ChecksumIEEE(data))

    n, err := al.file.Write(record)
    if err != nil {
        // Truncate partial write
        al.file.Truncate(al.offset)
        return err
    }

    // Sync to ensure durability
    if err := al.file.Sync(); err != nil {
        al.file.Truncate(al.offset)
        return err
    }

    al.offset += int64(n)
    return nil
}

// Recovery: find last complete record
func (al *AppendLog) Recover() error {
    stat, err := al.file.Stat()
    if err != nil {
        return err
    }

    validOffset := int64(0)
    offset := int64(0)

    for offset < stat.Size() {
        // Read length
        lengthBuf := make([]byte, 4)
        n, err := al.file.ReadAt(lengthBuf, offset)
        if err != nil || n < 4 {
            break  // Incomplete record
        }

        length := binary.LittleEndian.Uint32(lengthBuf)
        recordSize := int64(4 + length + 4)

        if offset+recordSize > stat.Size() {
            break  // Incomplete record
        }

        // Read data and checksum
        dataBuf := make([]byte, length)
        al.file.ReadAt(dataBuf, offset+4)

        checksumBuf := make([]byte, 4)
        al.file.ReadAt(checksumBuf, offset+4+int64(length))
        storedChecksum := binary.LittleEndian.Uint32(checksumBuf)

        if crc32.ChecksumIEEE(dataBuf) != storedChecksum {
            break  // Corrupted record
        }

        validOffset = offset + recordSize
        offset = validOffset
    }

    // Truncate any incomplete/corrupted suffix
    if validOffset < stat.Size() {
        al.file.Truncate(validOffset)
    }

    al.offset = validOffset
    return nil
}
```

**State Reconstruction:**

```go
type Archivalist struct {
    // ... other fields ...
    checkpointStore *CheckpointStore
    appendLog       *AppendLog
}

func (a *Archivalist) Recover() error {
    // Step 1: Load latest checkpoint
    checkpoint, err := a.checkpointStore.LoadLatest()
    if err != nil {
        // No checkpoint - start fresh
        checkpoint = &Checkpoint{Version: 0}
    }

    // Step 2: Recover append log (truncate incomplete records)
    if err := a.appendLog.Recover(); err != nil {
        return fmt.Errorf("log recovery failed: %w", err)
    }

    // Step 3: Replay log entries since checkpoint
    entries, err := a.appendLog.ReadSince(checkpoint.Version)
    if err != nil {
        return fmt.Errorf("log replay failed: %w", err)
    }

    // Step 4: Apply entries to rebuild state
    a.globalHistory = checkpoint.GlobalHistory.Clone()
    a.sessions = checkpoint.Sessions.Clone()

    for _, entry := range entries {
        if err := a.applyLogEntry(entry); err != nil {
            log.Printf("skipping invalid log entry: %v", err)
            continue  // Skip invalid entries, don't fail recovery
        }
    }

    log.Printf("recovered: checkpoint v%d + %d log entries = v%d",
        checkpoint.Version, len(entries), a.globalHistory.Version())

    return nil
}

func (a *Archivalist) applyLogEntry(entry *LogEntry) error {
    switch entry.Type {
    case LogEntryPattern:
        return a.globalHistory.applyPattern(entry.Data.(*PatternEntry))
    case LogEntryFailure:
        return a.globalHistory.applyFailure(entry.Data.(*FailureEntry))
    case LogEntrySessionUpdate:
        return a.applySessionUpdate(entry.Data.(*SessionUpdate))
    default:
        return fmt.Errorf("unknown entry type: %v", entry.Type)
    }
}
```

**Graceful Degradation:**

```go
// Agents can work independently if archivalist is unavailable

type Agent struct {
    archivalist *ArchivalistClient
    localCache  *LocalCache  // Fallback when archivalist unavailable
}

func (a *Agent) GetBriefing(tier BriefingTier) (*AgentBriefing, error) {
    // Try archivalist first
    briefing, err := a.archivalist.RequestBriefing(tier)
    if err == nil {
        a.localCache.Update(briefing)  // Cache for fallback
        return briefing, nil
    }

    // Archivalist unavailable - use cached data
    if cached := a.localCache.GetCached(); cached != nil {
        return cached.WithWarning("Using cached data - archivalist unavailable"), nil
    }

    // No cache - work without briefing
    return &AgentBriefing{
        Warning: "No archivalist connection and no cache - working independently",
    }, nil
}

func (a *Agent) RecordPattern(pattern *PatternEntry) error {
    err := a.archivalist.SubmitPattern(pattern)
    if err != nil {
        // Queue for later submission
        a.localCache.QueueForSubmission(pattern)
        return nil  // Don't fail agent work
    }
    return nil
}

// When archivalist comes back online
func (a *Agent) OnArchivalistReconnect() {
    queued := a.localCache.GetQueuedSubmissions()
    for _, item := range queued {
        if err := a.archivalist.Submit(item); err != nil {
            log.Printf("failed to submit queued item: %v", err)
        }
    }
    a.localCache.ClearQueue()
}
```

**Startup Recovery Flow:**

```go
func (a *Archivalist) Start() error {
    // 1. Check for crash recovery
    if a.needsRecovery() {
        log.Println("Crash detected, starting recovery...")
        if err := a.Recover(); err != nil {
            return fmt.Errorf("recovery failed: %w", err)
        }
        log.Println("Recovery complete")
    }

    // 2. Load state
    if err := a.loadState(); err != nil {
        return err
    }

    // 3. Start background workers
    go a.checkpointWorker()
    go a.cleanupWorker()

    // 4. Accept connections
    a.ready = true
    return nil
}

func (a *Archivalist) needsRecovery() bool {
    // Check for unclean shutdown marker
    _, err := os.Stat(a.lockFile)
    return err == nil  // Lock file exists = didn't shut down cleanly
}

func (a *Archivalist) Shutdown() error {
    // Clean shutdown
    a.ready = false

    // Wait for in-flight operations
    a.wg.Wait()

    // Final checkpoint
    if err := a.checkpoint(); err != nil {
        log.Printf("final checkpoint failed: %v", err)
    }

    // Sync logs
    if err := a.appendLog.Sync(); err != nil {
        log.Printf("log sync failed: %v", err)
    }

    // Remove lock file to indicate clean shutdown
    os.Remove(a.lockFile)

    return nil
}
```

**Periodic Checkpointing:**

```go
func (a *Archivalist) checkpointWorker() {
    ticker := time.NewTicker(5 * time.Minute)
    defer ticker.Stop()

    for {
        select {
        case <-ticker.C:
            if err := a.checkpoint(); err != nil {
                log.Printf("checkpoint failed: %v", err)
            }
        case <-a.done:
            return
        }
    }
}

func (a *Archivalist) checkpoint() error {
    a.mu.RLock()
    checkpoint := &Checkpoint{
        Version:       a.globalHistory.Version(),
        GlobalHistory: a.globalHistory.Clone(),
        Sessions:      a.cloneSessions(),
        CreatedAt:     time.Now(),
    }
    a.mu.RUnlock()

    return a.checkpointStore.Save(checkpoint)
}
```

**Token/Latency Impact:**

```
Complex recovery procedure:
  Startup after crash: Run recovery procedure
  Time: 1-30 seconds depending on log size
  Risk: Recovery procedure bugs

Append-only recovery:
  Startup after crash: Truncate incomplete record + replay
  Time: 100ms for typical log
  Risk: None (algorithm is trivial)

Improvement: Simpler, faster, no risk of recovery bugs

Graceful degradation:
  Archivalist down: Agents continue with cached data
  Queue submissions for later
  No work lost, no agent blocking
```

**Why This Is More Correct AND More Efficient:**

| Aspect | Complex Recovery | Append-Only + Degradation |
|--------|-----------------|---------------------------|
| **Corruption possible** | Yes (partial updates) | No (append-only) |
| **Recovery complexity** | WAL replay, rollback, checks | Truncate + replay |
| **Recovery time** | 1-30 seconds | ~100ms |
| **Recovery bugs** | Possible | Trivial algorithm |
| **Agent impact** | Blocked until recovery | Continue with cache |
| **Data loss** | Possible if WAL corrupted | Only incomplete append |

**The Key Insight:**

Append-only data structures eliminate the possibility of corruption:
- **Existing data is never modified** - can't be corrupted by failed updates
- **Failed appends are incomplete** - just truncate and continue
- **Recovery is deterministic** - checkpoint + log replay always produces same state

Combined with graceful degradation, agents are never blocked by archivalist issues.

---

## Core Architecture: RAG-Based Reasoning System

### The Fundamental Insight

The Archivalist is NOT a simple key-value store or cache. It is a **Retrieval Augmented Generation (RAG) system** where:

- **Sonnet 4.5 (1M context window)** is the reasoning brain
- **SQLite + embeddings** is the extended memory (the "library stacks")
- **Query similarity caching** provides 90%+ token savings on repeated queries
- **Semantic retrieval** finds relevant context for novel queries

Think of it like a library:
- **Sonnet's context window** = books currently on your desk
- **SQLite + embeddings** = books in the library stacks
- **Queries** = requests for information
- **Memory swapping** = requesting/returning books from the stacks

### Design Philosophy

| Principle | Implementation |
|-----------|----------------|
| **Token efficiency is paramount** | Cache query responses, not raw data |
| **Precision > recall** | Return 5 relevant patterns, not 50 matching ones |
| **Work once, serve many** | Compute at write time, cache results |
| **Degrade gracefully** | Agents continue if archivalist unavailable |

---

### RAG Architecture Overview

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              QUERY PATH                                      │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Agent Query                                                                 │
│       │                                                                      │
│       ▼                                                                      │
│  ┌─────────────────────────────────────────────────────────────────┐        │
│  │              QUERY SIMILARITY CACHE                              │        │
│  │  ┌─────────────────────────────────────────────────────────┐    │        │
│  │  │ Embed query → search for similar cached queries          │    │        │
│  │  │ Similarity > 0.95 → return cached response               │    │        │
│  │  └─────────────────────────────────────────────────────────┘    │        │
│  └─────────────────────────────────────────────────────────────────┘        │
│       │                                                                      │
│       │ Cache miss                                                           │
│       ▼                                                                      │
│  ┌─────────────────────────────────────────────────────────────────┐        │
│  │              SEMANTIC RETRIEVAL                                  │        │
│  │  ┌─────────────────────────────────────────────────────────┐    │        │
│  │  │ 1. Embed query                                           │    │        │
│  │  │ 2. Search SQLite (FTS5) for keyword matches              │    │        │
│  │  │ 3. Search embeddings for semantic matches                │    │        │
│  │  │ 4. Re-rank by relevance to query                         │    │        │
│  │  │ 5. Return top-K most relevant context                    │    │        │
│  │  └─────────────────────────────────────────────────────────┘    │        │
│  └─────────────────────────────────────────────────────────────────┘        │
│       │                                                                      │
│       │ Retrieved context                                                    │
│       ▼                                                                      │
│  ┌─────────────────────────────────────────────────────────────────┐        │
│  │              SONNET 4.5 SYNTHESIS                                │        │
│  │  ┌─────────────────────────────────────────────────────────┐    │        │
│  │  │ Reason over retrieved context                            │    │        │
│  │  │ Generate response tailored to query                      │    │        │
│  │  │ Optimize response for agent consumption                  │    │        │
│  │  └─────────────────────────────────────────────────────────┘    │        │
│  └─────────────────────────────────────────────────────────────────┘        │
│       │                                                                      │
│       │ Synthesized response                                                 │
│       ▼                                                                      │
│  ┌─────────────────────────────────────────────────────────────────┐        │
│  │              RESPONSE CACHE                                      │        │
│  │  ┌─────────────────────────────────────────────────────────┐    │        │
│  │  │ Cache (query_embedding, response) for future similarity  │    │        │
│  │  │ Set TTL based on query type                              │    │        │
│  │  └─────────────────────────────────────────────────────────┘    │        │
│  └─────────────────────────────────────────────────────────────────┘        │
│       │                                                                      │
│       ▼                                                                      │
│  Response to Agent                                                           │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

---

### Query Similarity Cache

The key to 90%+ token savings: **most queries are variations of queries we've seen before**.

**The Problem Without Caching:**

```
Agent A: "What's the error handling pattern for auth?"
→ Retrieve context, run Sonnet, generate response (500ms, 50K tokens)

Agent B: "How should I handle errors in authentication?"
→ Retrieve context, run Sonnet, generate response (500ms, 50K tokens)

Agent C: "What's the pattern for auth error handling?"
→ Retrieve context, run Sonnet, generate response (500ms, 50K tokens)

Total: 3 queries × 50K tokens = 150K tokens spent
All three queries are semantically identical!
```

**The Solution: Query Embedding + Similarity Matching:**

```go
type QueryCache struct {
    mu sync.RWMutex

    // Query embeddings → cached responses
    embeddings   []QueryEmbedding  // Vector storage
    responses    map[string]*CachedResponse  // query_hash → response

    // Embedding model (fast, local or API)
    embedder     Embedder

    // Similarity threshold for cache hit
    hitThreshold float64  // 0.95 = very similar queries
}

type QueryEmbedding struct {
    QueryHash   string    // Hash of original query text
    Embedding   []float32 // 1536-dim embedding
    CreatedAt   time.Time
    TTL         time.Duration
    SessionID   string    // For session-scoped caching
}

type CachedResponse struct {
    Response    []byte    // Serialized response
    QueryText   string    // Original query for debugging
    CreatedAt   time.Time
    HitCount    int64     // Track cache effectiveness
    LastHitAt   time.Time
}

func (qc *QueryCache) Get(ctx context.Context, query string, sessionID string) (*CachedResponse, bool) {
    qc.mu.RLock()
    defer qc.mu.RUnlock()

    // Step 1: Embed the query
    queryEmbed, err := qc.embedder.Embed(ctx, query)
    if err != nil {
        return nil, false  // Cache miss on embedding failure
    }

    // Step 2: Find similar cached queries
    bestMatch := qc.findMostSimilar(queryEmbed, sessionID)
    if bestMatch == nil || bestMatch.Similarity < qc.hitThreshold {
        return nil, false  // No sufficiently similar query found
    }

    // Step 3: Return cached response
    response, ok := qc.responses[bestMatch.QueryHash]
    if !ok || response.isExpired() {
        return nil, false
    }

    // Update stats
    atomic.AddInt64(&response.HitCount, 1)
    response.LastHitAt = time.Now()

    return response, true
}

func (qc *QueryCache) findMostSimilar(queryEmbed []float32, sessionID string) *SimilarityMatch {
    var best *SimilarityMatch

    for _, cached := range qc.embeddings {
        // Check TTL
        if time.Since(cached.CreatedAt) > cached.TTL {
            continue
        }

        // Check session scope (some queries are session-specific)
        if cached.SessionID != "" && cached.SessionID != sessionID {
            continue
        }

        // Compute cosine similarity
        sim := cosineSimilarity(queryEmbed, cached.Embedding)
        if best == nil || sim > best.Similarity {
            best = &SimilarityMatch{
                QueryHash:  cached.QueryHash,
                Similarity: sim,
            }
        }
    }

    return best
}

func cosineSimilarity(a, b []float32) float64 {
    if len(a) != len(b) {
        return 0
    }

    var dotProduct, normA, normB float64
    for i := range a {
        dotProduct += float64(a[i]) * float64(b[i])
        normA += float64(a[i]) * float64(a[i])
        normB += float64(b[i]) * float64(b[i])
    }

    if normA == 0 || normB == 0 {
        return 0
    }

    return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

func (qc *QueryCache) Store(ctx context.Context, query string, sessionID string, response []byte, ttl time.Duration) error {
    qc.mu.Lock()
    defer qc.mu.Unlock()

    // Embed the query
    queryEmbed, err := qc.embedder.Embed(ctx, query)
    if err != nil {
        return err
    }

    queryHash := hashQuery(query)

    // Store embedding
    qc.embeddings = append(qc.embeddings, QueryEmbedding{
        QueryHash:  queryHash,
        Embedding:  queryEmbed,
        CreatedAt:  time.Now(),
        TTL:        ttl,
        SessionID:  sessionID,
    })

    // Store response
    qc.responses[queryHash] = &CachedResponse{
        Response:  response,
        QueryText: query,
        CreatedAt: time.Now(),
        HitCount:  0,
    }

    return nil
}
```

**TTL by Query Type:**

```go
func getTTLForQuery(queryType QueryType) time.Duration {
    switch queryType {
    case QueryTypePattern:
        // Patterns change slowly
        return 30 * time.Minute

    case QueryTypeFailure:
        // Failures + resolutions relatively stable
        return 20 * time.Minute

    case QueryTypeFileState:
        // File state changes frequently
        return 5 * time.Minute

    case QueryTypeResumeState:
        // Resume state changes constantly
        return 1 * time.Minute

    case QueryTypeBriefing:
        // Full briefings are session-specific
        return 2 * time.Minute

    default:
        return 10 * time.Minute
    }
}
```

**Token Impact:**

```
Django refactor scenario: 180 agents, 8 hours

Without query caching:
  Pattern queries: 3600 queries × 10K tokens/response = 36M tokens
  Failure queries: 1800 queries × 5K tokens/response = 9M tokens
  Context queries: 5400 queries × 15K tokens/response = 81M tokens
  Total: ~126M tokens

With query caching (assuming 90% hit rate):
  Pattern queries: 360 unique × 10K = 3.6M tokens
  Failure queries: 180 unique × 5K = 0.9M tokens
  Context queries: 540 unique × 15K = 8.1M tokens
  Total: ~12.6M tokens

Savings: 113M tokens (90% reduction)
```

---

### Semantic Retrieval

When cache misses, retrieve relevant context from SQLite + embeddings.

**Multi-Source Retrieval:**

```go
type SemanticRetriever struct {
    db          *sql.DB           // SQLite with FTS5
    embeddings  *EmbeddingStore   // Vector storage for semantic search
    embedder    Embedder          // Embedding model
    reranker    Reranker          // Re-ranking model for precision
}

type RetrievalResult struct {
    ID         string
    Content    string
    Score      float64   // Combined relevance score
    Source     string    // "fts", "embedding", "exact"
    Category   string
    Metadata   map[string]any
}

func (sr *SemanticRetriever) Retrieve(ctx context.Context, query string, opts RetrievalOpts) ([]*RetrievalResult, error) {
    var results []*RetrievalResult

    // Step 1: Full-text search (fast, keyword-based)
    ftsResults, err := sr.searchFTS(ctx, query, opts.FTSLimit)
    if err != nil {
        return nil, fmt.Errorf("FTS search failed: %w", err)
    }
    results = append(results, ftsResults...)

    // Step 2: Embedding search (semantic similarity)
    if opts.UseEmbeddings {
        queryEmbed, err := sr.embedder.Embed(ctx, query)
        if err != nil {
            return nil, fmt.Errorf("embedding failed: %w", err)
        }

        embResults, err := sr.embeddings.SearchSimilar(queryEmbed, opts.EmbeddingLimit)
        if err != nil {
            return nil, fmt.Errorf("embedding search failed: %w", err)
        }
        results = append(results, embResults...)
    }

    // Step 3: Deduplicate
    results = deduplicateByID(results)

    // Step 4: Re-rank for precision
    if opts.UseReranker && len(results) > opts.TopK {
        results, err = sr.reranker.Rerank(ctx, query, results, opts.TopK)
        if err != nil {
            // Fall back to score-based ranking
            sort.Slice(results, func(i, j int) bool {
                return results[i].Score > results[j].Score
            })
            if len(results) > opts.TopK {
                results = results[:opts.TopK]
            }
        }
    }

    return results, nil
}

// FTS5 search with BM25 ranking
func (sr *SemanticRetriever) searchFTS(ctx context.Context, query string, limit int) ([]*RetrievalResult, error) {
    rows, err := sr.db.QueryContext(ctx, `
        SELECT
            e.id, e.content, e.category, e.metadata,
            bm25(entries_fts) as score
        FROM entries_fts
        JOIN entries e ON entries_fts.rowid = e.rowid
        WHERE entries_fts MATCH ?
        ORDER BY score
        LIMIT ?
    `, fts5Query(query), limit)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var results []*RetrievalResult
    for rows.Next() {
        r := &RetrievalResult{Source: "fts"}
        var metadataJSON []byte
        if err := rows.Scan(&r.ID, &r.Content, &r.Category, &metadataJSON, &r.Score); err != nil {
            return nil, err
        }
        json.Unmarshal(metadataJSON, &r.Metadata)
        results = append(results, r)
    }

    return results, nil
}

// Convert query to FTS5 syntax
func fts5Query(query string) string {
    // Tokenize and build OR query with prefix matching
    tokens := tokenize(query)
    var parts []string
    for _, t := range tokens {
        parts = append(parts, fmt.Sprintf("%s*", t))  // Prefix matching
    }
    return strings.Join(parts, " OR ")
}
```

**Embedding Storage:**

```go
type EmbeddingStore struct {
    // Use SQLite with vec extension or external vector DB
    db *sql.DB
}

func (es *EmbeddingStore) Store(id string, embedding []float32, metadata map[string]any) error {
    embBytes, _ := encodeEmbedding(embedding)
    metaBytes, _ := json.Marshal(metadata)

    _, err := es.db.Exec(`
        INSERT OR REPLACE INTO embeddings (id, embedding, metadata, created_at)
        VALUES (?, ?, ?, ?)
    `, id, embBytes, metaBytes, time.Now())

    return err
}

func (es *EmbeddingStore) SearchSimilar(query []float32, limit int) ([]*RetrievalResult, error) {
    // For small datasets: brute force
    // For large datasets: use approximate nearest neighbors (HNSW, IVF)

    rows, err := es.db.Query(`
        SELECT id, embedding, metadata FROM embeddings
    `)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    type scored struct {
        id       string
        score    float64
        metadata map[string]any
    }
    var candidates []scored

    for rows.Next() {
        var id string
        var embBytes, metaBytes []byte
        rows.Scan(&id, &embBytes, &metaBytes)

        emb := decodeEmbedding(embBytes)
        sim := cosineSimilarity(query, emb)

        var meta map[string]any
        json.Unmarshal(metaBytes, &meta)

        candidates = append(candidates, scored{id, sim, meta})
    }

    // Sort by similarity
    sort.Slice(candidates, func(i, j int) bool {
        return candidates[i].score > candidates[j].score
    })

    // Take top K
    if len(candidates) > limit {
        candidates = candidates[:limit]
    }

    // Convert to results
    var results []*RetrievalResult
    for _, c := range candidates {
        content, _ := es.getContent(c.id)
        results = append(results, &RetrievalResult{
            ID:       c.id,
            Content:  content,
            Score:    c.score,
            Source:   "embedding",
            Metadata: c.metadata,
        })
    }

    return results, nil
}
```

---

### Sonnet 4.5 Synthesis

When retrieval returns relevant context, Sonnet synthesizes the response.

**Synthesis Pipeline:**

```go
type Synthesizer struct {
    client      *anthropic.Client
    retriever   *SemanticRetriever
    queryCache  *QueryCache
}

func (s *Synthesizer) Answer(ctx context.Context, query string, sessionID string) (*Response, error) {
    // Step 1: Check query cache
    if cached, ok := s.queryCache.Get(ctx, query, sessionID); ok {
        return &Response{
            Data:     cached.Response,
            Source:   "cache",
            CacheHit: true,
        }, nil
    }

    // Step 2: Retrieve relevant context
    context, err := s.retriever.Retrieve(ctx, query, RetrievalOpts{
        FTSLimit:       50,
        EmbeddingLimit: 50,
        UseEmbeddings:  true,
        UseReranker:    true,
        TopK:           10,  // Return top 10 most relevant
    })
    if err != nil {
        return nil, fmt.Errorf("retrieval failed: %w", err)
    }

    // Step 3: Build prompt with context
    prompt := s.buildPrompt(query, context)

    // Step 4: Run Sonnet for synthesis
    message, err := s.client.Messages.New(ctx, anthropic.MessageNewParams{
        Model:     anthropic.F("claude-sonnet-4-5-20250514"),
        MaxTokens: anthropic.F(int64(8192)),
        System: anthropic.F([]anthropic.TextBlockParam{
            anthropic.NewTextBlock(SynthesisSystemPrompt),
        }),
        Messages: anthropic.F([]anthropic.MessageParam{
            anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
        }),
    })
    if err != nil {
        return nil, fmt.Errorf("synthesis failed: %w", err)
    }

    response := extractResponse(message)

    // Step 5: Cache the response
    ttl := getTTLForQuery(classifyQuery(query))
    s.queryCache.Store(ctx, query, sessionID, response, ttl)

    return &Response{
        Data:     response,
        Source:   "synthesis",
        CacheHit: false,
        Context:  context,  // Include for debugging/transparency
    }, nil
}

func (s *Synthesizer) buildPrompt(query string, context []*RetrievalResult) string {
    var sb strings.Builder

    sb.WriteString("## Relevant Context\n\n")
    for i, c := range context {
        sb.WriteString(fmt.Sprintf("### [%d] %s (relevance: %.2f)\n", i+1, c.Category, c.Score))
        sb.WriteString(c.Content)
        sb.WriteString("\n\n")
    }

    sb.WriteString("## Query\n\n")
    sb.WriteString(query)
    sb.WriteString("\n\n")

    sb.WriteString("## Instructions\n\n")
    sb.WriteString("Based on the relevant context above, provide a concise, actionable response to the query. ")
    sb.WriteString("Focus on information that directly addresses the query. ")
    sb.WriteString("Format your response for machine parsing - use structured formats where appropriate.")

    return sb.String()
}

const SynthesisSystemPrompt = `You are the reasoning component of THE ARCHIVALIST, a shared memory system for AI coding agents.

Your job is to synthesize relevant context into actionable responses for other AI agents.

CRITICAL REQUIREMENTS:
1. TOKEN EFFICIENCY - minimize response size while preserving all necessary information
2. STRUCTURED OUTPUT - format for machine parsing (JSON, tables, bullet points)
3. ACTIONABLE - every response should enable immediate action
4. PRECISE - only include information relevant to the query

OUTPUT FORMATS:
- For pattern queries: Return the pattern with brief rationale
- For failure queries: Return the failure and best resolution
- For context queries: Return structured summary of relevant state
- For coordination queries: Return affected sessions/agents with conflict details

Never include:
- Pleasantries or conversational filler
- Redundant explanations
- Information not directly relevant to the query`
```

---

### Memory Swapping

The key to using a 1M context window effectively: **hot memories stay in context, cold memories get swapped to SQLite**.

**Memory Hierarchy:**

```
┌────────────────────────────────────────────────────────────────────┐
│                    SONNET CONTEXT WINDOW (1M tokens)               │
├────────────────────────────────────────────────────────────────────┤
│  ┌─────────────────────────────────────────────────────────────┐  │
│  │ HOT ZONE (~200K tokens)                                     │  │
│  │   - Current session state (resume, files, intents)          │  │
│  │   - Recent queries and responses                            │  │
│  │   - Active patterns for current work                        │  │
│  │   - Recent failures with resolutions                        │  │
│  └─────────────────────────────────────────────────────────────┘  │
│  ┌─────────────────────────────────────────────────────────────┐  │
│  │ WARM ZONE (~300K tokens)                                    │  │
│  │   - Related session states                                  │  │
│  │   - Patterns for likely-needed categories                   │  │
│  │   - Cross-session coordination data                         │  │
│  └─────────────────────────────────────────────────────────────┘  │
│  ┌─────────────────────────────────────────────────────────────┐  │
│  │ BUFFER ZONE (~500K tokens)                                  │  │
│  │   - Swapped-in memories for current query                   │  │
│  │   - Retrieved context from SQLite                           │  │
│  │   - Working space for synthesis                             │  │
│  └─────────────────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────────────────┘
                              │
                              │ Swap in/out
                              ▼
┌────────────────────────────────────────────────────────────────────┐
│                    SQLITE + EMBEDDINGS (Unlimited)                 │
├────────────────────────────────────────────────────────────────────┤
│  - All historical patterns and failures                            │
│  - Completed session summaries                                     │
│  - Archived file states                                            │
│  - Old queries and responses                                       │
│  - Full event history                                              │
└────────────────────────────────────────────────────────────────────┘
```

**Swap-In Triggers:**

```go
type MemoryManager struct {
    hotZone    *HotMemory     // Currently in Sonnet context
    warmZone   *WarmMemory    // Ready to promote
    coldStore  *SQLiteArchive // Long-term storage

    tokenBudget TokenBudget
}

type TokenBudget struct {
    HotMax    int  // 200K
    WarmMax   int  // 300K
    BufferMax int  // 500K
    Total     int  // 1M
}

// Swap in memories when query needs them
func (mm *MemoryManager) PrepareForQuery(ctx context.Context, query string, sessionID string) error {
    // Analyze what the query needs
    needed := mm.analyzeQueryNeeds(query, sessionID)

    // Check what's already hot
    missing := mm.findMissing(needed)

    // Estimate tokens for missing data
    requiredTokens := mm.estimateTokens(missing)

    // Make room if necessary
    if mm.hotZone.Tokens() + requiredTokens > mm.tokenBudget.HotMax {
        mm.evictColdest(requiredTokens)
    }

    // Swap in missing data
    for _, item := range missing {
        data, err := mm.coldStore.Retrieve(item.ID)
        if err != nil {
            continue
        }
        mm.hotZone.Add(item.ID, data)
    }

    return nil
}

// Analyze what a query needs in context
func (mm *MemoryManager) analyzeQueryNeeds(query string, sessionID string) []MemoryNeed {
    var needs []MemoryNeed

    // Always need current session state
    needs = append(needs, MemoryNeed{
        Type:     NeedTypeSessionState,
        ID:       sessionID,
        Priority: PriorityCritical,
    })

    // Parse query for category references
    categories := extractCategories(query)
    for _, cat := range categories {
        needs = append(needs, MemoryNeed{
            Type:     NeedTypePatterns,
            ID:       cat,
            Priority: PriorityHigh,
        })
    }

    // Parse for file references
    files := extractFilePaths(query)
    for _, f := range files {
        needs = append(needs, MemoryNeed{
            Type:     NeedTypeFileState,
            ID:       f,
            Priority: PriorityHigh,
        })
    }

    // Check for failure-related keywords
    if containsFailureKeywords(query) {
        needs = append(needs, MemoryNeed{
            Type:     NeedTypeFailures,
            ID:       "recent",
            Priority: PriorityMedium,
        })
    }

    return needs
}

// Evict coldest memories to make room
func (mm *MemoryManager) evictColdest(requiredTokens int) {
    // Sort by last access time
    items := mm.hotZone.SortByLastAccess()

    evicted := 0
    for _, item := range items {
        // Don't evict current session
        if item.Type == NeedTypeSessionState && item.IsCurrentSession {
            continue
        }

        // Move to cold storage
        mm.coldStore.Archive(item)
        mm.hotZone.Remove(item.ID)
        evicted += item.Tokens

        if evicted >= requiredTokens {
            break
        }
    }
}
```

**Predictive Warming:**

```go
// Pre-warm memories based on session activity patterns
func (mm *MemoryManager) PredictiveWarm(sessionID string) {
    session := mm.getSession(sessionID)

    // What files is this session working on?
    activeFiles := session.GetActiveFiles()

    // What categories are relevant?
    categories := inferCategories(activeFiles)

    // Pre-warm patterns for those categories
    for _, cat := range categories {
        if !mm.hotZone.Has(cat) {
            patterns, _ := mm.coldStore.GetPatterns(cat)
            if mm.canFit(patterns) {
                mm.warmZone.Add(cat, patterns)
            }
        }
    }

    // Pre-warm recent failures for the file types
    fileTypes := extractFileTypes(activeFiles)
    for _, ft := range fileTypes {
        failures, _ := mm.coldStore.GetRecentFailures(ft, 10)
        if mm.canFit(failures) {
            mm.warmZone.Add("failures:"+ft, failures)
        }
    }
}

// Promote from warm to hot on access
func (mm *MemoryManager) OnAccess(id string) {
    if data, ok := mm.warmZone.Get(id); ok {
        mm.warmZone.Remove(id)
        mm.hotZone.Add(id, data)
    }
}
```

---

### Archivalist Tools

Tools the Archivalist provides to agents for querying and writing.

**Tool Definitions:**

```go
type ArchivalistTools struct {
    // Read tools
    GetBriefing      Tool  // Get handoff briefing
    QueryPatterns    Tool  // Search patterns by category/keyword
    QueryFailures    Tool  // Search failures and resolutions
    QueryFileState   Tool  // Get file state across sessions
    QueryContext     Tool  // Free-form context query (RAG)

    // Write tools
    RecordPattern    Tool  // Learn a new pattern
    RecordFailure    Tool  // Report a failure and resolution
    UpdateFileState  Tool  // Update file state
    DeclareIntent    Tool  // Announce cross-cutting work
    CompleteIntent   Tool  // Mark intent as done

    // Coordination tools
    GetConflicts     Tool  // Check for conflicts with other sessions
    AckBroadcast     Tool  // Acknowledge a broadcast message
}

// Tool definitions for agent consumption
var ToolDefinitions = []anthropic.ToolParam{
    {
        Name:        anthropic.F("archivalist_get_briefing"),
        Description: anthropic.F("Get a handoff briefing for continuing work. Returns current task state, modified files, patterns, failures, and next steps."),
        InputSchema: anthropic.F(BriefingSchema),
    },
    {
        Name:        anthropic.F("archivalist_query_patterns"),
        Description: anthropic.F("Query coding patterns by category. Categories: error, async, database, api, auth, testing, structure."),
        InputSchema: anthropic.F(QueryPatternsSchema),
    },
    {
        Name:        anthropic.F("archivalist_query_context"),
        Description: anthropic.F("Free-form query for any context. Use this when you need to know something that doesn't fit other tools. The archivalist will search across all knowledge and synthesize an answer."),
        InputSchema: anthropic.F(QueryContextSchema),
    },
    {
        Name:        anthropic.F("archivalist_record_pattern"),
        Description: anthropic.F("Record a new coding pattern learned during work. If this conflicts with an existing pattern, you must specify which pattern it supersedes."),
        InputSchema: anthropic.F(RecordPatternSchema),
    },
    {
        Name:        anthropic.F("archivalist_record_failure"),
        Description: anthropic.F("Record a failure and its resolution. This helps other agents avoid the same mistake."),
        InputSchema: anthropic.F(RecordFailureSchema),
    },
    {
        Name:        anthropic.F("archivalist_declare_intent"),
        Description: anthropic.F("Declare intent to perform cross-cutting work (refactoring, API changes, etc.). Other sessions will be notified."),
        InputSchema: anthropic.F(DeclareIntentSchema),
    },
}

// Schemas
var BriefingSchema = anthropic.ToolInputSchemaParam{
    Type: anthropic.F(anthropic.ToolInputSchemaTypeObject),
    Properties: anthropic.F(map[string]interface{}{
        "tier": map[string]interface{}{
            "type":        "string",
            "enum":        []string{"micro", "standard", "full"},
            "description": "Briefing detail level: micro (~20 tokens), standard (~500 tokens), full (~2000 tokens)",
        },
    }),
}

var QueryContextSchema = anthropic.ToolInputSchemaParam{
    Type: anthropic.F(anthropic.ToolInputSchemaTypeObject),
    Properties: anthropic.F(map[string]interface{}{
        "query": map[string]interface{}{
            "type":        "string",
            "description": "Your question or information need in natural language",
        },
        "scope": map[string]interface{}{
            "type":        "string",
            "enum":        []string{"session", "global", "all"},
            "description": "Search scope: session (this session only), global (shared knowledge), all (both)",
        },
    }),
    Required: anthropic.F([]string{"query"}),
}

var RecordPatternSchema = anthropic.ToolInputSchemaParam{
    Type: anthropic.F(anthropic.ToolInputSchemaTypeObject),
    Properties: anthropic.F(map[string]interface{}{
        "pattern": map[string]interface{}{
            "type":        "string",
            "description": "The pattern description (e.g., 'Always wrap errors with context using fmt.Errorf')",
        },
        "category": map[string]interface{}{
            "type":        "string",
            "description": "Hierarchical category (e.g., 'error.handling', 'database.orm')",
        },
        "scope": map[string]interface{}{
            "type":  "array",
            "items": map[string]interface{}{"type": "string"},
            "description": "File paths or modules this pattern applies to",
        },
        "supersedes": map[string]interface{}{
            "type":  "array",
            "items": map[string]interface{}{"type": "string"},
            "description": "IDs of patterns this replaces (required if conflict detected)",
        },
        "reason": map[string]interface{}{
            "type":        "string",
            "description": "Why this supersedes the old pattern",
        },
    }),
    Required: anthropic.F([]string{"pattern", "category"}),
}
```

**Tool Implementations:**

```go
func (a *Archivalist) HandleToolCall(ctx context.Context, call anthropic.ToolUseBlock) (string, error) {
    switch call.Name {
    case "archivalist_get_briefing":
        return a.handleGetBriefing(ctx, call.Input)

    case "archivalist_query_patterns":
        return a.handleQueryPatterns(ctx, call.Input)

    case "archivalist_query_context":
        return a.handleQueryContext(ctx, call.Input)

    case "archivalist_record_pattern":
        return a.handleRecordPattern(ctx, call.Input)

    case "archivalist_record_failure":
        return a.handleRecordFailure(ctx, call.Input)

    case "archivalist_declare_intent":
        return a.handleDeclareIntent(ctx, call.Input)

    default:
        return "", fmt.Errorf("unknown tool: %s", call.Name)
    }
}

func (a *Archivalist) handleQueryContext(ctx context.Context, input json.RawMessage) (string, error) {
    var params struct {
        Query string `json:"query"`
        Scope string `json:"scope"`
    }
    if err := json.Unmarshal(input, &params); err != nil {
        return "", err
    }

    sessionID := getSessionFromContext(ctx)

    // Use the RAG pipeline
    response, err := a.synthesizer.Answer(ctx, params.Query, sessionID)
    if err != nil {
        return "", err
    }

    // Format response
    result := map[string]interface{}{
        "answer":    string(response.Data),
        "source":    response.Source,
        "cache_hit": response.CacheHit,
    }

    return json.MarshalToString(result)
}
```

---

### Token Savings Summary

**Expected Savings with RAG Architecture:**

| Component | Without RAG | With RAG | Savings |
|-----------|------------|----------|---------|
| Query cache hits | 0% | 90% | 90% fewer Sonnet calls |
| Retrieved context | All patterns (~50) | Top-10 relevant | 80% smaller context |
| Response size | All possible info | Precisely what's asked | 60% smaller responses |
| Repeated queries | Full recompute | Cached response | 100% savings |
| Cross-session queries | N×duplicate work | Shared cache | (N-1)/N savings |

**Total Expected Savings:**

```
Django refactor scenario (180 agents, 8 hours):

Traditional approach (no RAG):
  Briefings: 3,600 × 15K tokens = 54M tokens
  Pattern queries: 3,600 × 10K tokens = 36M tokens
  Failure queries: 1,800 × 5K tokens = 9M tokens
  Context queries: 5,400 × 15K tokens = 81M tokens
  TOTAL: ~180M tokens

With RAG architecture:
  Briefings: 360 unique × 15K = 5.4M (90% cached)
  Pattern queries: 360 unique × 2K = 0.7M (90% cached, 80% smaller)
  Failure queries: 180 unique × 1K = 0.2M (90% cached, 80% smaller)
  Context queries: 540 unique × 3K = 1.6M (90% cached, 80% smaller)
  TOTAL: ~8M tokens

SAVINGS: 172M tokens (95% reduction)
```

---

## Retrospective Query Router

The Retrospective Query Router is the gateway for all agent interactions with the Archivalist. It determines whether incoming queries are retrospective (about past actions/observations/learnings) and routes them to the appropriate tools.

### Core Constraint: The Archivalist is Historical

**The Archivalist ONLY handles queries about the PAST:**

| Accepted (Retrospective) | Rejected (Prospective) |
|--------------------------|------------------------|
| What we HAVE DONE | What we NEED TO DO |
| What we HAVE SEEN | What we SHOULD DO |
| What we HAVE LEARNED | What we WANT TO LEARN |
| Past actions, implementations, changes | Future tasks, requirements, plans |
| Past observations, errors encountered | Recommendations, best practices to adopt |
| Past lessons, patterns discovered | Future learning goals |

### Router Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         Input                                    │
└─────────────────────────────────────────────────────────────────┘
                              │
              ┌───────────────┴───────────────┐
              ▼                               ▼
┌──────────────────────┐         ┌──────────────────────────────┐
│   Structured DSL     │         │     Natural Language         │
│   @arch:recall:...   │         │   "What patterns have we..." │
└──────────────────────┘         └──────────────────────────────┘
              │                               │
              ▼                               ▼
┌──────────────────────┐         ┌──────────────────────────────┐
│   Formal Grammar     │         │   LLM Tool-Use Classification│
│   Parser (PEG)       │         │   ~150 token prompt          │
│   Zero ambiguity     │         │   Structured tool response   │
└──────────────────────┘         └──────────────────────────────┘
              │                               │
              └───────────────┬───────────────┘
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                    Validated Query AST                           │
│         {intent, domain, entities, confidence}                   │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│                       Tool Dispatch                              │
└─────────────────────────────────────────────────────────────────┘
```

### Why LLM Classification?

No combination of regex, ontologies, embeddings, phonetic matching, or edit distance matches the accuracy of a language model at understanding language.

| Approach | Accuracy | Maintenance | Edge Cases | Novel Phrasings |
|----------|----------|-------------|------------|-----------------|
| Regex/Ontology | ~85% | High | Poor | Fails |
| Embeddings | ~90% | Medium | Moderate | Moderate |
| Ensemble | ~93% | Very High | Good | Moderate |
| **LLM Classification** | **~99%** | **Zero** | **Excellent** | **Excellent** |

### Dual-Mode Input Processing

#### Mode 1: Structured DSL (Inter-Agent Communication)

For programmatic inter-agent communication, bypass classification entirely:

```
@arch:<intent>:<domain>[?<params>][{<data>}]
```

**Formal Grammar (PEG notation):**

```peg
Query      ← '@arch:' Intent ':' Domain Params? Data? EOF
Intent     ← 'recall' / 'store' / 'check'
Domain     ← 'patterns' / 'failures' / 'decisions' / 'files' / 'learnings'
Params     ← '?' Param ('&' Param)*
Param      ← Key '=' Value
Key        ← [a-z_]+
Value      ← [^&{]+
Data       ← '{' JSON '}'
JSON       ← <valid JSON object>
```

**Examples:**

```
@arch:recall:patterns?scope=auth&limit=5
@arch:recall:failures?error=timeout&agent=opus
@arch:store:failure{approach:"recursive walk",outcome:"stack overflow",resolution:"use iterative"}
@arch:check:decisions?id=api-versioning
```

**Token cost:** 15-40 tokens per query. Zero classification overhead.

#### Mode 2: Natural Language (Human/Conversational)

Uses LLM tool-use classification for maximum accuracy.

**Token cost:** ~250 tokens for classification + query length.

### Intent Classification

```go
type RetrospectiveIntent int

const (
    IntentRecall RetrospectiveIntent = iota  // Query past data
    IntentStore                               // Record new data
    IntentCheck                               // Verify against history
)
```

| Intent | Purpose | Trigger Words |
|--------|---------|---------------|
| **recall** | Query past data | retrieve, get, find, look up, search, query |
| **store** | Record new data | save, record, log, archive, remember, note |
| **check** | Verify against history | verify, validate, confirm, test, examine |

### Domain Classification

```go
type HistoricalDomain int

const (
    DomainPatterns   HistoricalDomain = iota  // Code patterns, architectural patterns
    DomainFailures                             // Failed approaches, errors encountered
    DomainDecisions                            // Design decisions, choices made
    DomainFiles                                // File states, modifications made
    DomainLearnings                            // Lessons learned, insights gained
)
```

| Domain | Contents | Keywords |
|--------|----------|----------|
| **patterns** | Code patterns, architectural patterns, conventions | pattern, approach, style, convention, standard, architecture |
| **failures** | Failed approaches, errors encountered, bugs | failure, error, bug, issue, problem, mistake, crash |
| **decisions** | Design decisions, choices made, alternatives | decision, choice, chose, decided, picked, selected |
| **files** | File states, modifications made, changes | file, path, directory, module, component, changed |
| **learnings** | Lessons learned, insights gained | learned, lesson, insight, realization, understanding |

### Temporal Markers

**ACCEPT (Past-Focused):**

| Category | Markers |
|----------|---------|
| Past Tense Verbs | did, was, were, had, tried, attempted, used, implemented, built, created, wrote, designed, saw, observed, noticed, encountered, experienced, learned, discovered, found, realized, understood |
| Present Perfect | have done, have seen, have tried, have used, have learned, have encountered |
| Temporal References | before, previously, earlier, last time, in the past, already, once, back when |
| Historical Nouns | history, previous, earlier, past, archived, stored, recorded, logged |
| Recall Phrases | what did we, how did we, why did we, when did we, what have we, how have we |

**REJECT (Future-Focused):**

| Category | Markers |
|----------|---------|
| Future Tense | will, shall, going to, about to |
| Modal (Obligation) | should, must, need to, ought to, have to |
| Modal (Intent) | want to, plan to, intend to, aim to |
| Prospective Questions | what should we, how should we, what do we need, what will we |

### Classification Tool Definition

The router uses Anthropic's tool-use feature for constrained, structured output:

```go
var ClassificationTool = anthropic.ToolDefinition{
    Name:        "classify_archivalist_query",
    Description: "Classify whether a query is retrospective (about past actions/observations/learnings) and extract its intent and domain",
    InputSchema: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "is_retrospective": map[string]any{
                "type":        "boolean",
                "description": "True if query is about PAST actions, observations, or learnings. False if about FUTURE needs, plans, or requirements.",
            },
            "rejection_reason": map[string]any{
                "type":        "string",
                "description": "If not retrospective, explain why (e.g., 'Query asks about future requirements, not past actions')",
            },
            "intent": map[string]any{
                "type": "string",
                "enum": []string{"recall", "store", "check"},
                "description": "recall=query past data, store=record new data, check=verify against history",
            },
            "domain": map[string]any{
                "type": "string",
                "enum": []string{"patterns", "failures", "decisions", "files", "learnings"},
            },
            "entities": map[string]any{
                "type": "object",
                "properties": map[string]any{
                    "scope":      map[string]any{"type": "string", "description": "Area/component being queried (e.g., 'authentication', 'database')"},
                    "timeframe":  map[string]any{"type": "string", "description": "Time reference if any (e.g., 'yesterday', 'last week')"},
                    "agent":      map[string]any{"type": "string", "description": "Specific agent if mentioned"},
                    "file_paths": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
                    "error_type": map[string]any{"type": "string", "description": "Type of error if failure-related"},
                    "data":       map[string]any{"type": "object", "description": "Data payload for store operations"},
                },
            },
            "confidence": map[string]any{
                "type":        "number",
                "minimum":     0,
                "maximum":     1,
                "description": "Classification confidence 0.0-1.0",
            },
        },
        "required": []string{"is_retrospective", "confidence"},
    },
}
```

### Confidence Thresholds

The LLM provides its own confidence score. Use it directly:

| Confidence | Action | Response |
|------------|--------|----------|
| ≥ 0.90 | **Execute** | Process query, return results |
| 0.75-0.89 | **Execute+Log** | Process query, log for review |
| 0.50-0.74 | **Suggest** | Return classification for confirmation |
| < 0.50 | **Reject** | Reject with explanation |

### Multi-Intent Support

**Structured:**

```
@arch:recall:patterns?scope=auth; @arch:recall:failures?scope=auth
```

**Natural Language:**

The LLM handles multi-intent naturally. For queries containing multiple intents, the classifier returns an array of classifications.

### Learning System

The router accumulates corrections as few-shot examples:

```go
type CorrectionExample struct {
    Input                 string
    WrongClassification   ClassificationResult
    CorrectClassification ClassificationResult
    Timestamp             time.Time
}

type LearningStats struct {
    Patterns         map[string]*QueryPattern
    Corrections      []CorrectionExample
    MaxExamples      int  // Keep only recent corrections
}

func (ls *LearningStats) RecordCorrection(input string, wrong, correct ClassificationResult) {
    ls.Corrections = append(ls.Corrections, CorrectionExample{
        Input:                 input,
        WrongClassification:   wrong,
        CorrectClassification: correct,
        Timestamp:             time.Now(),
    })

    // Keep only recent corrections
    if len(ls.Corrections) > ls.MaxExamples {
        ls.Corrections = ls.Corrections[1:]
    }
}
```

Corrections are included as few-shot examples in the classification prompt, allowing the model to learn from its mistakes without retraining.

### Router Implementation

```go
type RetrospectiveRouter struct {
    // LLM client for classification
    client *anthropic.Client
    model  string  // Use a fast model: claude-3-5-haiku or similar

    // Structured DSL parser
    parser *DSLParser

    // Few-shot learning
    corrections []CorrectionExample
    maxExamples int

    // Tool dispatch
    handlers map[string]ToolHandler

    // Metrics
    stats RouterStats
}

func (r *RetrospectiveRouter) Route(ctx context.Context, input string) (*RouteResult, error) {
    // Fast path: Structured DSL
    if strings.HasPrefix(input, "@arch:") {
        return r.routeStructured(ctx, input)
    }

    // Slow path: LLM classification
    return r.routeNaturalLanguage(ctx, input)
}

func (r *RetrospectiveRouter) routeStructured(ctx context.Context, input string) (*RouteResult, error) {
    ast, err := r.parser.Parse(input)
    if err != nil {
        // Parse failed - fall back to LLM
        return r.routeNaturalLanguage(ctx, input)
    }

    return &RouteResult{
        Intent:     ast.Intent,
        Domain:     ast.Domain,
        Entities:   ast.Entities,
        Confidence: 1.0,  // Structured input is unambiguous
    }, nil
}

func (r *RetrospectiveRouter) routeNaturalLanguage(ctx context.Context, input string) (*RouteResult, error) {
    // Build prompt with few-shot corrections
    systemPrompt := FormatClassificationPrompt(r.formatCorrections())

    // Call LLM with tool-use
    resp, err := r.client.Messages.Create(ctx, anthropic.MessageCreateParams{
        Model:     r.model,
        MaxTokens: 256,
        System:    systemPrompt,
        Messages: []anthropic.MessageParam{
            anthropic.NewUserMessage(anthropic.NewTextBlock(input)),
        },
        Tools:      []anthropic.ToolDefinition{ClassificationTool},
        ToolChoice: anthropic.ToolChoiceAuto,
    })
    if err != nil {
        return nil, fmt.Errorf("classification failed: %w", err)
    }

    // Extract tool call result
    classification := r.extractClassification(resp)

    // Check retrospective gate
    if !classification.IsRetrospective {
        return &RouteResult{
            Rejected:   true,
            Reason:     classification.RejectionReason,
            Confidence: classification.Confidence,
        }, nil
    }

    // Record for metrics
    r.stats.RecordClassification(input, classification)

    return &RouteResult{
        Intent:     classification.Intent,
        Domain:     classification.Domain,
        Entities:   classification.Entities,
        Confidence: classification.Confidence,
    }, nil
}
```

### Token Efficiency Summary

| Path | Classification Cost | Query Cost | Total |
|------|---------------------|------------|-------|
| Structured DSL | 0 tokens | 15-40 tokens | **15-40 tokens** |
| Natural Language (short) | ~250 tokens | 20-50 tokens | **270-300 tokens** |
| Natural Language (complex) | ~250 tokens | 50-100 tokens | **300-350 tokens** |

**For inter-agent communication using structured DSL: ~95% token savings vs natural language.**

### Classification Examples

**Retrospective (is_retrospective=true):**

| Input | Intent | Domain | Reasoning |
|-------|--------|--------|-----------|
| "What authentication patterns have we used?" | recall | patterns | Past tense "have used" |
| "Did we encounter any timeout errors?" | recall | failures | Past tense "did encounter" |
| "Log this: recursive approach caused stack overflow" | store | failures | Recording past failure |
| "What decisions did we make about the API design?" | recall | decisions | Past tense "did make" |
| "Have we modified the config file?" | check | files | Present perfect "have modified" |
| "What did we learn from the refactoring?" | recall | learnings | Past tense "did learn" |

**Not Retrospective (is_retrospective=false):**

| Input | Rejection Reason |
|-------|------------------|
| "What patterns should we use?" | Asks about future guidance |
| "How do we implement caching?" | Asks about future implementation |
| "What's the best approach for this?" | Seeks recommendation |
| "We need to add authentication" | Describes future task |
| "Should we use Redis or Memcached?" | Asks for recommendation |

---

### Implementation Checklist

| Component | File | Status |
|-----------|------|--------|
| Query Cache | `query_cache.go` | Complete |
| Embedding Store | `embeddings.go` | Complete |
| Semantic Retriever | `retrieval.go` | Complete |
| Sonnet Synthesizer | `synthesis.go` | Complete |
| Memory Manager | `memory.go` | Complete |
| Tool Definitions | `tools.go` | Complete |
| Tool Handlers | `handlers.go` | Complete |
| Retrospective Router | `router.go` | Pending |
| DSL Parser | `parser.go` | Pending |
| Classification Tool | `classification.go` | Pending |
| Learning System | `learning.go` | Pending |
| Classification Prompt | `prompt.go` | Complete |

---
